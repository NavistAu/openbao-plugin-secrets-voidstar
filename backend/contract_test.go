package backend

import (
	"context"
	"strings"
	"testing"

	"github.com/openbao/openbao/sdk/v2/logical"
)

// TestStaticContract_LeaseBearingRevokesAndQuarantines drives the
// lease-bearing violation branch end-to-end: a non-empty
// lease_id on the loopback response must be revoked, the mapping
// quarantined with cause+outcome persisted, and the triggering read
// fails 502 — with no further loopback read on a subsequent attempt
// (fast-fail).
func TestStaticContract_LeaseBearingRevokesAndQuarantines(t *testing.T) {
	b, storage, fake, logs := newDereferenceTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db_password"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data:    map[string]interface{}{"data": map[string]interface{}{"a": "b"}},
		LeaseID: "lease-123",
	}

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("read with lease-bearing response: want error")
	}
	if got := codedStatus(rerr); got != 502 {
		t.Fatalf("status = %d, want 502", got)
	}
	if fake.RevokeLeaseCalls != 1 {
		t.Fatalf("RevokeLeaseCalls = %d, want 1", fake.RevokeLeaseCalls)
	}
	if fake.RevokeSelfCalls != 0 {
		t.Fatalf("RevokeSelfCalls = %d, want 0 (revoke succeeded, no need to recycle)", fake.RevokeSelfCalls)
	}
	if !strings.Contains(logs.String(), "static-contract violation") {
		t.Errorf("log missing lease-violation cause: %s", logs.String())
	}

	entry, err := getMappingFromStorage(context.Background(), storage, view)
	if err != nil || entry == nil {
		t.Fatalf("getMappingFromStorage: entry=%v err=%v", entry, err)
	}
	if !entry.Quarantined {
		t.Fatal("mapping not quarantined after lease-bearing violation")
	}
	if entry.QuarantineCause != "lease-bearing target response" {
		t.Errorf("QuarantineCause = %q, want %q", entry.QuarantineCause, "lease-bearing target response")
	}
	if entry.RevocationOutcome != "revoked" {
		t.Errorf("RevocationOutcome = %q, want %q", entry.RevocationOutcome, "revoked")
	}
	// Target/Adapter/WriteTime must survive the quarantine rewrite
	// unchanged (only an explicit vs/admin/map rewrite clears them).
	if entry.Target != "kv/data/x" {
		t.Errorf("Target = %q, want preserved kv/data/x", entry.Target)
	}

	// Subsequent read fast-fails without touching the loopback again.
	fake.ReadCalls = 0
	_, rerr2 := readData(t, b, storage, view)
	if rerr2 == nil {
		t.Fatal("second read of quarantined view: want error")
	}
	if fake.ReadCalls != 0 {
		t.Fatalf("quarantine fast-fail must not call the loopback: ReadCalls = %d, want 0", fake.ReadCalls)
	}
}

// TestStaticContract_RenewableOnlySkipsRevoke covers the renewable-
// only branch (empty lease_id, renewable=true): there is nothing to
// revoke, so RevokeLease/RevokeSelf must not be called, but the
// mapping is still quarantined and the read still fails.
func TestStaticContract_RenewableOnlySkipsRevoke(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data:      map[string]interface{}{"data": map[string]interface{}{"a": "b"}},
		Renewable: true,
	}

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("read with renewable-only response: want error")
	}
	if got := codedStatus(rerr); got != 502 {
		t.Fatalf("status = %d, want 502", got)
	}
	if fake.RevokeLeaseCalls != 0 {
		t.Fatalf("RevokeLeaseCalls = %d, want 0 (nothing to revoke)", fake.RevokeLeaseCalls)
	}
	if fake.RevokeSelfCalls != 0 {
		t.Fatalf("RevokeSelfCalls = %d, want 0", fake.RevokeSelfCalls)
	}

	entry, err := getMappingFromStorage(context.Background(), storage, view)
	if err != nil || entry == nil {
		t.Fatalf("getMappingFromStorage: entry=%v err=%v", entry, err)
	}
	if !entry.Quarantined {
		t.Fatal("mapping not quarantined after renewable-only violation")
	}
	if entry.RevocationOutcome != "" {
		t.Errorf("RevocationOutcome = %q, want empty (nothing revoked)", entry.RevocationOutcome)
	}
}

// TestStaticContract_RevocationFailureRecyclesToken covers the
// RevokeLease-failure branch: the token must be
// recycled via RevokeSelf and invalidated for lazy re-login, and the
// revocation outcome persisted on the quarantine reflects the failure.
func TestStaticContract_RevocationFailureRecyclesToken(t *testing.T) {
	b, storage, fake, logs := newDereferenceTestBackend(t)

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data:    map[string]interface{}{"data": map[string]interface{}{"a": "b"}},
		LeaseID: "lease-456",
	}
	fake.RevokeLeaseErr["lease-456"] = newStatusError(500, "connection refused")

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("want error")
	}
	if fake.RevokeLeaseCalls != 1 {
		t.Fatalf("RevokeLeaseCalls = %d, want 1", fake.RevokeLeaseCalls)
	}
	if fake.RevokeSelfCalls != 1 {
		t.Fatalf("RevokeSelfCalls = %d, want 1 (revoke failed, must recycle token)", fake.RevokeSelfCalls)
	}
	if !strings.Contains(logs.String(), "lease revocation failed") {
		t.Errorf("log missing revocation-failure line: %s", logs.String())
	}

	b.mu.RLock()
	invalidated := b.client == nil
	b.mu.RUnlock()
	if !invalidated {
		t.Fatal("loopback client must be invalidated after a revocation failure (forces lazy re-login)")
	}

	entry, err := getMappingFromStorage(context.Background(), storage, view)
	if err != nil || entry == nil {
		t.Fatalf("getMappingFromStorage: entry=%v err=%v", entry, err)
	}
	if !entry.Quarantined {
		t.Fatal("mapping must still be quarantined even when revocation itself fails")
	}
	if !strings.Contains(entry.RevocationOutcome, "revoke failed") {
		t.Errorf("RevocationOutcome = %q, want it to record the revoke failure", entry.RevocationOutcome)
	}
}

// TestStaticContract_QuarantinePersistsAcrossReload proves quarantine
// state is durable storage, not in-memory-only: a fresh *Backend
// (simulating a plugin restart) reading the same storage still
// fast-fails the view without ever constructing a loopback client.
func TestStaticContract_QuarantinePersistsAcrossReload(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data:    map[string]interface{}{"data": map[string]interface{}{"a": "b"}},
		LeaseID: "lease-789",
	}
	if _, rerr := readData(t, b, storage, view); rerr == nil {
		t.Fatal("want error triggering quarantine")
	}

	// Fresh backend instance, same storage: initialize() reloads config
	// from storage; the mapping entry (with quarantine already set) is
	// read directly from storage by pathDataRead, independent of any
	// backend-instance state.
	b2 := newBackend()
	fake2 := NewFakeLoopbackClient()
	b2.clientFactory = func(ctx context.Context, cfg *Config) (LoopbackClient, int, error) {
		return fake2, 300, nil
	}
	conf := &logical.InitializationRequest{Storage: storage}
	if err := b2.Setup(context.Background(), &logical.BackendConfig{StorageView: storage, Logger: b.Logger(), System: logical.TestSystemView()}); err != nil {
		t.Fatalf("Setup (reloaded backend): %v", err)
	}
	if err := b2.initialize(context.Background(), conf); err != nil {
		t.Fatalf("initialize (reloaded backend): %v", err)
	}

	_, rerr2 := readData(t, b2, storage, view)
	if rerr2 == nil {
		t.Fatal("read after reload: want error (quarantine must persist)")
	}
	if fake2.ReadCalls != 0 {
		t.Fatalf("reloaded backend's fast-fail must not touch the loopback: ReadCalls = %d, want 0", fake2.ReadCalls)
	}
}

// TestStaticContract_RewriteClearsQuarantine is the integration
// assertion (through the read path) for the rewrite-clears-quarantine
// behavior already covered at the storage level
// (paths_map_test.go's TestPathMap_RewriteClearsQuarantine): after a
// quarantine-triggering read, rewriting the mapping via
// vs/admin/map/<view> must make the view readable again.
func TestStaticContract_RewriteClearsQuarantine(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data:    map[string]interface{}{"data": map[string]interface{}{"a": "b"}},
		LeaseID: "lease-abc",
	}
	if _, rerr := readData(t, b, storage, view); rerr == nil {
		t.Fatal("want error triggering quarantine")
	}

	// Rewrite the mapping ("quarantine clears only when the
	// mapping is rewritten via vs/admin/map/<view>").
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("rewrite mapping: %v", err)
	}

	// This time the target's static (no lease): the read must succeed.
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data: map[string]interface{}{"data": map[string]interface{}{"a": "b"}},
	}
	resp, rerr := readData(t, b, storage, view)
	if rerr != nil {
		t.Fatalf("read after rewrite: want success, got error: %v", rerr)
	}
	if resp == nil {
		t.Fatal("read after rewrite: nil response")
	}
}
