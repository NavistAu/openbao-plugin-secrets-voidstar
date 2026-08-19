package backend

import (
	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// pathNotFound is the spec §5 catch-all: "anything else on the KV-
// emulation surface ... 404." It must be registered LAST in the
// backend's Paths slice (docs/NOTES.md F4: routing is a first-match-
// wins linear scan) — every other pattern this backend registers
// (admin/config, admin/map/*, data/*, metadata/*, and the KV-reserved
// paths) is tried first, so this only ever fires for a request that
// matched none of them, which by construction excludes the vs/admin/*
// family (spec §5: the catch-all explicitly excludes it) whenever an
// admin path is one this backend actually knows about.
func pathNotFound(b *Backend) *framework.Path {
	op := path404()
	return &framework.Path{
		Pattern: ".*",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.CreateOperation: op,
			logical.ReadOperation:   op,
			logical.UpdateOperation: op,
			logical.PatchOperation:  op,
			logical.DeleteOperation: op,
			logical.ListOperation:   op,
		},
		HelpSynopsis: "Catch-all 404 for the KV-emulation surface (spec §5).",
	}
}
