package backend

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// pathMap defines the `admin/map/<view>` family: GET/POST/
// DELETE on a specific view, and LIST on a prefix. All four operations
// share one pattern and one capture ("view") because the admin surface
// treats a view path and a list prefix as the same opaque string
// ("paths are opaque strings; hierarchy exists only through
// list") — the operation, not the pattern, decides whether it's read
// as an exact key or a storage-list prefix.
func pathMap(b *Backend) *framework.Path {
	return &framework.Path{
		Pattern: "admin/map/" + framework.MatchAllRegex("view"),

		Fields: map[string]*framework.FieldSchema{
			"view": {
				Type:        framework.TypeString,
				Description: "View path (GET/POST/DELETE) or list prefix (LIST).",
			},
			"target": {
				Type:        framework.TypeString,
				Required:    true,
				Description: "Canonical target this view points to: <mount>/<path>[#field].",
			},
			"adapter": {
				Type:        framework.TypeString,
				Description: `Adapter override: "kv2" or "raw". Empty auto-detects by target shape (Task 5).`,
			},
		},

		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathMapRead,
				Summary:  "Read a view's mapping entry.",
			},
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.pathMapWrite,
				Summary:  "Create or replace a view's mapping entry.",
			},
			logical.DeleteOperation: &framework.PathOperation{
				Callback: b.pathMapDelete,
				Summary:  "Delete a view's mapping entry.",
			},
			logical.ListOperation: &framework.PathOperation{
				Callback: b.pathMapList,
				Summary:  "Enumerate mapping entries under a prefix.",
			},
		},

		HelpSynopsis:    "Manage voidstar view-to-target mappings.",
		HelpDescription: "CRUD for the view-to-target mapping entries voidstar dereferences.",
	}
}

func (b *Backend) pathMapRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	view := data.Get("view").(string)

	entry, err := getMappingFromStorage(ctx, req.Storage, view)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}
	return &logical.Response{Data: mappingResponseData(entry)}, nil
}

func (b *Backend) pathMapWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	view := data.Get("view").(string)
	if view == "" {
		return logical.ErrorResponse("view path is required"), nil
	}

	target := data.Get("target").(string)
	adapter := data.Get("adapter").(string)
	if !validAdapterOverride(adapter) {
		return logical.ErrorResponse(fmt.Sprintf("adapter %q must be \"kv2\" or \"raw\"", adapter)), nil
	}

	ownMount := strings.TrimSuffix(req.MountPoint, "/")
	mount, err := canonicalizeTarget(target, ownMount)
	if err != nil {
		return logical.ErrorResponse(err.Error()), nil
	}

	cfg := b.currentConfig()
	if !cfg.targetMountAllowed(mount) {
		return logical.ErrorResponse(fmt.Sprintf("target mount %q is not permitted", mount)), nil
	}

	// A fresh MappingEntry, not a mutation of any existing one: this is
	// what "rewrite clears quarantine" means —
	// quarantine fields simply aren't carried forward.
	entry := &MappingEntry{
		Target:    target,
		Adapter:   adapter,
		WriteTime: time.Now().UTC(),
	}

	storageEntry, err := logical.StorageEntryJSON(mapStorageKey(view), entry)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, storageEntry); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *Backend) pathMapDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	view := data.Get("view").(string)
	if err := req.Storage.Delete(ctx, mapStorageKey(view)); err != nil {
		return nil, err
	}
	return nil, nil
}

func (b *Backend) pathMapList(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	prefix := data.Get("view").(string)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	keys, err := req.Storage.List(ctx, mapStorageKey(prefix))
	if err != nil {
		return nil, err
	}
	return logical.ListResponse(keys), nil
}

// getMappingFromStorage reads view's mapping entry fresh from storage,
// or nil if none exists.
func getMappingFromStorage(ctx context.Context, storage logical.Storage, view string) (*MappingEntry, error) {
	raw, err := storage.Get(ctx, mapStorageKey(view))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	var entry MappingEntry
	if err := raw.DecodeJSON(&entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// mappingResponseData builds the `admin/map/<view>` read response.
func mappingResponseData(entry *MappingEntry) map[string]interface{} {
	return map[string]interface{}{
		"target":             entry.Target,
		"adapter":            entry.Adapter,
		"write_time":         entry.WriteTime,
		"quarantined":        entry.Quarantined,
		"quarantine_cause":   entry.QuarantineCause,
		"revocation_outcome": entry.RevocationOutcome,
	}
}
