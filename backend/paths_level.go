package backend

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// levelStoragePrefix namespaces level entries. Keys carry no trailing
// slash (storage keys stay plain); the entry records the normalised prefix.
const levelStoragePrefix = "level/"

// LevelEntry is custom_metadata attached to a path prefix ("level").
type LevelEntry struct {
	Prefix         string            `json:"prefix"`
	CustomMetadata map[string]string `json:"custom_metadata"`
	WriteTime      time.Time         `json:"write_time"`
}

// normalisePrefix returns prefix with exactly one trailing slash, or ""
// when nothing remains.
func normalisePrefix(prefix string) string {
	p := strings.TrimRight(prefix, "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

func levelStorageKey(normPrefix string) string {
	return levelStoragePrefix + strings.TrimSuffix(normPrefix, "/")
}

func getLevel(ctx context.Context, storage logical.Storage, prefix string) (*LevelEntry, error) {
	raw, err := storage.Get(ctx, levelStorageKey(normalisePrefix(prefix)))
	if err != nil || raw == nil {
		return nil, err
	}
	var e LevelEntry
	if err := raw.DecodeJSON(&e); err != nil {
		return nil, err
	}
	return &e, nil
}

// pathLevel defines `admin/level/<prefix>`: GET/POST/DELETE/LIST of
// custom_metadata attached to a prefix, read back through
// `metadata/<prefix>/`.
func pathLevel(b *Backend) *framework.Path {
	return &framework.Path{
		Pattern: "admin/level/" + framework.MatchAllRegex("prefix"),
		Fields: map[string]*framework.FieldSchema{
			"prefix": {Type: framework.TypeString, Description: "Level prefix (GET/POST/DELETE) or list prefix (LIST)."},
			"custom_metadata": {
				Type:        framework.TypeKVPairs,
				Description: "String map returned by GET <mount>/metadata/<prefix>/ (KV v2 limits).",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.pathLevelRead, Summary: "Read a level's metadata."},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.pathLevelWrite, Summary: "Create or replace a level's metadata."},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.pathLevelDelete, Summary: "Delete a level's metadata."},
			logical.ListOperation:   &framework.PathOperation{Callback: b.pathLevelList, Summary: "Enumerate levels under a prefix."},
		},
		HelpSynopsis:    "Manage custom_metadata attached to a path prefix.",
		HelpDescription: "Level metadata is set by IaC and returned by GET <mount>/metadata/<prefix>/.",
	}
}

func (b *Backend) pathLevelRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	e, err := getLevel(ctx, req.Storage, data.Get("prefix").(string))
	if err != nil || e == nil {
		return nil, err
	}
	return &logical.Response{Data: map[string]interface{}{
		"prefix":          e.Prefix,
		"custom_metadata": e.CustomMetadata,
		"write_time":      e.WriteTime,
	}}, nil
}

func (b *Backend) pathLevelWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	prefix := normalisePrefix(data.Get("prefix").(string))
	if prefix == "" {
		return logical.ErrorResponse("level prefix is required"), nil
	}
	if msg := unknownFields(data); msg != "" {
		return logical.ErrorResponse(msg), nil
	}
	md := data.Get("custom_metadata").(map[string]string)
	if err := validateCustomMetadata(md); err != nil {
		return logical.ErrorResponse(err.Error()), nil
	}
	se, err := logical.StorageEntryJSON(levelStorageKey(prefix), &LevelEntry{Prefix: prefix, CustomMetadata: md, WriteTime: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	return nil, req.Storage.Put(ctx, se)
}

func (b *Backend) pathLevelDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	prefix := normalisePrefix(data.Get("prefix").(string))
	if prefix == "" {
		return nil, nil
	}
	return nil, req.Storage.Delete(ctx, levelStorageKey(prefix))
}

func (b *Backend) pathLevelList(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	prefix := data.Get("prefix").(string)
	if p := normalisePrefix(prefix); p != "" {
		prefix = p
	} else {
		prefix = ""
	}
	keys, err := req.Storage.List(ctx, levelStoragePrefix+prefix)
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return logical.ListResponse(keys), nil
}
