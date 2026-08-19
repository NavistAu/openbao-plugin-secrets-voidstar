package backend

import (
	"context"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// pathData defines `data/<view>` (spec §5): the consumer dereference
// read, KV v2 data-read wire-compatible. GET only in this task; Task 7
// adds explicit 405 handlers for the write verbs on this same pattern
// (spec §5: "any other verb on vs/data/* ... 405, error text naming
// voidstar as read-only").
func pathData(b *Backend) *framework.Path {
	return &framework.Path{
		Pattern: "data/" + framework.MatchAllRegex("view"),

		Fields: map[string]*framework.FieldSchema{
			"view": {
				Type:        framework.TypeString,
				Description: "View path to dereference.",
			},
		},

		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathDataRead,
				Summary:  "Dereference a view to its target's current value.",
			},
			// Explicit 405s (spec §5: "any other verb on vs/data/* ...
			// 405, error text naming voidstar as read-only") —
			// must be registered, not left
			// unregistered, or the 405 body text is the SDK's generic
			// one instead of this spec-mandated one.
			logical.CreateOperation: path405("data/<view> is read-only"),
			logical.UpdateOperation: path405("data/<view> is read-only"),
			logical.PatchOperation:  path405("data/<view> is read-only"),
			logical.DeleteOperation: path405("data/<view> is read-only"),
		},

		HelpSynopsis:    "Dereference a voidstar view.",
		HelpDescription: "Server-side dereferences the view's mapped target and returns its value in KV v2 data-read shape (spec §5).",
	}
}

// pathDataRead implements the spec §4/§5 dereference read path:
// mapping lookup (404 unmapped), quarantine fast-fail (502, no
// loopback call), loopback read, adapter unwrap, #field select,
// response in KV v2 data-read shape with embedded synthetic per-
// version metadata.
func (b *Backend) pathDataRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	view := data.Get("view").(string)
	cfg := b.currentConfig()

	entry, err := getMappingFromStorage(ctx, req.Storage, view)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, logical.CodedError(404, "voidstar: no mapping for view %q", view)
	}

	targetPath, field := splitTargetPathField(entry.Target)
	mount := targetMount(targetPath)

	if entry.Quarantined {
		b.logDereferenceFailure(view, mount, derefClassQuarantined)
		b.recordFailure(mount, failureClassQuarantinedFastFail)
		return nil, logical.CodedError(502, dereferenceErrorMsg(cfg, derefClassQuarantined, view, mount))
	}

	client, err := b.ensureLoopbackClient(ctx)
	if err != nil {
		b.logDereferenceFailure(view, mount, derefClassUpstreamRead)
		b.recordFailure(mount, failureClassUpstreamRead)
		return nil, logical.CodedError(502, dereferenceErrorMsg(cfg, derefClassUpstreamRead, view, mount))
	}

	rawData, leaseID, renewable, err := client.Read(ctx, targetPath)
	if err != nil {
		b.handleLoopbackErr(err)
		b.logDereferenceFailure(view, mount, derefClassUpstreamRead)
		b.recordFailure(mount, failureClassUpstreamRead)
		return nil, logical.CodedError(502, dereferenceErrorMsg(cfg, derefClassUpstreamRead, view, mount))
	}

	if cerr := b.checkStaticContract(ctx, req.Storage, client, view, entry, mount, leaseID, renewable); cerr != nil {
		return nil, cerr
	}

	adapter := selectAdapter(entry.Adapter, targetPath)
	unwrapped, uerr := unwrapAdapter(adapter, rawData)
	if uerr != nil {
		b.logDereferenceFailure(view, mount, derefClassUpstreamRead)
		b.recordFailure(mount, failureClassUpstreamRead)
		return nil, logical.CodedError(502, dereferenceErrorMsg(cfg, derefClassUpstreamRead, view, mount))
	}

	respData := unwrapped
	if field != "" {
		selected, ok := selectField(unwrapped, field)
		if !ok {
			b.logDereferenceFailure(view, mount, derefClassMissingField)
			b.recordFailure(mount, failureClassMissingField)
			return nil, logical.CodedError(502, dereferenceErrorMsg(cfg, derefClassMissingField, view, mount))
		}
		respData = selected
	}

	return &logical.Response{
		Data: map[string]interface{}{
			"data":     respData,
			"metadata": syntheticVersionMetadata(entry, cfg),
		},
	}, nil
}
