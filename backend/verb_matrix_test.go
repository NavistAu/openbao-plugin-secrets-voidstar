package backend

import (
	"context"
	"strings"
	"testing"

	"github.com/openbao/openbao/sdk/v2/logical"
)

func doRequest(b *Backend, storage logical.Storage, op logical.Operation, path string) (*logical.Response, error) {
	req := &logical.Request{
		Operation:  op,
		Path:       path,
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

// TestVerbMatrix_405 is the table-driven sweep over the 405
// matrix: write verbs on data/* and metadata/*, and every verb
// (including read/list) on the KV-reserved paths. Each must be an
// explicitly-registered 405 naming voidstar, not the SDK's generic
// unsupported-operation text.
func TestVerbMatrix_405(t *testing.T) {
	writeVerbs := []logical.Operation{logical.CreateOperation, logical.UpdateOperation, logical.PatchOperation, logical.DeleteOperation}
	anyVerb := []logical.Operation{logical.CreateOperation, logical.ReadOperation, logical.UpdateOperation, logical.PatchOperation, logical.DeleteOperation, logical.ListOperation}

	type tc struct {
		path string
		op   logical.Operation
	}
	var cases []tc

	for _, op := range writeVerbs {
		cases = append(cases, tc{"data/some/view", op})
		cases = append(cases, tc{"metadata/some/view", op})
	}
	for _, path := range []string{"config", "delete/some/view", "undelete/some/view", "destroy/some/view", "detailed-metadata/some/view"} {
		for _, op := range anyVerb {
			cases = append(cases, tc{path, op})
		}
	}

	for _, c := range cases {
		t.Run(string(c.op)+" "+c.path, func(t *testing.T) {
			b, storage := newTestBackend(t)
			_, err := doRequest(b, storage, c.op, c.path)
			if err == nil {
				t.Fatalf("%s %s: want error", c.op, c.path)
			}
			if got := codedStatus(err); got != 405 {
				t.Fatalf("%s %s: status = %d, want 405", c.op, c.path, got)
			}
			if !strings.Contains(err.Error(), "voidstar") {
				t.Errorf("%s %s: error %q must name voidstar", c.op, c.path, err.Error())
			}
		})
	}
}

// TestVerbMatrix_SupportedVerbsStillWork proves the 405 registrations
// didn't accidentally shadow the verbs that must stay supported: GET
// on data/* and metadata/*, and LIST on metadata/*.
func TestVerbMatrix_SupportedVerbsStillWork(t *testing.T) {
	cases := []struct {
		name string
		path string
		op   logical.Operation
	}{
		{"data read", "data/some/view", logical.ReadOperation},
		{"metadata read", "metadata/some/view", logical.ReadOperation},
		{"metadata list", "metadata/some/", logical.ListOperation},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, storage := newTestBackend(t)
			if _, err := writeMapping(t, b, storage, "some/view", map[string]interface{}{"target": "kv/data/x"}); err != nil {
				t.Fatalf("write mapping: %v", err)
			}
			_, err := doRequest(b, storage, c.op, c.path)
			if err != nil {
				if got := codedStatus(err); got == 405 {
					t.Fatalf("%s: got 405, want a supported verb (err=%v)", c.name, err)
				}
			}
		})
	}
}

// TestVerbMatrix_404CatchAll sweeps the "anything else on the KV-
// emulation surface" catch-all across verbs and unrelated
// paths.
func TestVerbMatrix_404CatchAll(t *testing.T) {
	cases := []struct {
		name string
		path string
		op   logical.Operation
	}{
		{"bare nonsense", "nonsense", logical.ReadOperation},
		{"nested nonsense", "foo/bar/baz", logical.ReadOperation},
		{"nonsense list", "nonsense", logical.ListOperation},
		{"nonsense create", "nonsense", logical.CreateOperation},
		{"root", "", logical.ReadOperation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, storage := newTestBackend(t)
			_, err := doRequest(b, storage, c.op, c.path)
			if err == nil {
				t.Fatalf("%s %s: want error", c.op, c.path)
			}
			if got := codedStatus(err); got != 404 {
				t.Fatalf("%s %s: status = %d, want 404", c.op, c.path, got)
			}
		})
	}
}

// TestVerbMatrix_AdminSurfaceNotShadowed proves vs/admin/* is routed
// to its own handlers, not swallowed by pathNotFound's ".*" catch-all
// (the catch-all "excludes the vs/admin/* family"):
// admin/config with no config written returns pathConfigRead's own
// nil,nil "nothing written yet" — not the catch-all's explicit
// CodedError(404, ...).
func TestVerbMatrix_AdminSurfaceNotShadowed(t *testing.T) {
	b, storage := newTestBackend(t)

	resp, err := doRequest(b, storage, logical.ReadOperation, "admin/config")
	if err != nil {
		t.Fatalf("admin/config read: unexpected error: %v", err)
	}
	if resp != nil {
		t.Fatalf("admin/config read with no config written: resp = %+v, want nil", resp)
	}

	resp, err = doRequest(b, storage, logical.ReadOperation, "admin/map/no/such/view")
	if err != nil {
		t.Fatalf("admin/map read: unexpected error: %v", err)
	}
	if resp != nil {
		t.Fatalf("admin/map read for unmapped view: resp = %+v, want nil", resp)
	}
}
