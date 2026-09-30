package backend

import (
	"context"
	"strings"
	"testing"

	"github.com/openbao/openbao/sdk/v2/logical"
)

func levelReq(t *testing.T, b *Backend, storage logical.Storage, op logical.Operation, prefix string, data map[string]interface{}) (*logical.Response, error) {
	t.Helper()
	return b.HandleRequest(context.Background(), &logical.Request{
		Operation: op, Path: "admin/level/" + prefix, Storage: storage, Data: data, MountPoint: "vs/",
	})
}

func TestPathLevel_CRUD(t *testing.T) {
	b, storage := newTestBackend(t)
	resp, err := levelReq(t, b, storage, logical.UpdateOperation, "prod", map[string]interface{}{"custom_metadata": cm("branches", "main")})
	if err != nil || (resp != nil && resp.IsError()) {
		t.Fatalf("write: resp=%+v err=%v", resp, err)
	}
	r, err := levelReq(t, b, storage, logical.ReadOperation, "prod", nil)
	if err != nil || r == nil {
		t.Fatalf("read: %+v %v", r, err)
	}
	if got := r.Data["custom_metadata"].(map[string]string); got["branches"] != "main" {
		t.Fatalf("custom_metadata = %v", got)
	}
	if r.Data["prefix"] != "prod/" {
		t.Fatalf("prefix = %v, want prod/", r.Data["prefix"])
	}
	l, err := levelReq(t, b, storage, logical.ListOperation, "", nil)
	if err != nil || len(l.Data["keys"].([]string)) != 1 {
		t.Fatalf("list: %+v %v", l, err)
	}
	if _, err := levelReq(t, b, storage, logical.DeleteOperation, "prod/", nil); err != nil {
		t.Fatal(err)
	}
	if r, _ := levelReq(t, b, storage, logical.ReadOperation, "prod", nil); r != nil {
		t.Fatalf("read after delete = %+v, want nil", r)
	}
}

func TestPathLevel_Normalisation(t *testing.T) {
	b, storage := newTestBackend(t)
	levelReq(t, b, storage, logical.UpdateOperation, "a/b", map[string]interface{}{"custom_metadata": cm("k", "1")})
	levelReq(t, b, storage, logical.UpdateOperation, "a/b/", map[string]interface{}{"custom_metadata": cm("k", "2")})
	r, _ := levelReq(t, b, storage, logical.ReadOperation, "a/b//", nil)
	if r == nil || r.Data["custom_metadata"].(map[string]string)["k"] != "2" {
		t.Fatalf("a/b and a/b/ must be one level: %+v", r)
	}
}

func TestPathLevel_Rejects(t *testing.T) {
	b, storage := newTestBackend(t)
	for name, d := range map[string]map[string]interface{}{
		"unknown field": {"custom_metadata": cm("a", "b"), "nope": 1},
		"reserved key":  {"custom_metadata": cm("voidstar_target", "b")},
		"value 513":     {"custom_metadata": cm("a", strings.Repeat("v", 513))},
	} {
		resp, _ := levelReq(t, b, storage, logical.UpdateOperation, "p", d)
		if resp == nil || !resp.IsError() {
			t.Errorf("%s: resp = %+v, want error", name, resp)
		}
	}
	if resp, _ := levelReq(t, b, storage, logical.UpdateOperation, "", map[string]interface{}{"custom_metadata": cm("a", "b")}); resp == nil || !resp.IsError() {
		t.Errorf("empty prefix must be rejected: %+v", resp)
	}
}
