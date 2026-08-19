package backend

import (
	"context"
	"sort"
	"strings"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// Failure classes (spec §10, plan Task 8): the machine-readable keys
// failure_counters is indexed by, one per distinct dereference failure
// site. Distinct from dereference.go's derefClass* strings, which are
// the human-readable text embedded in consumer-visible errors and log
// lines — these are the stable counter keys admin/status exposes.
const (
	failureClassUpstreamRead        = "upstream_read_failure"
	failureClassMissingField        = "missing_field"
	failureClassLeaseViolation      = "lease_violation"
	failureClassRevocationFailure   = "revocation_failure"
	failureClassQuarantinedFastFail = "quarantined_fastfail"
)

// pathStatus defines `admin/status` (spec §5, §10): loopback client
// state, mapping count, failure counters, and quarantined mappings.
// Admin-only, separate grant family from consumer reads (spec §6). No
// secret material ever appears in the response.
func pathStatus(b *Backend) *framework.Path {
	return &framework.Path{
		Pattern: "admin/status",

		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathStatusRead,
				Summary:  "Read voidstar engine status.",
			},
		},

		HelpSynopsis:    "voidstar engine status.",
		HelpDescription: "Loopback client health, mapping count, failure counters keyed by target mount, and quarantined mappings (spec §5, §10). No secret material.",
	}
}

func (b *Backend) pathStatusRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	b.mu.RLock()
	clientConnected := b.client != nil
	tokenTTL := b.tokenTTL
	tokenRenewedAt := b.tokenRenewedAt
	failureCounters := make(map[string]map[string]int, len(b.failureCounters))
	for mount, classes := range b.failureCounters {
		cp := make(map[string]int, len(classes))
		for class, count := range classes {
			cp[class] = count
		}
		failureCounters[mount] = cp
	}
	b.mu.RUnlock()

	initFailures, initLastErr := b.loopbackGov.snapshot()

	mappings, err := walkAllMappings(ctx, req.Storage)
	if err != nil {
		return nil, err
	}

	var quarantined []map[string]interface{}
	for view, entry := range mappings {
		if !entry.Quarantined {
			continue
		}
		quarantined = append(quarantined, map[string]interface{}{
			"view":               view,
			"quarantine_cause":   entry.QuarantineCause,
			"revocation_outcome": entry.RevocationOutcome,
		})
	}
	sort.Slice(quarantined, func(i, j int) bool {
		return quarantined[i]["view"].(string) < quarantined[j]["view"].(string)
	})

	return &logical.Response{Data: map[string]interface{}{
		"loopback": map[string]interface{}{
			"client_connected":          clientConnected,
			"token_ttl_seconds":         int(tokenTTL.Seconds()),
			"token_renewed_at":          tokenRenewedAt,
			"init_consecutive_failures": initFailures,
			"init_last_error":           initLastErr,
		},
		"mapping_count":        len(mappings),
		"failure_counters":     failureCounters,
		"quarantined_mappings": quarantined,
	}}, nil
}

// walkAllMappings recursively walks every `map/<view>` storage entry
// (spec §8), returning each mapping keyed by its full view path.
// storage.List only returns direct children (the same primitive
// paths_map.go's pathMapList and paths_metadata.go's pathMetadataList
// build their single-level listings on) — a full inventory for
// admin/status's mapping count and quarantine listing needs recursion
// over the "/"-suffixed intermediate segments.
func walkAllMappings(ctx context.Context, storage logical.Storage) (map[string]*MappingEntry, error) {
	result := map[string]*MappingEntry{}
	var walk func(prefix string) error
	walk = func(prefix string) error {
		keys, err := storage.List(ctx, mapStorageKey(prefix))
		if err != nil {
			return err
		}
		for _, k := range keys {
			view := prefix + k
			if strings.HasSuffix(k, "/") {
				if err := walk(view); err != nil {
					return err
				}
				continue
			}
			entry, err := getMappingFromStorage(ctx, storage, view)
			if err != nil {
				return err
			}
			if entry != nil {
				result[view] = entry
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	return result, nil
}
