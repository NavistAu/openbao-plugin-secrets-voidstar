package backend

import (
	"context"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// path405 builds a framework.PathOperation whose callback always
// fails with a 405 CodedError naming voidstar as read-only (spec §5).
// Registering this explicitly — rather than leaving a verb
// unregistered — matters because an
// unregistered verb also produces a 405 (via the SDK's
// ErrUnsupportedOperation), but with its generic "unsupported
// operation" text, not this spec-mandated one naming voidstar.
func path405(what string) *framework.PathOperation {
	return &framework.PathOperation{
		Callback: func(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
			return nil, logical.CodedError(405, "voidstar: read-only secrets engine; %s", what)
		},
		Summary: what,
	}
}

// path404 builds a framework.PathOperation whose callback always
// fails with an explicit 404 CodedError naming voidstar. Used by the
// catch-all (paths_notfound.go) for "anything else on the KV-
// emulation surface" (spec §5) — explicit rather than the SDK's
// implicit nil,nil-response 404 so the body names
// the unmatched path.
func path404() *framework.PathOperation {
	return &framework.PathOperation{
		Callback: func(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
			return nil, logical.CodedError(404, "voidstar: %q not found", req.Path)
		},
	}
}
