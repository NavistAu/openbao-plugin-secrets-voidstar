package backend

import (
	"context"
	"testing"

	"github.com/openbao/openbao/sdk/v2/logical"
)

func writeConfig(t *testing.T, b *Backend, storage logical.Storage, data map[string]interface{}) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation: logical.UpdateOperation,
		Path:      "admin/config",
		Storage:   storage,
		Data:      data,
	}
	return b.HandleRequest(context.Background(), req)
}

func readConfig(t *testing.T, b *Backend, storage logical.Storage) (*logical.Response, error) {
	t.Helper()
	req := &logical.Request{
		Operation: logical.ReadOperation,
		Path:      "admin/config",
		Storage:   storage,
	}
	return b.HandleRequest(context.Background(), req)
}

func validConfigData() map[string]interface{} {
	return map[string]interface{}{
		"role_id":   "role-1",
		"secret_id": "secret-1",
		"api_addr":  "https://127.0.0.1:8200",
	}
}

func TestPathConfig_WriteReadRedaction(t *testing.T) {
	b, storage := newTestBackend(t)

	data := validConfigData()
	resp, err := writeConfig(t, b, storage, data)
	if err != nil || (resp != nil && resp.IsError()) {
		t.Fatalf("write config: resp=%+v err=%v", resp, err)
	}

	resp, err = readConfig(t, b, storage)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if resp == nil {
		t.Fatalf("read config: nil response")
	}
	if _, present := resp.Data["secret_id"]; present {
		t.Errorf("secret_id leaked in read response: %+v", resp.Data)
	}
	if got := resp.Data["role_id"]; got != "role-1" {
		t.Errorf("role_id = %v, want role-1", got)
	}
	if got := resp.Data["api_addr"]; got != "https://127.0.0.1:8200" {
		t.Errorf("api_addr = %v, want https://127.0.0.1:8200", got)
	}
}

func TestPathConfig_ApproleMountDefault(t *testing.T) {
	b, storage := newTestBackend(t)

	if _, err := writeConfig(t, b, storage, validConfigData()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	resp, err := readConfig(t, b, storage)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if got := resp.Data["approle_mount"]; got != defaultApproleMount {
		t.Errorf("approle_mount default = %v, want %v", got, defaultApproleMount)
	}
}

func TestPathConfig_ExposeTargetsDefaultFalse(t *testing.T) {
	b, storage := newTestBackend(t)

	if _, err := writeConfig(t, b, storage, validConfigData()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	resp, err := readConfig(t, b, storage)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if got := resp.Data["expose_targets"]; got != false {
		t.Errorf("expose_targets default = %v, want false", got)
	}
}

func TestPathConfig_RequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]interface{})
		wantErr string
	}{
		{
			name: "missing role_id",
			mutate: func(d map[string]interface{}) {
				delete(d, "role_id")
			},
			wantErr: "role_id is required",
		},
		{
			name: "missing secret_id",
			mutate: func(d map[string]interface{}) {
				delete(d, "secret_id")
			},
			wantErr: "secret_id is required",
		},
		{
			name: "missing api_addr",
			mutate: func(d map[string]interface{}) {
				delete(d, "api_addr")
			},
			wantErr: "api_addr is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, storage := newTestBackend(t)
			data := validConfigData()
			tc.mutate(data)

			resp, err := writeConfig(t, b, storage, data)
			if err != nil {
				t.Fatalf("write config: unexpected error: %v", err)
			}
			if resp == nil || !resp.IsError() {
				t.Fatalf("write config %s: resp=%+v, want an error response", tc.name, resp)
			}
			if resp.Data["error"] != tc.wantErr {
				t.Errorf("error = %v, want %v", resp.Data["error"], tc.wantErr)
			}

			got, rerr := readConfig(t, b, storage)
			if rerr != nil {
				t.Fatalf("read config: %v", rerr)
			}
			if got != nil {
				t.Fatalf("rejected config must not be persisted, got %+v", got.Data)
			}
		})
	}
}

func TestPathConfig_TargetMountAllowlist_AbsentPermitsAllButFixedReject(t *testing.T) {
	b, storage := newTestBackend(t)

	if _, err := writeConfig(t, b, storage, validConfigData()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := b.currentConfig()
	if cfg == nil {
		t.Fatal("config not cached after write")
	}

	for _, mount := range []string{"kv", "op", "anything"} {
		if !cfg.targetMountAllowed(mount) {
			t.Errorf("targetMountAllowed(%q) = false, want true (absent allowlist)", mount)
		}
	}
	for _, mount := range fixedRejectMounts {
		if cfg.targetMountAllowed(mount) {
			t.Errorf("targetMountAllowed(%q) = true, want false (fixed reject list)", mount)
		}
	}
}

func TestPathConfig_TargetMountAllowlist_PresentNarrows(t *testing.T) {
	b, storage := newTestBackend(t)

	data := validConfigData()
	data["target_mount_allowlist"] = "kv,op"
	if _, err := writeConfig(t, b, storage, data); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := b.currentConfig()
	if cfg == nil {
		t.Fatal("config not cached after write")
	}

	for _, mount := range []string{"kv", "op"} {
		if !cfg.targetMountAllowed(mount) {
			t.Errorf("targetMountAllowed(%q) = false, want true (in allowlist)", mount)
		}
	}
	if cfg.targetMountAllowed("other") {
		t.Errorf("targetMountAllowed(\"other\") = true, want false (not in allowlist)")
	}
	// The fixed reject list is unconditional, even if an operator
	// tries to explicitly allowlist one of them.
	for _, mount := range fixedRejectMounts {
		if cfg.targetMountAllowed(mount) {
			t.Errorf("targetMountAllowed(%q) = true, want false (fixed reject list overrides allowlist)", mount)
		}
	}
}

func TestPathConfig_InitializeLoadsPersistedConfig(t *testing.T) {
	b, storage := newTestBackend(t)

	if _, err := writeConfig(t, b, storage, validConfigData()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Simulate a plugin restart: a fresh backend, same storage.
	b2 := newBackend()
	conf := logical.TestBackendConfig()
	conf.StorageView = storage
	if err := b2.Setup(context.Background(), conf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := b2.initialize(context.Background(), &logical.InitializationRequest{Storage: storage}); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	cfg := b2.currentConfig()
	if cfg == nil {
		t.Fatal("initialize did not load persisted config")
	}
	if cfg.RoleID != "role-1" {
		t.Errorf("loaded config RoleID = %v, want role-1", cfg.RoleID)
	}
}

func TestPathConfig_InitializeNoPersistedConfig(t *testing.T) {
	b, storage := newTestBackend(t)

	if err := b.initialize(context.Background(), &logical.InitializationRequest{Storage: storage}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if cfg := b.currentConfig(); cfg != nil {
		t.Errorf("currentConfig = %+v, want nil", cfg)
	}
}
