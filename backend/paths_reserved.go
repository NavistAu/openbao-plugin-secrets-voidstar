package backend

import (
	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// reservedPathSpecs are the KV-reserved paths: "any verb,
// including GET" is a 405 on these — voidstar deliberately does not
// implement KV v2's config/delete/undelete/destroy/detailed-metadata
// semantics; vs/config in particular is NOT the KV config endpoint
// (engine admin lives under vs/admin/).
var reservedPathSpecs = []struct {
	pattern string
	what    string
}{
	{"config", "vs/config is not the KV config endpoint; voidstar admin config lives under vs/admin/config"},
	{"delete/" + framework.MatchAllRegex("view"), "voidstar has no delete/undelete/destroy semantics"},
	{"undelete/" + framework.MatchAllRegex("view"), "voidstar has no delete/undelete/destroy semantics"},
	{"destroy/" + framework.MatchAllRegex("view"), "voidstar has no delete/undelete/destroy semantics"},
	{"detailed-metadata/" + framework.MatchAllRegex("view"), "voidstar has no detailed-metadata endpoint"},
}

// reservedPaths builds one framework.Path per reservedPathSpecs entry,
// every operation (including read and list) mapped to the same 405.
func reservedPaths(b *Backend) []*framework.Path {
	paths := make([]*framework.Path, len(reservedPathSpecs))
	for i, s := range reservedPathSpecs {
		op := path405(s.what)
		paths[i] = &framework.Path{
			Pattern: s.pattern,
			Operations: map[logical.Operation]framework.OperationHandler{
				logical.CreateOperation: op,
				logical.ReadOperation:   op,
				logical.UpdateOperation: op,
				logical.PatchOperation:  op,
				logical.DeleteOperation: op,
				logical.ListOperation:   op,
			},
			HelpSynopsis: "Reserved KV v2 path; not implemented by voidstar.",
		}
	}
	return paths
}
