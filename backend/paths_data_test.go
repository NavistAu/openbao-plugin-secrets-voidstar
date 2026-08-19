package backend

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// newDereferenceTestBackend builds a *Backend with config already
// written and a fake loopback client wired in (mirroring
// newLoopbackTestBackend), plus a buffered logger so dereference-
// failure logging (spec §10) can be asserted directly.
func newDereferenceTestBackend(t *testing.T) (b *Backend, storage logical.Storage, fake *FakeLoopbackClient, logs *bytes.Buffer) {
	t.Helper()

	b = newBackend()
	logs = &bytes.Buffer{}
	conf := logical.TestBackendConfig()
	conf.StorageView = &logical.InmemStorage{}
	conf.Logger = hclog.New(&hclog.LoggerOptions{Output: logs, Level: hclog.Trace})
	if err := b.Setup(context.Background(), conf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	storage = conf.StorageView

	if _, err := writeConfig(t, b, storage, validConfigData()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	fake = NewFakeLoopbackClient()
	b.clientFactory = func(ctx context.Context, cfg *Config) (LoopbackClient, int, error) {
		return fake, 300, nil
	}
	return b, storage, fake, logs
}

func readData(t *testing.T, b *Backend, storage logical.Storage, view string) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation:  logical.ReadOperation,
		Path:       "data/" + view,
		Storage:    storage,
		MountPoint: "vs/",
	}
	return b.HandleRequest(context.Background(), req)
}

func codedStatus(err error) int {
	if ce, ok := err.(logical.HTTPCodedError); ok {
		return ce.Code()
	}
	return 0
}

func TestPathData_Unmapped404(t *testing.T) {
	b, storage, _, _ := newDereferenceTestBackend(t)

	_, err := readData(t, b, storage, "no/such/view")
	if err == nil {
		t.Fatal("read unmapped view: want error")
	}
	if got := codedStatus(err); got != 404 {
		t.Fatalf("read unmapped view: status = %d, want 404", got)
	}
}

func TestPathData_QuarantinedFastFail(t *testing.T) {
	b, storage, fake, logs := newDereferenceTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db_password"
	entry := &MappingEntry{Target: "kv/data/infra/postgres#password", Quarantined: true, QuarantineCause: "lease-bearing target response"}
	se, err := logical.StorageEntryJSON(mapStorageKey(view), entry)
	if err != nil {
		t.Fatalf("StorageEntryJSON: %v", err)
	}
	if err := storage.Put(context.Background(), se); err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("read quarantined view: want error")
	}
	if got := codedStatus(rerr); got != 502 {
		t.Fatalf("read quarantined view: status = %d, want 502", got)
	}
	if fake.ReadCalls != 0 {
		t.Fatalf("quarantined fast-fail must not call the loopback: ReadCalls = %d, want 0", fake.ReadCalls)
	}
	if !strings.Contains(logs.String(), view) || !strings.Contains(logs.String(), "kv") {
		t.Errorf("dereference failure log missing view/mount: %s", logs.String())
	}
}

func TestPathData_KV2WholeMap(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/infra/postgres"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/infra/postgres"] = FakeReadResponse{
		Data: map[string]interface{}{
			"data":     map[string]interface{}{"username": "bob", "password": "hunter2"},
			"metadata": map[string]interface{}{"version": 3},
		},
	}

	resp, err := readData(t, b, storage, view)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp == nil {
		t.Fatal("read: nil response")
	}
	data, ok := resp.Data["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data = %v, want map", resp.Data["data"])
	}
	if data["username"] != "bob" || data["password"] != "hunter2" {
		t.Errorf("data = %+v, want unwrapped kv2 fields", data)
	}

	meta, ok := resp.Data["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata = %v, want map", resp.Data["metadata"])
	}
	if meta["version"] != 1 {
		t.Errorf("synthetic metadata version = %v, want 1 (not kv2's own version)", meta["version"])
	}
	if meta["deletion_time"] != "" {
		t.Errorf("deletion_time = %v, want empty", meta["deletion_time"])
	}
	if meta["destroyed"] != false {
		t.Errorf("destroyed = %v, want false", meta["destroyed"])
	}
	if cm, ok := meta["custom_metadata"].(map[string]interface{}); !ok || len(cm) != 0 {
		t.Errorf("custom_metadata = %v, want {} (expose_targets false)", meta["custom_metadata"])
	}
}

func TestPathData_KV2FieldSelect(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	view := "example.com/iac/prod/svc.foo/db_password"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/infra/postgres#password"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/infra/postgres"] = FakeReadResponse{
		Data: map[string]interface{}{
			"data": map[string]interface{}{"username": "bob", "password": "hunter2"},
		},
	}

	resp, err := readData(t, b, storage, view)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data, ok := resp.Data["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data = %v, want map", resp.Data["data"])
	}
	if len(data) != 1 || data["value"] != "hunter2" {
		t.Errorf("data = %+v, want {value: hunter2}", data)
	}
}

func TestPathData_RawAdapterAsIs(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	view := "example.com/iac/prod/svc.foo/identity"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "op/field/Infra/db.example.com/foo-db"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["op/field/Infra/db.example.com/foo-db"] = FakeReadResponse{
		Data: map[string]interface{}{"value": "hunter2"},
	}

	resp, err := readData(t, b, storage, view)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data, ok := resp.Data["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data = %v, want map", resp.Data["data"])
	}
	if data["value"] != "hunter2" {
		t.Errorf("data = %+v, want raw passthrough", data)
	}
}

func TestPathData_ExplicitAdapterOverride(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	// A kv2-shaped path (mount/data/...) explicitly overridden to raw:
	// the response must NOT be unwrapped.
	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x", "adapter": "raw"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{
		Data: map[string]interface{}{"data": map[string]interface{}{"a": "b"}},
	}

	resp, err := readData(t, b, storage, view)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data, ok := resp.Data["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data = %v, want map", resp.Data["data"])
	}
	if _, hasNested := data["data"].(map[string]interface{}); !hasNested {
		t.Errorf("data = %+v, want raw (unwrapped) passthrough despite kv2-shaped path", data)
	}
}

func TestPathData_MissingFieldIs502(t *testing.T) {
	b, storage, fake, logs := newDereferenceTestBackend(t)

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x#nope"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{Data: map[string]interface{}{"data": map[string]interface{}{"other": "v"}}}

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("read with missing field: want error")
	}
	if got := codedStatus(rerr); got != 502 {
		t.Fatalf("read with missing field: status = %d, want 502", got)
	}
	if !strings.Contains(logs.String(), "target field not found") {
		t.Errorf("log missing missing-field cause: %s", logs.String())
	}
}

func TestPathData_KV2ShapeMismatchIs502(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{Data: map[string]interface{}{"not_data": "surprise"}}

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("read with kv2 shape mismatch: want error")
	}
	if got := codedStatus(rerr); got != 502 {
		t.Fatalf("status = %d, want 502", got)
	}
}

func TestPathData_LoopbackReadErrorIs502(t *testing.T) {
	b, storage, fake, logs := newDereferenceTestBackend(t)

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{Err: newStatusError(500, "backend unreachable")}

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("read with loopback error: want error")
	}
	if got := codedStatus(rerr); got != 502 {
		t.Fatalf("status = %d, want 502", got)
	}
	if !strings.Contains(logs.String(), "upstream dereference failed") {
		t.Errorf("log missing upstream-read cause: %s", logs.String())
	}
}

func TestPathData_LoopbackUnavailableIs502(t *testing.T) {
	b, storage := newTestBackend(t)
	// No config written at all: ensureLoopbackClient fails with
	// errLoopbackNotConfigured, which must surface as a 502 dereference
	// failure, not an internal error.
	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	_, rerr := readData(t, b, storage, view)
	if rerr == nil {
		t.Fatal("read with no loopback client: want error")
	}
	if got := codedStatus(rerr); got != 502 {
		t.Fatalf("status = %d, want 502", got)
	}
}

func TestPathData_ExposeTargetsRedaction(t *testing.T) {
	for _, expose := range []bool{false, true} {
		t.Run(map[bool]string{true: "exposed", false: "redacted"}[expose], func(t *testing.T) {
			b, storage, _, _ := newDereferenceTestBackend(t)

			cfgData := validConfigData()
			cfgData["expose_targets"] = expose
			if _, err := writeConfig(t, b, storage, cfgData); err != nil {
				t.Fatalf("write config: %v", err)
			}

			view := "no/such/view/quarantined"
			entry := &MappingEntry{Target: "kv/data/infra/postgres", Quarantined: true}
			se, err := logical.StorageEntryJSON(mapStorageKey(view), entry)
			if err != nil {
				t.Fatalf("StorageEntryJSON: %v", err)
			}
			if err := storage.Put(context.Background(), se); err != nil {
				t.Fatalf("Put: %v", err)
			}

			_, rerr := readData(t, b, storage, view)
			if rerr == nil {
				t.Fatal("want error")
			}
			hasMount := strings.Contains(rerr.Error(), "\"kv\"")
			if expose && !hasMount {
				t.Errorf("expose_targets=true: error %q must name the target mount", rerr.Error())
			}
			if !expose && hasMount {
				t.Errorf("expose_targets=false: error %q must redact the target mount", rerr.Error())
			}
			if !strings.Contains(rerr.Error(), view) {
				t.Errorf("error %q must always name the view", rerr.Error())
			}
		})
	}
}

func TestPathData_CustomMetadataGatedOnExposeTargets(t *testing.T) {
	b, storage, fake, _ := newDereferenceTestBackend(t)

	cfgData := validConfigData()
	cfgData["expose_targets"] = true
	if _, err := writeConfig(t, b, storage, cfgData); err != nil {
		t.Fatalf("write config: %v", err)
	}

	view := "some/view"
	if _, err := writeMapping(t, b, storage, view, map[string]interface{}{"target": "kv/data/x"}); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	fake.Reads["kv/data/x"] = FakeReadResponse{Data: map[string]interface{}{"data": map[string]interface{}{"a": "b"}}}

	resp, err := readData(t, b, storage, view)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	meta := resp.Data["metadata"].(map[string]interface{})
	cm, ok := meta["custom_metadata"].(map[string]interface{})
	if !ok || cm["voidstar_target"] != "kv/data/x" {
		t.Errorf("custom_metadata = %v, want {voidstar_target: kv/data/x}", meta["custom_metadata"])
	}
}
