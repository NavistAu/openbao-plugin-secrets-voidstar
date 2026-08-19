package backend

import (
	"context"
	"errors"
	"fmt"

	api "github.com/openbao/openbao/api/v2"
)

// LoopbackClient is the seam between the backend and the OpenBao API
// client voidstar authenticates to its own instance with, to
// dereference targets. Every loopback call goes through here so
// tests substitute a fake (loopback_fake.go) — no network in tests.
type LoopbackClient interface {
	// Read performs a logical read of path and returns its data
	// unmodified (adapter unwrapping is Task 5's job) along with the
	// lease fields the static-contract detection needs.
	Read(ctx context.Context, path string) (data map[string]interface{}, leaseID string, renewable bool, err error)
	// RevokeLease revokes leaseID via
	// sys/leases/revoke. Revoking an already-gone
	// lease ID does not error (idempotent revoke) — callers must not
	// infer "there was nothing to revoke" from a nil error.
	RevokeLease(ctx context.Context, leaseID string) error
	// RevokeSelf revokes the loopback token itself
	// (auth/token/revoke-self), cascading to its child leases. Used as
	// the fallback when RevokeLease fails.
	RevokeSelf(ctx context.Context) error
	// RenewSelf renews the loopback token ahead of its TTL
	// and returns the renewed TTL in seconds.
	RenewSelf(ctx context.Context) (ttlSeconds int, err error)
}

// errLoopbackTargetNotFound is returned by sdkLoopbackClient.Read when
// the target path itself doesn't exist: the api/v2 client surfaces
// that as (nil, nil), not an error (the same "implicit 404"
// behavior applies to the loopback read too), so it's turned into an
// explicit error here rather than risking a nil-Secret panic
// downstream.
var errLoopbackTargetNotFound = errors.New("voidstar: loopback target not found")

// sdkLoopbackClient is the concrete LoopbackClient wrapping a real
// api/v2 client already authenticated (newSDKLoopbackClient performs
// the AppRole login at construction).
type sdkLoopbackClient struct {
	client *api.Client
}

var _ LoopbackClient = (*sdkLoopbackClient)(nil)

// newSDKLoopbackClient builds a LoopbackClient by logging into cfg's
// AppRole against cfg.APIAddr (a raw
// login call — api/v2 has no AppRole helper package) and returns it
// along with the login's token TTL in seconds (SecretAuth.LeaseDuration
// — this is the token TTL, not a lease_id) so the caller can seed
// its renew-ahead-of-TTL tracking without a separate call.
func newSDKLoopbackClient(ctx context.Context, cfg *Config) (LoopbackClient, int, error) {
	apiCfg := api.DefaultConfig()
	apiCfg.Address = cfg.APIAddr
	c, err := api.NewClient(apiCfg)
	if err != nil {
		return nil, 0, fmt.Errorf("voidstar: loopback client: %w", err)
	}

	loginPath := fmt.Sprintf("auth/%s/login", cfg.ApproleMount)
	secret, err := c.Logical().WriteWithContext(ctx, loginPath, map[string]interface{}{
		"role_id":   cfg.RoleID,
		"secret_id": cfg.SecretID,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("voidstar: loopback login: %w", err)
	}
	if secret == nil || secret.Auth == nil {
		return nil, 0, errors.New("voidstar: loopback login returned no auth")
	}
	c.SetToken(secret.Auth.ClientToken)

	return &sdkLoopbackClient{client: c}, secret.Auth.LeaseDuration, nil
}

func (s *sdkLoopbackClient) Read(ctx context.Context, path string) (map[string]interface{}, string, bool, error) {
	secret, err := s.client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return nil, "", false, err
	}
	if secret == nil {
		return nil, "", false, errLoopbackTargetNotFound
	}
	return secret.Data, secret.LeaseID, secret.Renewable, nil
}

func (s *sdkLoopbackClient) RevokeLease(ctx context.Context, leaseID string) error {
	return s.client.Sys().RevokeWithContext(ctx, leaseID)
}

func (s *sdkLoopbackClient) RevokeSelf(ctx context.Context) error {
	return s.client.Auth().Token().RevokeSelfWithContext(ctx, "")
}

func (s *sdkLoopbackClient) RenewSelf(ctx context.Context) (int, error) {
	secret, err := s.client.Auth().Token().RenewSelfWithContext(ctx, 0)
	if err != nil {
		return 0, err
	}
	if secret == nil || secret.Auth == nil {
		return 0, errors.New("voidstar: loopback renew-self returned no auth")
	}
	return secret.Auth.LeaseDuration, nil
}

// is403 classifies a loopback call error as an HTTP 403 ("Any 403 or
// token-expiry on a loopback call invalidates the client ... expiry is
// indistinguishable from revocation and both are handled by the same
// re-auth path" — so a token-expiry manifests as a 403
// permission-denied response from the revoke-self
// probe, and needs no separate classification). Handles both the real
// api/v2 client's *api.ResponseError (a StatusCode field) and the
// fake's *statusError (a StatusCode method) so the same classification
// logic runs unchanged against either.
func is403(err error) bool {
	if err == nil {
		return false
	}
	var respErr *api.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == 403
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.code == 403
	}
	return false
}
