package backend

import (
	"context"
	"sort"
	"strings"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// pathMetadata defines `metadata/<view>`: GET returns the
// full synthetic KV v2 metadata document for a view, LIST synthesizes
// direct children from the mapping. Both stay supported; every write
// verb on this pattern is an explicit 405.
func pathMetadata(b *Backend) *framework.Path {
	return &framework.Path{
		Pattern: "metadata/" + framework.MatchAllRegex("view"),

		Fields: map[string]*framework.FieldSchema{
			"view": {
				Type:        framework.TypeString,
				Description: "View path (GET) or list prefix (LIST).",
			},
		},

		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathMetadataRead,
				Summary:  "Read a view's synthetic KV v2 metadata document.",
			},
			logical.ListOperation: &framework.PathOperation{
				Callback: b.pathMetadataList,
				Summary:  "List views under a prefix.",
			},
			logical.CreateOperation: path405("metadata/<view> is read-only"),
			logical.UpdateOperation: path405("metadata/<view> is read-only"),
			logical.PatchOperation:  path405("metadata/<view> is read-only"),
			logical.DeleteOperation: path405("metadata/<view> is read-only"),
		},

		HelpSynopsis:    "Read or list voidstar view metadata.",
		HelpDescription: "Synthetic KV v2 metadata document per view; LIST synthesizes direct children from the mapping.",
	}
}

// pathMetadataRead builds the metadata-endpoint document:
// always current_version/oldest_version 1 (no version history exists),
// max_versions 0 (unbounded/not applicable — voidstar has no
// versioning), created_time/updated_time both the mapping's write
// time (there is only ever one "version"), a versions map with
// exactly key "1", and custom_metadata gated on expose_targets.
func (b *Backend) pathMetadataRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	view := data.Get("view").(string)
	cfg := b.currentConfig()

	entry, err := getMappingFromStorage(ctx, req.Storage, view)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, logical.CodedError(404, "voidstar: no mapping for view %q", view)
	}

	return &logical.Response{Data: map[string]interface{}{
		"current_version": 1,
		"oldest_version":  1,
		"max_versions":    0,
		"created_time":    entry.WriteTime,
		"updated_time":    entry.WriteTime,
		"versions": map[string]interface{}{
			"1": map[string]interface{}{
				"created_time":  entry.WriteTime,
				"deletion_time": "",
				"destroyed":     false,
			},
		},
		"custom_metadata": customMetadata(cfg, entry.Target),
	}}, nil
}

// pathMetadataList synthesizes a LIST from the mapping keys:
// direct children only, intermediate segments suffixed "/", sorted;
// an empty result is a 404 (KV v2 convention), distinct from
// vs/admin/map/<prefix>'s LIST (paths_map.go's pathMapList), which
// permits an empty result — this is the consumer-facing KV v2
// emulation surface, that is the admin surface.
func (b *Backend) pathMetadataList(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	prefix := data.Get("view").(string)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	keys, err := req.Storage.List(ctx, mapStorageKey(prefix))
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, logical.CodedError(404, "voidstar: no views under prefix %q", prefix)
	}
	sort.Strings(keys)
	return logical.ListResponse(keys), nil
}
