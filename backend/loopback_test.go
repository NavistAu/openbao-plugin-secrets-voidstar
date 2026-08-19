package backend

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/openbao/openbao/sdk/v2/logical"
)

func TestIs403(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "fake 403", err: newStatusError(403, ""), want: true},
		{name: "fake 401", err: newStatusError(401, ""), want: false},
		{name: "fake 500", err: newStatusError(500, ""), want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "wrapped fake 403", err: fmt.Errorf("context: %w", newStatusError(403, "denied")), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := is403(tc.err); got != tc.want {
				t.Errorf("is403(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestLoopbackGovernor_BackoffAndRecovery(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	g := newLoopbackGovernor(clock)

	if !g.allowed() {
		t.Fatal("fresh governor should allow an attempt")
	}

	g.recordResult(errors.New("connection refused"))
	if g.allowed() {
		t.Fatal("immediately after a failure, should be backed off")
	}
	failures, lastErr := g.snapshot()
	if failures != 1 || lastErr != "connection refused" {
		t.Errorf("snapshot = (%d, %q), want (1, %q)", failures, lastErr, "connection refused")
	}

	now = now.Add(2 * time.Second) // past the 1s (2^0) backoff for 1 failure
	if !g.allowed() {
		t.Fatal("should be allowed again after the backoff window elapses")
	}

	g.recordResult(nil)
	failures, lastErr = g.snapshot()
	if failures != 0 || lastErr != "" {
		t.Errorf("snapshot after success = (%d, %q), want (0, \"\")", failures, lastErr)
	}
}

// newLoopbackTestBackend builds a *Backend with config already written
// and a fake loopback client factory, so loopback lifecycle tests don't
// need a live storage round trip through pathConfigWrite.
func newLoopbackTestBackend(t *testing.T) (b *Backend, storage logical.Storage, fake *FakeLoopbackClient, factoryCalls *int) {
	t.Helper()
	b, storage = newTestBackend(t)

	if _, err := writeConfig(t, b, storage, validConfigData()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	fake = NewFakeLoopbackClient()
	calls := 0
	b.clientFactory = func(ctx context.Context, cfg *Config) (LoopbackClient, int, error) {
		calls++
		return fake, 300, nil
	}
	return b, storage, fake, &calls
}

func TestEnsureLoopbackClient_LazyInitAndReuse(t *testing.T) {
	b, _, fake, calls := newLoopbackTestBackend(t)

	c1, err := b.ensureLoopbackClient(context.Background())
	if err != nil {
		t.Fatalf("ensureLoopbackClient: %v", err)
	}
	if c1 != fake {
		t.Fatalf("ensureLoopbackClient returned %v, want the fake", c1)
	}
	if *calls != 1 {
		t.Fatalf("clientFactory calls = %d, want 1", *calls)
	}

	c2, err := b.ensureLoopbackClient(context.Background())
	if err != nil {
		t.Fatalf("ensureLoopbackClient (second call): %v", err)
	}
	if c2 != c1 {
		t.Fatal("second ensureLoopbackClient call should reuse the same client")
	}
	if *calls != 1 {
		t.Fatalf("clientFactory calls after reuse = %d, want 1 (no re-login)", *calls)
	}
}

func TestEnsureLoopbackClient_NoConfig(t *testing.T) {
	b, _ := newTestBackend(t)

	_, err := b.ensureLoopbackClient(context.Background())
	if !errors.Is(err, errLoopbackNotConfigured) {
		t.Fatalf("ensureLoopbackClient with no config: err = %v, want errLoopbackNotConfigured", err)
	}
}

func TestEnsureLoopbackClient_BackoffOnFailure(t *testing.T) {
	b, storage := newTestBackend(t)
	if _, err := writeConfig(t, b, storage, validConfigData()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	calls := 0
	b.clientFactory = func(ctx context.Context, cfg *Config) (LoopbackClient, int, error) {
		calls++
		return nil, 0, errors.New("connection refused")
	}

	_, err := b.ensureLoopbackClient(context.Background())
	if err == nil {
		t.Fatal("ensureLoopbackClient: want error on factory failure")
	}
	if calls != 1 {
		t.Fatalf("clientFactory calls = %d, want 1", calls)
	}

	// Immediately retrying must not call the factory again — the
	// governor's backoff window (>=1s for 1 failure) hasn't elapsed.
	_, err = b.ensureLoopbackClient(context.Background())
	if !errors.Is(err, errLoopbackUnavailable) {
		t.Fatalf("second ensureLoopbackClient: err = %v, want errLoopbackUnavailable", err)
	}
	if calls != 1 {
		t.Fatalf("clientFactory calls after immediate retry = %d, want 1 (backed off)", calls)
	}
}

func TestHandleLoopbackErr_403Invalidates(t *testing.T) {
	b, _, fake, calls := newLoopbackTestBackend(t)

	if _, err := b.ensureLoopbackClient(context.Background()); err != nil {
		t.Fatalf("ensureLoopbackClient: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("clientFactory calls = %d, want 1", *calls)
	}

	b.handleLoopbackErr(newStatusError(403, "permission denied"))

	b.mu.RLock()
	invalidated := b.client == nil
	b.mu.RUnlock()
	if !invalidated {
		t.Fatal("handleLoopbackErr(403) did not invalidate the client")
	}

	// Lazy re-login: the next ensureLoopbackClient call constructs a
	// fresh client (spec §4: "invalidates the client and triggers
	// re-login").
	c, err := b.ensureLoopbackClient(context.Background())
	if err != nil {
		t.Fatalf("ensureLoopbackClient after invalidation: %v", err)
	}
	if c != fake {
		t.Fatalf("re-login returned %v, want the fake", c)
	}
	if *calls != 2 {
		t.Fatalf("clientFactory calls after re-login = %d, want 2", *calls)
	}
}

func TestHandleLoopbackErr_NonNon403DoesNotInvalidate(t *testing.T) {
	b, _, _, calls := newLoopbackTestBackend(t)

	if _, err := b.ensureLoopbackClient(context.Background()); err != nil {
		t.Fatalf("ensureLoopbackClient: %v", err)
	}

	cases := []error{
		errors.New("connection refused"),
		newStatusError(500, "internal error"),
		newStatusError(401, "unauthenticated"),
	}
	for _, err := range cases {
		b.handleLoopbackErr(err)
	}

	b.mu.RLock()
	stillSet := b.client != nil
	b.mu.RUnlock()
	if !stillSet {
		t.Fatal("a non-403 loopback error must not invalidate the client")
	}
	if *calls != 1 {
		t.Fatalf("clientFactory calls = %d, want 1 (no re-login triggered)", *calls)
	}
}

func TestPeriodic_RenewalFiresWhenDue(t *testing.T) {
	b, storage, fake, _ := newLoopbackTestBackend(t)
	fake.RenewSelfTTL = 300

	fixed := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return fixed }

	if _, err := b.ensureLoopbackClient(context.Background()); err != nil {
		t.Fatalf("ensureLoopbackClient: %v", err)
	}

	// TTL is 300s; the margin is 2/3, so 201s elapsed is due.
	b.now = func() time.Time { return fixed.Add(201 * time.Second) }

	req := &logical.Request{Storage: storage}
	if err := b.periodic(context.Background(), req); err != nil {
		t.Fatalf("periodic: %v", err)
	}

	if fake.RenewSelfCalls != 1 {
		t.Fatalf("RenewSelfCalls = %d, want 1", fake.RenewSelfCalls)
	}

	b.mu.RLock()
	renewedAt := b.tokenRenewedAt
	ttl := b.tokenTTL
	b.mu.RUnlock()
	if !renewedAt.Equal(fixed.Add(201 * time.Second)) {
		t.Errorf("tokenRenewedAt = %v, want %v", renewedAt, fixed.Add(201*time.Second))
	}
	if ttl != 300*time.Second {
		t.Errorf("tokenTTL = %v, want 300s", ttl)
	}
}

func TestPeriodic_RenewalNotYetDue(t *testing.T) {
	b, storage, fake, _ := newLoopbackTestBackend(t)

	fixed := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return fixed }

	if _, err := b.ensureLoopbackClient(context.Background()); err != nil {
		t.Fatalf("ensureLoopbackClient: %v", err)
	}

	// Only 10s elapsed, well under the 200s (2/3 of 300s) margin.
	b.now = func() time.Time { return fixed.Add(10 * time.Second) }

	req := &logical.Request{Storage: storage}
	if err := b.periodic(context.Background(), req); err != nil {
		t.Fatalf("periodic: %v", err)
	}
	if fake.RenewSelfCalls != 0 {
		t.Fatalf("RenewSelfCalls = %d, want 0 (not due yet)", fake.RenewSelfCalls)
	}
}

func TestPeriodic_RenewalFailure403Invalidates(t *testing.T) {
	b, storage, fake, calls := newLoopbackTestBackend(t)

	fixed := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return fixed }
	if _, err := b.ensureLoopbackClient(context.Background()); err != nil {
		t.Fatalf("ensureLoopbackClient: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("clientFactory calls = %d, want 1", *calls)
	}

	fake.RenewSelfErr = newStatusError(403, "token expired")
	b.now = func() time.Time { return fixed.Add(201 * time.Second) }

	req := &logical.Request{Storage: storage}
	if err := b.periodic(context.Background(), req); err != nil {
		t.Fatalf("periodic: %v", err)
	}
	if fake.RenewSelfCalls != 1 {
		t.Fatalf("RenewSelfCalls = %d, want 1", fake.RenewSelfCalls)
	}

	b.mu.RLock()
	invalidated := b.client == nil
	b.mu.RUnlock()
	if !invalidated {
		t.Fatal("a 403 renewal failure must invalidate the client")
	}

	// Next tick re-logs-in lazily.
	if err := b.periodic(context.Background(), req); err != nil {
		t.Fatalf("periodic (second tick): %v", err)
	}
	if *calls != 2 {
		t.Fatalf("clientFactory calls after re-login tick = %d, want 2", *calls)
	}
}

func TestPeriodic_RenewalFailureNonNon403DoesNotInvalidate(t *testing.T) {
	b, storage, fake, calls := newLoopbackTestBackend(t)

	fixed := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return fixed }
	if _, err := b.ensureLoopbackClient(context.Background()); err != nil {
		t.Fatalf("ensureLoopbackClient: %v", err)
	}

	fake.RenewSelfErr = errors.New("connection reset")
	b.now = func() time.Time { return fixed.Add(201 * time.Second) }

	req := &logical.Request{Storage: storage}
	if err := b.periodic(context.Background(), req); err != nil {
		t.Fatalf("periodic: %v", err)
	}
	if fake.RenewSelfCalls != 1 {
		t.Fatalf("RenewSelfCalls = %d, want 1", fake.RenewSelfCalls)
	}

	b.mu.RLock()
	stillSet := b.client != nil
	b.mu.RUnlock()
	if !stillSet {
		t.Fatal("a non-403 renewal failure must not invalidate the client")
	}
	if *calls != 1 {
		t.Fatalf("clientFactory calls = %d, want 1 (no re-login triggered)", *calls)
	}
}
