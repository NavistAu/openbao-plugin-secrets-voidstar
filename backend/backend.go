// Package backend implements the "voidstar" OpenBao secrets engine,
// which serves read-only virtual views over pointers into other
// mounts.
package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

const backendHelp = `
The voidstar secrets engine serves virtual views: paths that hold no
secret material of their own, only references ("pointers") to real
paths in other mounts. Reading a view path dereferences the pointer
server-side and returns the target's value.
`

// Backend is the voidstar secrets engine.
type Backend struct {
	*framework.Backend

	mu sync.RWMutex

	// config is the live, validated configuration — the in-memory
	// counterpart of the `admin/config` storage entry, refreshed on
	// every successful config write and loaded on mount (initialize).
	config *Config

	// client is the current loopback client,
	// constructed lazily by ensureLoopbackClient and invalidated on a
	// classified-403 loopback error (handleLoopbackErr) for lazy
	// re-login. clientFactory builds one from a validated Config —
	// overridable so tests substitute a fake and observe
	// re-initialization after invalidation.
	client        LoopbackClient
	clientFactory func(ctx context.Context, cfg *Config) (LoopbackClient, int, error)
	// loopbackGov paces retries of loopback client construction
	// (governor.go) after a failed attempt.
	loopbackGov *loopbackGovernor

	// tokenTTL/tokenRenewedAt track the loopback token's renew-ahead-
	// of-TTL schedule: seeded by ensureLoopbackClient's login
	// TTL, refreshed by a successful periodic renewal, zeroed on
	// invalidation.
	tokenTTL       time.Duration
	tokenRenewedAt time.Time

	// now is the backend's clock, overridable in tests so renewal
	// timing (renewLoopbackTokenIfDue) doesn't depend on wall-clock
	// sleeps.
	now func() time.Time

	// failureCounters backs the admin/status bookkeeping: dereference
	// failures keyed by target mount, then by failure class. Never
	// holds secret material — only mount names and fixed class strings.
	failureCounters map[string]map[string]int
}

// Factory returns a configured Backend as a logical.Backend, matching
// the logical.Factory signature the OpenBao plugin catalog invokes.
func Factory(ctx context.Context, conf *logical.BackendConfig) (logical.Backend, error) {
	b := newBackend()
	if err := b.Setup(ctx, conf); err != nil {
		return nil, err
	}
	return b, nil
}

func newBackend() *Backend {
	b := &Backend{now: time.Now, failureCounters: map[string]map[string]int{}}
	b.clientFactory = newSDKLoopbackClient
	b.loopbackGov = newLoopbackGovernor(time.Now)
	b.Backend = &framework.Backend{
		Help:           strings.TrimSpace(backendHelp),
		BackendType:    logical.TypeLogical,
		Paths:          backendPaths(b),
		InitializeFunc: b.initialize,
		PeriodicFunc:   b.periodic,
	}
	return b
}

// backendPaths assembles the full Paths slice. Order matters:
// framework routing is a first-match-wins linear scan with no
// specificity tiebreaking — more specific/literal
// patterns must be registered before general catch-alls. The admin
// family and the KV-reserved paths are all mutually non-overlapping
// literal prefixes (anchored full-path match — "metadata/*" cannot
// shadow "detailed-metadata/*", they don't share a common prefix), so
// their relative order doesn't matter; pathNotFound's ".*" catch-all
// is the one pattern that MUST come last, or it would shadow
// everything registered after it.
func backendPaths(b *Backend) []*framework.Path {
	paths := []*framework.Path{
		pathConfig(b),
		pathMap(b),
		pathStatus(b),
		pathData(b),
		pathMetadata(b),
	}
	paths = append(paths, reservedPaths(b)...)
	paths = append(paths, pathNotFound(b))
	return paths
}

// currentConfig returns the backend's live config, or nil if none has
// been written yet.
func (b *Backend) currentConfig() *Config {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.config
}

// initialize is the framework.Backend InitializeFunc, invoked just
// after the plugin is mounted. Config load is a pure storage read —
// no client construction, no network — so a restart never blocks on
// the loopback client being reachable; a missing or
// unreadable config just leaves b.config nil rather than failing the
// mount.
func (b *Backend) initialize(ctx context.Context, req *logical.InitializationRequest) error {
	cfg, err := getConfigFromStorage(ctx, req.Storage)
	if err != nil || cfg == nil {
		return nil
	}
	b.mu.Lock()
	b.config = cfg
	b.mu.Unlock()
	return nil
}

// errLoopbackNotConfigured is returned by ensureLoopbackClient when no
// config has been written yet — distinct from errLoopbackUnavailable
// (config exists, construction is failing/backing off).
var errLoopbackNotConfigured = errors.New("voidstar: loopback client not configured")

// errLoopbackUnavailable is returned by ensureLoopbackClient when
// construction hasn't succeeded yet (in backoff, or the most recent
// attempt failed) — mirrors the sibling's errClientUnavailable.
var errLoopbackUnavailable = errors.New("voidstar: loopback client unavailable, retrying")

// ensureLoopbackClient returns the backend's current LoopbackClient,
// constructing it lazily (and retryably, loopbackGov-paced) if it
// doesn't exist yet — the client initializes lazily on first
// use, with backoff, and init failure surfaces in status — never
// wedging the mount. A successful construction seeds
// tokenTTL/tokenRenewedAt from the login's TTL so renewLoopbackTokenIfDue
// has a schedule to work from without a separate call.
func (b *Backend) ensureLoopbackClient(ctx context.Context) (LoopbackClient, error) {
	b.mu.RLock()
	client := b.client
	cfg := b.config
	b.mu.RUnlock()
	if client != nil {
		return client, nil
	}
	if cfg == nil {
		return nil, errLoopbackNotConfigured
	}
	if !b.loopbackGov.allowed() {
		return nil, errLoopbackUnavailable
	}

	newClient, ttlSeconds, err := b.clientFactory(ctx, cfg)
	b.loopbackGov.recordResult(err)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errLoopbackUnavailable, err)
	}

	b.mu.Lock()
	b.client = newClient
	b.tokenTTL = time.Duration(ttlSeconds) * time.Second
	b.tokenRenewedAt = b.now()
	b.mu.Unlock()
	return newClient, nil
}

// invalidateLoopbackClient discards the current client and its TTL
// schedule, forcing the next ensureLoopbackClient call to lazily
// re-login.
func (b *Backend) invalidateLoopbackClient() {
	b.mu.Lock()
	b.client = nil
	b.tokenTTL = 0
	b.tokenRenewedAt = time.Time{}
	b.mu.Unlock()
}

// handleLoopbackErr classifies err from any loopback call (Read,
// RenewSelf, ...): a 403 — indistinguishable from token
// expiry, both handled by the same re-auth path — invalidates the
// client for lazy re-login; any other error leaves the client in
// place, since a transient failure is not evidence the token itself is
// bad. The dereference path also calls this; periodic only wires
// it into renewal here.
func (b *Backend) handleLoopbackErr(err error) {
	if is403(err) {
		b.invalidateLoopbackClient()
	}
}

// recordFailure increments mount's counter for failure class. Called
// from every dereference
// failure site (dereference.go, paths_data.go) with one of the
// failureClass* constants (status.go).
func (b *Backend) recordFailure(mount, class string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failureCounters[mount] == nil {
		b.failureCounters[mount] = map[string]int{}
	}
	b.failureCounters[mount][class]++
}

// loopbackRenewMarginFraction triggers renewal once this fraction of
// the token's TTL has elapsed.
const loopbackRenewMarginFraction = 2.0 / 3.0

// renewLoopbackTokenIfDue renews the loopback token once
// loopbackRenewMarginFraction of its TTL has elapsed since the last
// login/renewal. Driven by periodic (the framework's periodic hook),
// not a goroutine timer, per the plan. A renewal failure is classified
// through handleLoopbackErr exactly like any other loopback call: a
// 403 invalidates the client, anything else is left for the next tick.
func (b *Backend) renewLoopbackTokenIfDue(ctx context.Context) {
	b.mu.RLock()
	client := b.client
	ttl := b.tokenTTL
	renewedAt := b.tokenRenewedAt
	b.mu.RUnlock()
	if client == nil || ttl <= 0 {
		return
	}
	if b.now().Sub(renewedAt) < time.Duration(float64(ttl)*loopbackRenewMarginFraction) {
		return
	}

	newTTLSeconds, err := client.RenewSelf(ctx)
	if err != nil {
		b.handleLoopbackErr(err)
		return
	}

	b.mu.Lock()
	b.tokenTTL = time.Duration(newTTLSeconds) * time.Second
	b.tokenRenewedAt = b.now()
	b.mu.Unlock()
}

// periodic is the framework.Backend PeriodicFunc: drives lazy loopback
// client (re-)construction retries and the renew-ahead-of-TTL
// schedule. A construction failure is left for the next tick
// (loopbackGov already recorded and paced it) rather than returned —
// mirrors the sibling's coldStart, a failed retry must not fail the
// tick itself.
func (b *Backend) periodic(ctx context.Context, req *logical.Request) error {
	if b.currentConfig() == nil {
		return nil
	}
	if _, err := b.ensureLoopbackClient(ctx); err != nil {
		return nil
	}
	b.renewLoopbackTokenIfDue(ctx)
	return nil
}
