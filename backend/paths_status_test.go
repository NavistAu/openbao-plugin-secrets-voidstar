package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/openbao/openbao/sdk/v2/logical"
)

func readStatus(t *testing.T, b *Backend, storage logical.Storage) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.ReadOperation,
		Path:       "admin/status",
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func TestPathStatus_MappingCountAndQuarantineListing(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	for _, v := range []string{"a/b", "a/c", "d/e"} {
		if _, err := writeMapping(t, b, storage, v, map[string]interface{}{"target": "kv/data/x"}); err != nil {
			t.Fatalf("write mapping %s: %v", v, err)
		}
	}

	// Quarantine one of them via a lease-bearing response.
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data:    map[string]interface{}{"data": map[string]interface{}{"v": "x"}},
		LeaseID: "lease-1",
	}
	if _, err := readData(t, b, storage, "a/b"); err == nil {
		t.Fatal("want error triggering quarantine")
	}

	resp, err := readStatus(t, b, storage)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if resp == nil {
		t.Fatal("read status: nil response")
	}

	if got := resp.Data["mapping_count"]; got != 3 {
		t.Errorf("mapping_count = %v, want 3", got)
	}

	quarantined, ok := resp.Data["quarantined_mappings"].([]map[string]interface{})
	if !ok || len(quarantined) != 1 {
		t.Fatalf("quarantined_mappings = %v, want exactly 1 entry", resp.Data["quarantined_mappings"])
	}
	q := quarantined[0]
	if q["view"] != "a/b" {
		t.Errorf("quarantined view = %v, want a/b", q["view"])
	}
	if q["quarantine_cause"] != "lease-bearing target response" {
		t.Errorf("quarantine_cause = %v, want %q", q["quarantine_cause"], "lease-bearing target response")
	}
	if q["revocation_outcome"] != "revoked" {
		t.Errorf("revocation_outcome = %v, want revoked", q["revocation_outcome"])
	}
}

// TestPathStatus_FailureCounters drives each of the five failure
// classes and asserts the corresponding counter increments, keyed by
// target mount.
func TestPathStatus_FailureCounters(t *testing.T) {
	t.Run("upstream_read_failure", func(t *testing.T) {
		b, storage, fake, _ := newDereferenceTestBackend(t)
		if _, err := writeMapping(t, b, storage, "some/view", map[string]interface{}{"target": "kv/data/x"}); err != nil {
			t.Fatalf("write mapping: %v", err)
		}
		fake.Reads["kv/data/x"] = FakeReadResponse{Err: newStatusError(500, "unreachable")}

		if _, err := readData(t, b, storage, "some/view"); err == nil {
			t.Fatal("want error")
		}
		assertFailureCounter(t, b, storage, "kv", failureClassUpstreamRead, 1)
	})

	t.Run("missing_field", func(t *testing.T) {
		b, storage, fake, _ := newDereferenceTestBackend(t)
		if _, err := writeMapping(t, b, storage, "some/view", map[string]interface{}{"target": "kv/data/x#nope"}); err != nil {
			t.Fatalf("write mapping: %v", err)
		}
		fake.Reads["kv/data/x"] = FakeReadResponse{Data: map[string]interface{}{"data": map[string]interface{}{"other": "v"}}}

		if _, err := readData(t, b, storage, "some/view"); err == nil {
			t.Fatal("want error")
		}
		assertFailureCounter(t, b, storage, "kv", failureClassMissingField, 1)
	})

	t.Run("lease_violation", func(t *testing.T) {
		b, storage, fake, _ := newDereferenceTestBackend(t)
		if _, err := writeMapping(t, b, storage, "some/view", map[string]interface{}{"target": "kv/data/x"}); err != nil {
			t.Fatalf("write mapping: %v", err)
		}
		fake.Reads["kv/data/x"] = FakeReadResponse{
			Data:    map[string]interface{}{"data": map[string]interface{}{"v": "x"}},
			LeaseID: "lease-1",
		}

		if _, err := readData(t, b, storage, "some/view"); err == nil {
			t.Fatal("want error")
		}
		assertFailureCounter(t, b, storage, "kv", failureClassLeaseViolation, 1)
	})

	t.Run("revocation_failure", func(t *testing.T) {
		b, storage, fake, _ := newDereferenceTestBackend(t)
		if _, err := writeMapping(t, b, storage, "some/view", map[string]interface{}{"target": "kv/data/x"}); err != nil {
			t.Fatalf("write mapping: %v", err)
		}
		fake.Reads["kv/data/x"] = FakeReadResponse{
			Data:    map[string]interface{}{"data": map[string]interface{}{"v": "x"}},
			LeaseID: "lease-1",
		}
		fake.RevokeLeaseErr["lease-1"] = newStatusError(500, "connection refused")

		if _, err := readData(t, b, storage, "some/view"); err == nil {
			t.Fatal("want error")
		}
		assertFailureCounter(t, b, storage, "kv", failureClassRevocationFailure, 1)
		// A revocation failure is also a lease violation.
		assertFailureCounter(t, b, storage, "kv", failureClassLeaseViolation, 1)
	})

	t.Run("quarantined_fastfail", func(t *testing.T) {
		b, storage, fake, _ := newDereferenceTestBackend(t)
		if _, err := writeMapping(t, b, storage, "some/view", map[string]interface{}{"target": "kv/data/x"}); err != nil {
			t.Fatalf("write mapping: %v", err)
		}
		fake.Reads["kv/data/x"] = FakeReadResponse{
			Data:    map[string]interface{}{"data": map[string]interface{}{"v": "x"}},
			LeaseID: "lease-1",
		}
		if _, err := readData(t, b, storage, "some/view"); err == nil {
			t.Fatal("want error triggering quarantine")
		}

		// The next read fast-fails on the now-quarantined mapping.
		if _, err := readData(t, b, storage, "some/view"); err == nil {
			t.Fatal("want error on quarantined re-read")
		}
		assertFailureCounter(t, b, storage, "kv", failureClassQuarantinedFastFail, 1)
	})
}

func assertFailureCounter(t *testing.T, b *Backend, storage logical.Storage, mount, class string, want int) {
	t.Helper()
	resp, err := readStatus(t, b, storage)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	counters, ok := resp.Data["failure_counters"].(map[string]map[string]int)
	if !ok {
		t.Fatalf("failure_counters = %v, want map[string]map[string]int", resp.Data["failure_counters"])
	}
	got := counters[mount][class]
	if got != want {
		t.Errorf("failure_counters[%q][%q] = %d, want %d (full: %+v)", mount, class, got, want, counters)
	}
}

// TestPathStatus_NoSecretID walks the status response looking for the
// configured secret_id value — it must appear nowhere (spec §6: the
// loopback SecretID is write-only, never readable back).
func TestPathStatus_NoSecretID(t *testing.T) {
	b, storage, _, _ := newDereferenceTestBackend(t)

	const sentinelSecretID = "secret-1" // set by validConfigData()

	resp, err := readStatus(t, b, storage)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if resp == nil {
		t.Fatal("read status: nil response")
	}

	blob, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal status response: %v", err)
	}
	if strings.Contains(string(blob), sentinelSecretID) {
		t.Fatalf("status response leaks configured secret_id: %s", blob)
	}
}

// TestPathStatus_NoTargetValuesInStorage walks ALL storage entries
// after a mix of reads and a quarantine drill, asserting a
// fake-returned sentinel target value appears nowhere in storage
// (spec §6: "target secret values are never persisted and never
// cached to storage").
func TestPathStatus_NoTargetValuesInStorage(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	const sentinelValue = "sentinel-target-value-should-never-be-persisted"

	if _, err := writeMapping(t, b, storage, "some/view", map[string]interface{}{"target": "kv/data/x#password"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data: map[string]interface{}{"data": map[string]interface{}{"password": sentinelValue}},
	}
	if _, err := readData(t, b, storage, "some/view"); err != nil {
		t.Fatalf("read: %v", err)
	}

	// Quarantine drill: a second, lease-bearing mapping.
	if _, err := writeMapping(t, b, storage, "other/view", map[string]interface{}{"target": "kv/data/y"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/y"] = FakeReadResponse{
		Data:    map[string]interface{}{"data": map[string]interface{}{"secret": sentinelValue}},
		LeaseID: "lease-1",
	}
	if _, err := readData(t, b, storage, "other/view"); err == nil {
		t.Fatal("want error triggering quarantine")
	}

	if _, err := readStatus(t, b, storage); err != nil {
		t.Fatalf("read status: %v", err)
	}

	entries, err := listAllStorageKeysRecursive(context.Background(), storage, "")
	if err != nil {
		t.Fatalf("walk storage: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("walk storage: found no entries at all, test is not exercising anything")
	}
	for _, key := range entries {
		se, err := storage.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("Get %q: %v", key, err)
		}
		if se == nil {
			continue
		}
		if strings.Contains(string(se.Value), sentinelValue) {
			t.Fatalf("storage entry %q contains the sentinel target value — target secret material was persisted", key)
		}
	}
}

// listAllStorageKeysRecursive is a test-only full storage walk (mirrors
// walkAllMappings's recursion, but over the whole keyspace rather than
// just map/*, so it also covers config).
func listAllStorageKeysRecursive(ctx context.Context, storage logical.Storage, prefix string) ([]string, error) {
	var out []string
	keys, err := storage.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("List(%q): %w", prefix, err)
	}
	for _, k := range keys {
		full := prefix + k
		if strings.HasSuffix(k, "/") {
			sub, err := listAllStorageKeysRecursive(ctx, storage, full)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		out = append(out, full)
	}
	return out, nil
}
