package backend

import (
	"context"
	"testing"

	"github.com/openbao/openbao/sdk/v2/logical"
)

func readMetadata(t *testing.T, b *Backend, storage logical.Storage, view string) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.ReadOperation,
		Path:       "metadata/" + view,
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func listMetadata(t *testing.T, b *Backend, storage logical.Storage, prefix string) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.ListOperation,
		Path:       "metadata/" + prefix,
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func TestPathMetadata_ReadFullDocument(t *testing.T) {
	b, storage := newTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db_password"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/infra/postgres#password"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	resp, err := readMetadata(t, b, storage, view)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if resp == nil {
		t.Fatal("read metadata: nil response")
	}

	if resp.Data["current_version"] != 1 {
		t.Errorf("current_version = %v, want 1", resp.Data["current_version"])
	}
	if resp.Data["oldest_version"] != 1 {
		t.Errorf("oldest_version = %v, want 1", resp.Data["oldest_version"])
	}
	if resp.Data["max_versions"] != 0 {
		t.Errorf("max_versions = %v, want 0", resp.Data["max_versions"])
	}
	if resp.Data["created_time"] != resp.Data["updated_time"] {
		t.Errorf("created_time %v != updated_time %v, want equal", resp.Data["created_time"], resp.Data["updated_time"])
	}

	versions, ok := resp.Data["versions"].(map[string]interface{})
	if !ok || len(versions) != 1 {
		t.Fatalf("versions = %v, want exactly key \"1\"", resp.Data["versions"])
	}
	v1, ok := versions["1"].(map[string]interface{})
	if !ok {
		t.Fatalf("versions[\"1\"] = %v, want map", versions["1"])
	}
	if v1["deletion_time"] != "" {
		t.Errorf("versions[1].deletion_time = %v, want empty", v1["deletion_time"])
	}
	if v1["destroyed"] != false {
		t.Errorf("versions[1].destroyed = %v, want false", v1["destroyed"])
	}

	if cm, ok := resp.Data["custom_metadata"].(map[string]interface{}); !ok || len(cm) != 0 {
		t.Errorf("custom_metadata = %v, want {} (expose_targets false)", resp.Data["custom_metadata"])
	}
}

func TestPathMetadata_ReadUnmapped404(t *testing.T) {
	b, storage := newTestBackend(t)

	_, err := readMetadata(t, b, storage, "no/such/view")
	if err == nil {
		t.Fatal("read unmapped metadata: want error")
	}
	if got := codedStatus(err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

func TestPathMetadata_CustomMetadataGated(t *testing.T) {
	b, storage := newTestBackend(t)

	cfgData := validConfigData()
	cfgData["expose_targets"] = true
	if _, err := writeConfig(t, b, storage, cfgData); err != nil {
		t.Fatalf("write config: %v", err)
	}

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	resp, err := readMetadata(t, b, storage, view)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	cm, ok := resp.Data["custom_metadata"].(map[string]interface{})
	if !ok || cm["voidstar_target"] != "kv/data/x" {
		t.Errorf("custom_metadata = %v, want {voidstar_target: kv/data/x}", resp.Data["custom_metadata"])
	}
}

func TestPathMetadata_ListDirectChildrenSorted(t *testing.T) {
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

	resp, err := listMetadata(t, b, storage, "example.com/iac/prod/")
	if err != nil {
		t.Fatalf("list metadata: %v", err)
	}
	if resp == nil {
		t.Fatal("list metadata: nil response")
	}
	keys, ok := resp.Data["keys"].([]string)
	if !ok {
		t.Fatalf("keys = %v, want []string", resp.Data["keys"])
	}
	want := []string{"svc.bar/", "svc.foo/"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("keys[%d] = %q, want %q (sorted)", i, keys[i], want[i])
		}
	}
}

func TestPathMetadata_ListEmptyIs404(t *testing.T) {
	b, storage := newTestBackend(t)

	_, err := listMetadata(t, b, storage, "no/such/prefix")
	if err == nil {
		t.Fatal("list empty prefix: want error")
	}
	if got := codedStatus(err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

func TestPathMetadata_CustomMetadataFromMapping(t *testing.T) {
	b, storage := newTestBackend(t)
	writeMapping(t, b, storage, "v", map[string]interface{}{"target": "kv/data/x", "custom_metadata": cm("a", "b")})
	resp, err := readMetadata(t, b, storage, "v")
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Data["custom_metadata"].(map[string]interface{})
	if m["a"] != "b" || len(m) != 1 {
		t.Fatalf("custom_metadata = %v", m)
	}
}

func TestPathMetadata_ModesReadAndList(t *testing.T) {
	b, storage := newTestBackend(t)
	writeMapping(t, b, storage, "g/meta", map[string]interface{}{"mode": "metadata", "custom_metadata": cm("x", "y")})
	writeMapping(t, b, storage, "g/res", map[string]interface{}{"mode": "resolve", "target": "database/creds/ro", "custom_metadata": cm("p", "q")})
	l, err := listMetadata(t, b, storage, "g")
	if err != nil {
		t.Fatal(err)
	}
	keys := l.Data["keys"].([]string)
	if len(keys) != 2 || keys[0] != "meta" || keys[1] != "res" {
		t.Fatalf("keys = %v", keys)
	}
	for view, k := range map[string]string{"g/meta": "x", "g/res": "p"} {
		r, err := readMetadata(t, b, storage, view)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := r.Data["custom_metadata"].(map[string]interface{})[k]; !ok {
			t.Fatalf("%s: custom_metadata = %v", view, r.Data["custom_metadata"])
		}
	}
}

func TestPathMetadata_LevelRead(t *testing.T) {
	b, storage := newTestBackend(t)
	levelReq(t, b, storage, logical.UpdateOperation, "prod", map[string]interface{}{"custom_metadata": cm("branches", "main")})
	resp, err := readMetadata(t, b, storage, "prod/")
	if err != nil {
		t.Fatalf("level read: %v", err)
	}
	if m := resp.Data["custom_metadata"].(map[string]interface{}); m["branches"] != "main" {
		t.Fatalf("custom_metadata = %v", m)
	}
	if resp.Data["created_time"] == nil || resp.Data["updated_time"] == nil {
		t.Fatalf("missing times: %v", resp.Data)
	}
	if _, err := readMetadata(t, b, storage, "none/"); codedStatus(err) != 404 {
		t.Fatalf("none/: err = %v, want 404", err)
	}
}

func TestPathMetadata_LevelDoesNotAffectListAndNeedsNoViews(t *testing.T) {
	b, storage := newTestBackend(t)
	levelReq(t, b, storage, logical.UpdateOperation, "prod", map[string]interface{}{"custom_metadata": cm("a", "b")})
	if _, err := listMetadata(t, b, storage, "prod/"); codedStatus(err) != 404 {
		t.Fatalf("list of level with no views: err = %v, want 404 (unchanged)", err)
	}
	writeMapping(t, b, storage, "prod/x", map[string]interface{}{"target": "kv/data/x"})
	l, err := listMetadata(t, b, storage, "prod/")
	if err != nil || len(l.Data["keys"].([]string)) != 1 {
		t.Fatalf("list: %+v %v", l, err)
	}
	if _, err := readMetadata(t, b, storage, "prod/"); err != nil {
		t.Fatalf("level still readable: %v", err)
	}
}
