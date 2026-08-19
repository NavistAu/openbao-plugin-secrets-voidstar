// Command dynfake is a throwaway lease-emitting secrets engine, built
// only for the bench gate's mandatory lease/quarantine drill: its
// "leaky" read path mints a real,
// renewable lease via a *logical.Secret response, so the drive script
// can exercise voidstar's static-contract detection, revocation, and
// quarantine against a genuine minted lease over the real
// plugin RPC + HTTP wire — not the unit tests' scripted fake
// (backend/loopback_fake.go). Never built, registered, or shipped
// outside bench/.
package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// dynfakeBackend adds one field to framework.Backend: reads, an
// atomic counter the drive script polls via "count" to prove whether
// a second read ever reached this plugin — the quarantine fast-fail
// proof ("a subsequent read fast-fails without touching
// dynfake").
type dynfakeBackend struct {
	*framework.Backend
	reads int64
}

func factory(ctx context.Context, conf *logical.BackendConfig) (logical.Backend, error) {
	b := &dynfakeBackend{}
	secretType := &framework.Secret{
		Type:            "dynfake",
		DefaultDuration: 60 * time.Second,
		Revoke: func(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
			return nil, nil
		},
	}
	b.Backend = &framework.Backend{
		BackendType: logical.TypeLogical,
		Secrets:     []*framework.Secret{secretType},
		Paths: []*framework.Path{
			{
				Pattern: "count",
				Operations: map[logical.Operation]framework.OperationHandler{
					logical.ReadOperation: &framework.PathOperation{
						Callback: func(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
							return &logical.Response{Data: map[string]interface{}{
								"count": atomic.LoadInt64(&b.reads),
							}}, nil
						},
					},
				},
			},
			{
				Pattern: "leaky",
				Operations: map[logical.Operation]framework.OperationHandler{
					logical.ReadOperation: &framework.PathOperation{
						Callback: func(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
							atomic.AddInt64(&b.reads, 1)
							resp := secretType.Response(map[string]interface{}{"value": "dynfake-secret"}, nil)
							resp.Secret.Renewable = true
							return resp, nil
						},
					},
				},
			},
		},
	}
	if err := b.Setup(ctx, conf); err != nil {
		return nil, err
	}
	return b, nil
}
