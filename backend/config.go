package backend

import (
	"context"
	"errors"

	"github.com/openbao/openbao/sdk/v2/logical"
)

// configStorageKey is the single storage entry backing `vs/admin/config`.
const configStorageKey = "config"

// defaultApproleMount is the default for approle_mount.
const defaultApproleMount = "approle"

// fixedRejectMounts are unconditionally rejected as mapping targets
// regardless of target_mount_allowlist: "auth/*", "sys/*",
// "identity/*", "cubbyhole/*". (The engine's own mount is also
// rejected, but that check needs the request's mount
// point, not just config, and lives with Task 3's canonicalization.)
var fixedRejectMounts = []string{"auth", "sys", "identity", "cubbyhole"}

// Config is the persisted `vs/admin/config` entry. A write
// always replaces it wholesale — role_id/secret_id/api_addr are
// required on every write, so there is no partial-update/merge case.
type Config struct {
	ApproleMount         string   `json:"approle_mount"`
	RoleID               string   `json:"role_id"`
	SecretID             string   `json:"secret_id"`
	APIAddr              string   `json:"api_addr"`
	ExposeTargets        bool     `json:"expose_targets"`
	TargetMountAllowlist []string `json:"target_mount_allowlist,omitempty"`
}

// validate applies the required-field rules.
func (c *Config) validate() error {
	if c.RoleID == "" {
		return errors.New("role_id is required")
	}
	if c.SecretID == "" {
		return errors.New("secret_id is required")
	}
	if c.APIAddr == "" {
		return errors.New("api_addr is required")
	}
	return nil
}

// targetMountAllowed reports whether mount may be used as a mapping
// target's mount: the fixed reject list is rejected
// unconditionally; otherwise an absent/empty target_mount_allowlist
// permits every other mount, and a non-empty allowlist narrows to
// exactly its entries. A nil receiver (no config written yet) behaves
// like an unconfigured allowlist: fixed rejects still apply, every
// other mount is permitted — Task 3's mapping writes must stay
// callable before `vs/admin/config` has ever been written.
func (c *Config) targetMountAllowed(mount string) bool {
	for _, r := range fixedRejectMounts {
		if mount == r {
			return false
		}
	}
	if c == nil || len(c.TargetMountAllowlist) == 0 {
		return true
	}
	for _, m := range c.TargetMountAllowlist {
		if m == mount {
			return true
		}
	}
	return false
}

// getConfigFromStorage reads `config` fresh from storage — the source
// of truth for reads, independent of whatever the backend has cached
// in memory. Takes a bare logical.Storage (rather than a
// *logical.Request) so it's callable from contexts that don't have a
// full Request, e.g. the Initialize hook.
func getConfigFromStorage(ctx context.Context, storage logical.Storage) (*Config, error) {
	entry, err := storage.Get(ctx, configStorageKey)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	var cfg Config
	if err := entry.DecodeJSON(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyConfig validates cfg, persists it, and caches it in-memory.
// This is a pure storage operation — no client construction, no
// network. Nothing is touched — storage included — if
// validation fails.
func (b *Backend) applyConfig(ctx context.Context, req *logical.Request, cfg *Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	entry, err := logical.StorageEntryJSON(configStorageKey, cfg)
	if err != nil {
		return err
	}
	if err := req.Storage.Put(ctx, entry); err != nil {
		return err
	}

	b.mu.Lock()
	b.config = cfg
	b.mu.Unlock()

	return nil
}

// configResponseData builds the `vs/admin/config` read response.
// secret_id is intentionally absent — concealed on read.
func configResponseData(cfg *Config) map[string]interface{} {
	return map[string]interface{}{
		"approle_mount":          cfg.ApproleMount,
		"role_id":                cfg.RoleID,
		"api_addr":               cfg.APIAddr,
		"expose_targets":         cfg.ExposeTargets,
		"target_mount_allowlist": cfg.TargetMountAllowlist,
	}
}
