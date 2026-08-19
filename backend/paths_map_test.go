package backend

import (
	"context"
	"testing"
	"time"

	"github.com/openbao/openbao/sdk/v2/logical"
)

// TestCanonicalizeTarget is the bulk of the canonicalization coverage:
// every reject class and every accept class from the target grammar, exercised directly
// against the pure function (fast, no storage/backend involved).
func TestCanonicalizeTarget(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		ownMount string
		wantErr  bool
		wantMnt  string
	}{
		// --- reject classes ---
		{name: "empty target", target: "", wantErr: true},
		{name: "leading slash", target: "/kv/data/x", wantErr: true},
		{name: "trailing slash", target: "kv/data/x/", wantErr: true},
		{name: "double slash", target: "kv//data/x", wantErr: true},
		{name: "dot segment", target: "kv/./data/x", wantErr: true},
		{name: "dotdot segment", target: "kv/../data/x", wantErr: true},
		{name: "url-encoded", target: "kv/data/x%2Fy", wantErr: true},
		{name: "query string", target: "kv/data/x?version=1", wantErr: true},
		{name: "empty field suffix", target: "kv/data/x#", wantErr: true},
		{name: "multiple field suffixes", target: "kv/data/x#a#b", wantErr: true},
		{name: "mount only, no path", target: "kv", wantErr: true},
		{name: "own mount", target: "vs/data/x", ownMount: "vs", wantErr: true},
		{name: "own mount with field", target: "vs/data/x#f", ownMount: "vs", wantErr: true},

		// --- accept classes ---
		{name: "kv2 with field", target: "kv/data/infra/postgres#password", wantErr: false, wantMnt: "kv"},
		{name: "raw target no field", target: "op/field/Infra/db.example.com/foo-db", wantErr: false, wantMnt: "op"},
		{name: "whole map, no field", target: "kv/data/infra/postgres", wantErr: false, wantMnt: "kv"},
		{name: "multi-segment path", target: "kv/data/a/b/c/d#field", wantErr: false, wantMnt: "kv"},
		{name: "different mount than own", target: "kv/data/x", ownMount: "vs", wantErr: false, wantMnt: "kv"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mount, err := canonicalizeTarget(tc.target, tc.ownMount)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("canonicalizeTarget(%q, %q) = nil error, want error", tc.target, tc.ownMount)
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalizeTarget(%q, %q) unexpected error: %v", tc.target, tc.ownMount, err)
			}
			if mount != tc.wantMnt {
				t.Errorf("canonicalizeTarget(%q, %q) mount = %q, want %q", tc.target, tc.ownMount, mount, tc.wantMnt)
			}
		})
	}
}

func writeMapping(t *testing.T, b *Backend, storage logical.Storage, view string, data map[string]interface{}) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.UpdateOperation,
		Path:       "admin/map/" + view,
		Storage:    storage,
		Data:       data,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func readMapping(t *testing.T, b *Backend, storage logical.Storage, view string) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.ReadOperation,
		Path:       "admin/map/" + view,
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func deleteMapping(t *testing.T, b *Backend, storage logical.Storage, view string) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.DeleteOperation,
		Path:       "admin/map/" + view,
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func listMappings(t *testing.T, b *Backend, storage logical.Storage, prefix string) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.ListOperation,
		Path:       "admin/map/" + prefix,
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func TestPathMap_WriteReadRoundTrip(t *testing.T) {
	b, storage := newTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db_password"
	resp, err := writeMapping(t, b, storage, view, map[string]interface{}{
		"target": "kv/data/infra/postgres#password",
	})
	if err != nil || (resp != nil && resp.IsError()) {
		t.Fatalf("write mapping: resp=%+v err=%v", resp, err)
	}

	resp, err = readMapping(t, b, storage, view)
	if err != nil {
		t.Fatalf("read mapping: %v", err)
	}
	if resp == nil {
		t.Fatal("read mapping: nil response")
	}
	if got := resp.Data["target"]; got != "kv/data/infra/postgres#password" {
		t.Errorf("target = %v, want kv/data/infra/postgres#password", got)
	}
	if got := resp.Data["adapter"]; got != "" {
		t.Errorf("adapter = %v, want empty", got)
	}
	if got := resp.Data["quarantined"]; got != false {
		t.Errorf("quarantined = %v, want false", got)
	}
}

func TestPathMap_ReadUnmapped(t *testing.T) {
	b, storage := newTestBackend(t)

	resp, err := readMapping(t, b, storage, "no/such/view")
	if err != nil {
		t.Fatalf("read mapping: unexpected error: %v", err)
	}
	if resp != nil {
		t.Fatalf("read mapping: resp = %+v, want nil (unmapped -> implicit 404)", resp)
	}
}

func TestPathMap_Delete(t *testing.T) {
	b, storage := newTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db_password"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/infra/postgres"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	if _, err := deleteMapping(t, b, storage, view); err != nil {
		t.Fatalf("delete mapping: %v", err)
	}

	resp, err := readMapping(t, b, storage, view)
	if err != nil {
		t.Fatalf("read mapping after delete: %v", err)
	}
	if resp != nil {
		t.Fatalf("read mapping after delete: resp = %+v, want nil", resp)
	}
}

func TestPathMap_DeleteNonexistentIsIdempotent(t *testing.T) {
	b, storage := newTestBackend(t)

	if _, err := deleteMapping(t, b, storage, "never/written"); err != nil {
		t.Fatalf("delete nonexistent mapping: %v", err)
	}
}

func TestPathMap_List(t *testing.T) {
	b, storage := newTestBackend(t)

	views := []string{
		"example.com/iac/prod/svc.foo/db_password",
		"example.com/iac/prod/svc.foo/api_key",
		"example.com/iac/prod/svc.bar/db_password",
	}
	for _, v := range views {
		if _, err := writeMapping(t, b, storage, v, map[string]interface{}{"target": "kv/data/x"}); err != nil {
			t.Fatalf("write mapping %s: %v", v, err)
		}
	}

	resp, err := listMappings(t, b, storage, "example.com/iac/prod/")
	if err != nil {
		t.Fatalf("list mappings: %v", err)
	}
	if resp == nil {
		t.Fatal("list mappings: nil response")
	}
	keys, ok := resp.Data["keys"].([]string)
	if !ok {
		t.Fatalf("list mappings: keys = %v, want []string", resp.Data["keys"])
	}

	want := map[string]bool{"svc.foo/": true, "svc.bar/": true}
	if len(keys) != len(want) {
		t.Fatalf("list mappings: keys = %v, want direct children %v", keys, want)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("list mappings: unexpected key %q", k)
		}
	}
}

func TestPathMap_ListEmptyPrefix(t *testing.T) {
	b, storage := newTestBackend(t)

	if _, err := writeMapping(t, b, storage, "a/b", map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	resp, err := listMappings(t, b, storage, "")
	if err != nil {
		t.Fatalf("list mappings: %v", err)
	}
	if resp == nil {
		t.Fatal("list mappings: nil response")
	}
	keys, _ := resp.Data["keys"].([]string)
	if len(keys) != 1 || keys[0] != "a/" {
		t.Errorf("list mappings root = %v, want [a/]", keys)
	}
}

func TestPathMap_WriteMissingView(t *testing.T) {
	b, storage := newTestBackend(t)

	resp, err := writeMapping(t, b, storage, "", map[string]interface{}{"target": "kv/data/x"})
	if err != nil {
		t.Fatalf("write mapping: unexpected error: %v", err)
	}
	if resp == nil || !resp.IsError() {
		t.Fatalf("write mapping with empty view: resp = %+v, want error response", resp)
	}
}

// TestPathMap_WriteRejectClasses drives pathMapWrite end-to-end (through
// HandleRequest, MountPoint set to "vs/") across every reject class the
// admin surface must enforce: grammar violations (canonicalizeTarget),
// the fixed reject list, own-mount recursion, and invalid adapter
// overrides.
func TestPathMap_WriteRejectClasses(t *testing.T) {
	cases := []struct {
		name string
		data map[string]interface{}
	}{
		{name: "leading slash", data: map[string]interface{}{"target": "/kv/data/x"}},
		{name: "double slash", data: map[string]interface{}{"target": "kv//data/x"}},
		{name: "dot segment", data: map[string]interface{}{"target": "kv/./data/x"}},
		{name: "dotdot segment", data: map[string]interface{}{"target": "kv/../data/x"}},
		{name: "url-encoded", data: map[string]interface{}{"target": "kv/data/x%2Fy"}},
		{name: "query string", data: map[string]interface{}{"target": "kv/data/x?v=1"}},
		{name: "empty field", data: map[string]interface{}{"target": "kv/data/x#"}},
		{name: "own mount", data: map[string]interface{}{"target": "vs/data/x"}},
		{name: "auth mount", data: map[string]interface{}{"target": "auth/approle/login"}},
		{name: "sys mount", data: map[string]interface{}{"target": "sys/leases/revoke"}},
		{name: "identity mount", data: map[string]interface{}{"target": "identity/entity/id/x"}},
		{name: "cubbyhole mount", data: map[string]interface{}{"target": "cubbyhole/x"}},
		{name: "invalid adapter", data: map[string]interface{}{"target": "kv/data/x", "adapter": "bogus"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, storage := newTestBackend(t)
			resp, err := writeMapping(t, b, storage, "some/view", tc.data)
			if err != nil {
				t.Fatalf("write mapping: unexpected error: %v", err)
			}
			if resp == nil || !resp.IsError() {
				t.Fatalf("write mapping %s: resp = %+v, want error response", tc.name, resp)
			}

			got, rerr := readMapping(t, b, storage, "some/view")
			if rerr != nil {
				t.Fatalf("read mapping: %v", rerr)
			}
			if got != nil {
				t.Fatalf("rejected mapping must not be persisted, got %+v", got.Data)
			}
		})
	}
}

// TestPathMap_WriteAcceptClasses covers the accept side of the same
// end-to-end surface: canonical kv2 and raw targets, with and without
// a #field, and explicit adapter overrides.
func TestPathMap_WriteAcceptClasses(t *testing.T) {
	cases := []struct {
		name string
		data map[string]interface{}
	}{
		{name: "kv2 with field", data: map[string]interface{}{"target": "kv/data/infra/postgres#password"}},
		{name: "raw no field", data: map[string]interface{}{"target": "op/field/Infra/db.example.com/foo-db"}},
		{name: "whole map", data: map[string]interface{}{"target": "kv/data/infra/postgres"}},
		{name: "explicit kv2 adapter", data: map[string]interface{}{"target": "kv/data/x", "adapter": "kv2"}},
		{name: "explicit raw adapter", data: map[string]interface{}{"target": "kv/data/x", "adapter": "raw"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, storage := newTestBackend(t)
			resp, err := writeMapping(t, b, storage, "some/view", tc.data)
			if err != nil || (resp != nil && resp.IsError()) {
				t.Fatalf("write mapping %s: resp=%+v err=%v", tc.name, resp, err)
			}

			got, rerr := readMapping(t, b, storage, "some/view")
			if rerr != nil || got == nil {
				t.Fatalf("read mapping after accept: resp=%+v err=%v", got, rerr)
			}
			if got.Data["target"] != tc.data["target"] {
				t.Errorf("target = %v, want %v", got.Data["target"], tc.data["target"])
			}
		})
	}
}

func TestPathMap_TargetMountAllowlist(t *testing.T) {
	b, storage := newTestBackend(t)

	cfgData := validConfigData()
	cfgData["target_mount_allowlist"] = "kv"
	if _, err := writeConfig(t, b, storage, cfgData); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Allowed mount: accepted.
	resp, err := writeMapping(t, b, storage, "view/a", map[string]interface{}{"target": "kv/data/x"})
	if err != nil || (resp != nil && resp.IsError()) {
		t.Fatalf("write mapping to allowlisted mount: resp=%+v err=%v", resp, err)
	}

	// Disallowed mount: rejected, not persisted.
	resp, err = writeMapping(t, b, storage, "view/b", map[string]interface{}{"target": "op/field/x"})
	if err != nil {
		t.Fatalf("write mapping: unexpected error: %v", err)
	}
	if resp == nil || !resp.IsError() {
		t.Fatalf("write mapping to non-allowlisted mount: resp = %+v, want error", resp)
	}
	got, rerr := readMapping(t, b, storage, "view/b")
	if rerr != nil {
		t.Fatalf("read mapping: %v", rerr)
	}
	if got != nil {
		t.Fatalf("rejected mapping must not be persisted, got %+v", got.Data)
	}
}

func TestPathMap_WriteBeforeConfig_FixedRejectStillApplies(t *testing.T) {
	// No admin/config write at all — mapping writes must stay
	// callable, and the fixed reject list (auth/sys/identity/cubbyhole)
	// must still be enforced via Config.targetMountAllowed's nil-safe
	// path, even though b.currentConfig() is nil.
	b, storage := newTestBackend(t)

	resp, err := writeMapping(t, b, storage, "view/a", map[string]interface{}{"target": "auth/approle/login"})
	if err != nil {
		t.Fatalf("write mapping: unexpected error: %v", err)
	}
	if resp == nil || !resp.IsError() {
		t.Fatalf("write mapping targeting auth/ with no config: resp = %+v, want error", resp)
	}

	// A permitted mount succeeds with no config written at all.
	resp, err = writeMapping(t, b, storage, "view/b", map[string]interface{}{"target": "kv/data/x"})
	if err != nil || (resp != nil && resp.IsError()) {
		t.Fatalf("write mapping with no config: resp=%+v err=%v", resp, err)
	}
}

func TestPathMap_RewriteClearsQuarantine(t *testing.T) {
	b, storage := newTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db_password"
	quarantined := &MappingEntry{
		Target:            "kv/data/infra/postgres#password",
		WriteTime:         time.Now().UTC(),
		Quarantined:       true,
		QuarantineCause:   "lease-bearing target response",
		RevocationOutcome: "revoked",
	}
	entry, err := logical.StorageEntryJSON(mapStorageKey(view), quarantined)
	if err != nil {
		t.Fatalf("StorageEntryJSON: %v", err)
	}
	if err := storage.Put(context.Background(), entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resp, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/infra/postgres#password"})
	if err != nil || (resp != nil && resp.IsError()) {
		t.Fatalf("rewrite mapping: resp=%+v err=%v", resp, err)
	}

	got, rerr := readMapping(t, b, storage, view)
	if rerr != nil || got == nil {
		t.Fatalf("read mapping after rewrite: resp=%+v err=%v", got, rerr)
	}
	if got.Data["quarantined"] != false {
		t.Errorf("quarantined = %v, want false after rewrite", got.Data["quarantined"])
	}
	if got.Data["quarantine_cause"] != "" {
		t.Errorf("quarantine_cause = %v, want empty after rewrite", got.Data["quarantine_cause"])
	}
	if got.Data["revocation_outcome"] != "" {
		t.Errorf("revocation_outcome = %v, want empty after rewrite", got.Data["revocation_outcome"])
	}
}
