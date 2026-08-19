package backend

import (
	"context"
	"testing"
	"time"

	"github.com/openbao/openbao/sdk/v2/logical"
)

// TestMappingEntry_StorageRoundTrip proves the map/<view> storage
// shape survives a JSON round trip through storage, independent of
// the CRUD handlers (paths_map.go) that use it.
func TestMappingEntry_StorageRoundTrip(t *testing.T) {
	_, storage := newTestBackend(t)
	ctx := context.Background()

	want := &MappingEntry{
		Target:            "kv/data/infra/postgres#password",
		Adapter:           "kv2",
		WriteTime:         time.Now().UTC().Truncate(time.Second),
		Quarantined:       true,
		QuarantineCause:   "lease-bearing target response",
		RevocationOutcome: "revoked",
	}

	entry, err := logical.StorageEntryJSON(mapStorageKey("example.com/iac/prod/svc.foo/db_password"), want)
	if err != nil {
		t.Fatalf("StorageEntryJSON: %v", err)
	}
	if err := storage.Put(ctx, entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := storage.Get(ctx, mapStorageKey("example.com/iac/prod/svc.foo/db_password"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: nil entry")
	}

	var loaded MappingEntry
	if err := got.DecodeJSON(&loaded); err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}

	if loaded.Target != want.Target {
		t.Errorf("Target = %v, want %v", loaded.Target, want.Target)
	}
	if loaded.Adapter != want.Adapter {
		t.Errorf("Adapter = %v, want %v", loaded.Adapter, want.Adapter)
	}
	if !loaded.WriteTime.Equal(want.WriteTime) {
		t.Errorf("WriteTime = %v, want %v", loaded.WriteTime, want.WriteTime)
	}
	if loaded.Quarantined != want.Quarantined {
		t.Errorf("Quarantined = %v, want %v", loaded.Quarantined, want.Quarantined)
	}
	if loaded.QuarantineCause != want.QuarantineCause {
		t.Errorf("QuarantineCause = %v, want %v", loaded.QuarantineCause, want.QuarantineCause)
	}
	if loaded.RevocationOutcome != want.RevocationOutcome {
		t.Errorf("RevocationOutcome = %v, want %v", loaded.RevocationOutcome, want.RevocationOutcome)
	}
}

func TestMapStorageKey_Prefix(t *testing.T) {
	if got, want := mapStorageKey("a/b/c"), "map/a/b/c"; got != want {
		t.Errorf("mapStorageKey(%q) = %q, want %q", "a/b/c", got, want)
	}
}
