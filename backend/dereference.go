package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbao/openbao/sdk/v2/logical"
)

// Dereference failure classes (spec §5 error mechanics, §10 logging):
// stable, distinct text per class so consumer-visible errors and
// backend logs are unambiguous about what went wrong. Task 8 keys its
// failure counters off a parallel, machine-readable set (failure
// classes in status.go), not these human-readable strings.
const (
	derefClassUpstreamRead   = "upstream dereference failed"
	derefClassMissingField   = "target field not found"
	derefClassLeaseViolation = "static-contract violation"
	derefClassQuarantined    = "view quarantined"
)

// splitTargetPathField splits a canonical target (spec §3:
// "<mount>/<path>[#field]") into its loopback-read path and optional
// field selector. entry.Target is already validated canonical at
// mapping-write time (mapping.go's canonicalizeTarget), so this is a
// pure split, not a re-validation.
func splitTargetPathField(target string) (path, field string) {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		return target[:i], target[i+1:]
	}
	return target, ""
}

// targetMount returns the mount segment (first path component) of a
// canonical target path (no #field suffix) — the value logged
// unconditionally (spec §10) and surfaced in consumer-visible errors
// only when expose_targets (spec §5, §6).
func targetMount(targetPath string) string {
	if i := strings.IndexByte(targetPath, '/'); i >= 0 {
		return targetPath[:i]
	}
	return targetPath
}

// selectAdapter picks the kv2/raw adapter for targetPath (spec §3): an
// explicit per-mapping override always wins; otherwise the choice is
// by target shape — a path whose second segment is "data" (KV v2's own
// data-endpoint convention, <mount>/data/<path>) is kv2, else raw.
func selectAdapter(override, targetPath string) string {
	if override != "" {
		return override
	}
	segs := strings.SplitN(targetPath, "/", 3)
	if len(segs) >= 2 && segs[1] == "data" {
		return "kv2"
	}
	return "raw"
}

// errKV2ShapeMismatch is the cause wrapped into the upstream-read-
// failure class when a kv2-adapter target's loopback response doesn't
// have the expected data.data envelope (spec §3) — a shape surprise
// from the upstream target, not a missing-field selection, so it's
// classified with upstream failures rather than derefClassMissingField.
var errKV2ShapeMismatch = fmt.Errorf("voidstar: kv2 adapter: response missing data.data envelope")

// unwrapAdapter applies the chosen adapter to a loopback response's
// raw data (spec §3): kv2 unwraps the data.data envelope, raw passes
// data through unmodified.
func unwrapAdapter(adapter string, raw map[string]interface{}) (map[string]interface{}, error) {
	if adapter != "kv2" {
		return raw, nil
	}
	inner, ok := raw["data"]
	if !ok {
		return nil, errKV2ShapeMismatch
	}
	m, ok := inner.(map[string]interface{})
	if !ok {
		return nil, errKV2ShapeMismatch
	}
	return m, nil
}

// selectField applies the spec §3 #field convention: {value: <field
// value>}. ok is false when the field is absent from data.
func selectField(data map[string]interface{}, field string) (map[string]interface{}, bool) {
	val, ok := data[field]
	if !ok {
		return nil, false
	}
	return map[string]interface{}{"value": val}, true
}

// dereferenceErrorMsg builds a consumer-visible error string for
// class, always identifying view, and mount only when
// cfg.ExposeTargets (spec §5: "target mount name appears only when
// expose_targets=true, otherwise redacted"). class alone makes the
// string stable and distinct per failure class.
func dereferenceErrorMsg(cfg *Config, class, view, mount string) string {
	if cfg != nil && cfg.ExposeTargets && mount != "" {
		return fmt.Sprintf("voidstar: %s for view %q (target mount %q)", class, view, mount)
	}
	return fmt.Sprintf("voidstar: %s for view %q", class, view)
}

// logDereferenceFailure logs view path + target mount + cause (spec
// §10) unconditionally and unredacted — logs are operator-side (spec
// §10: "values never logged; log redaction does not apply").
func (b *Backend) logDereferenceFailure(view, mount, cause string) {
	b.Logger().Warn("voidstar: dereference failed", "view", view, "target_mount", mount, "cause", cause)
}

// syntheticVersionMetadata builds the per-version synthetic metadata
// embedded in a data read (spec §5): always version 1, created_time is
// the mapping's write time, no delete/destroy semantics exist so
// deletion_time/destroyed are always the empty/false zero values.
func syntheticVersionMetadata(entry *MappingEntry, cfg *Config) map[string]interface{} {
	return map[string]interface{}{
		"version":         1,
		"created_time":    entry.WriteTime,
		"deletion_time":   "",
		"destroyed":       false,
		"custom_metadata": customMetadata(cfg, entry.Target),
	}
}

// customMetadata is {voidstar_target: <target>} only when
// cfg.ExposeTargets, else {} (spec §5).
func customMetadata(cfg *Config, target string) map[string]interface{} {
	if cfg != nil && cfg.ExposeTargets {
		return map[string]interface{}{"voidstar_target": target}
	}
	return map[string]interface{}{}
}

// checkStaticContract enforces spec §4's static-contract detection: a
// loopback response violates it when it carries a non-empty leaseID,
// or renewable=true with an empty leaseID. Neither holds here — the
// static contract is intact — so this is a no-op.
//
// A violation triggers, strictly in order (spec §4 mitigation 1-3):
//  1. Revoke what there is to revoke — RevokeLease(leaseID) when
//     leaseID is non-empty; the renewable-only case has no lease to
//     revoke, so this step is skipped entirely (cleanup is vacuous,
//     quarantine still applies). A RevokeLease failure is logged,
//     recorded as the quarantine's revocation outcome, and recycles
//     the loopback token (RevokeSelf, cascading to its child leases)
//     plus invalidates the client for lazy re-login — docs/NOTES.md F1:
//     RevokeLease succeeding is not evidence a lease existed
//     (idempotent revoke), but a RevokeLease *error* is a genuine
//     connectivity/permission failure worth reacting to.
//  2. Quarantine the mapping persistently (cause + revocation outcome
//     on the MappingEntry, spec §4 mitigation 2) so every subsequent
//     read of this view fast-fails without a loopback read.
//  3. Fail the triggering read itself with the same coded 502.
func (b *Backend) checkStaticContract(ctx context.Context, storage logical.Storage, client LoopbackClient, view string, entry *MappingEntry, mount, leaseID string, renewable bool) error {
	if leaseID == "" && !renewable {
		return nil
	}

	cause := "renewable target response with no lease_id"
	revocationOutcome := "" // renewable-only: nothing to revoke, vacuous cleanup
	if leaseID != "" {
		cause = "lease-bearing target response"
		if rerr := client.RevokeLease(ctx, leaseID); rerr != nil {
			revocationOutcome = fmt.Sprintf("revoke failed, token recycled: %v", rerr)
			b.Logger().Warn("voidstar: lease revocation failed, recycling loopback token", "view", view, "target_mount", mount, "error", rerr)
			b.recordFailure(mount, failureClassRevocationFailure)
			if serr := client.RevokeSelf(ctx); serr != nil {
				b.Logger().Warn("voidstar: loopback token self-revoke also failed", "view", view, "target_mount", mount, "error", serr)
			}
			b.invalidateLoopbackClient()
		} else {
			revocationOutcome = "revoked"
		}
	}

	if qerr := quarantineMapping(ctx, storage, view, entry, cause, revocationOutcome); qerr != nil {
		b.Logger().Warn("voidstar: failed to persist quarantine", "view", view, "target_mount", mount, "error", qerr)
	}

	b.logDereferenceFailure(view, mount, derefClassLeaseViolation)
	b.recordFailure(mount, failureClassLeaseViolation)
	cfg := b.currentConfig()
	return logical.CodedError(502, dereferenceErrorMsg(cfg, derefClassLeaseViolation, view, mount))
}

// quarantineMapping persists view's mapping entry with quarantine
// fields set (spec §4 mitigation 2), preserving its Target/Adapter/
// WriteTime as-is — only a rewrite through vs/admin/map/<view>
// (paths_map.go's pathMapWrite) clears quarantine (spec §4: "clears
// only when the mapping is rewritten").
func quarantineMapping(ctx context.Context, storage logical.Storage, view string, entry *MappingEntry, cause, revocationOutcome string) error {
	updated := *entry
	updated.Quarantined = true
	updated.QuarantineCause = cause
	updated.RevocationOutcome = revocationOutcome
	se, err := logical.StorageEntryJSON(mapStorageKey(view), &updated)
	if err != nil {
		return err
	}
	return storage.Put(ctx, se)
}
