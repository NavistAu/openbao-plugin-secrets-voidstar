package backend

import (
	"errors"
	"strings"
	"time"
)

// mapStoragePrefix namespaces every mapping entry's storage key
// (`map/<view>`). This file
// defines the storage shape the CRUD handlers (paths_map.go) build on.
const mapStoragePrefix = "map/"

// mapStorageKey returns the storage key for view's mapping entry.
func mapStorageKey(view string) string {
	return mapStoragePrefix + view
}

// MappingEntry is the persisted `map/<view>` storage entry: the
// view's target, an optional adapter
// override, when it was last written, and quarantine state. No target
// secret value is ever part of this shape — target secret
// values are never persisted.
type MappingEntry struct {
	// Target is the canonicalized target string:
	// "<mount>/<path>[#<field>]".
	Target string `json:"target"`
	// Adapter overrides shape-based adapter selection:
	// "" (auto-detect), "kv2", or "raw".
	Adapter string `json:"adapter,omitempty"`
	// WriteTime is when this entry was last written, surfaced as the
	// view's synthetic `created_time`/`updated_time`.
	WriteTime time.Time `json:"write_time"`
	// Quarantined blocks reads of this view with a fast-fail 502,
	// without a loopback read, until the
	// mapping is rewritten.
	Quarantined bool `json:"quarantined,omitempty"`
	// QuarantineCause records why the mapping was quarantined: the
	// static-contract violation that triggered it.
	QuarantineCause string `json:"quarantine_cause,omitempty"`
	// RevocationOutcome records the outcome of the lease revocation
	// attempt that led to this quarantine (e.g.
	// "revoked", "revoke failed, token recycled").
	RevocationOutcome string `json:"revocation_outcome,omitempty"`
}

// validAdapters is the adapter override allowlist: "" (auto-
// detect, not a value a caller passes explicitly) plus "kv2"/"raw".
var validAdapters = map[string]bool{"kv2": true, "raw": true}

// validAdapterOverride reports whether adapter is a legal explicit
// override value: empty (no override) or exactly "kv2"/"raw".
func validAdapterOverride(adapter string) bool {
	return adapter == "" || validAdapters[adapter]
}

// canonicalizeTarget validates target against the canonical
// target grammar and the mount-recursion reject rule, returning the
// target's mount segment (the substring before its first "/") for the
// caller to check against Config.targetMountAllowed (which covers the
// fixed reject list — auth/sys/identity/cubbyhole — and the
// optional target_mount_allowlist). ownMount is the requesting
// backend's own mount point with any trailing slash trimmed; an empty
// ownMount skips the recursion check (used by tests that don't care
// about it).
//
// Non-canonical input is rejected outright, never normalized:
// no leading slash, no "//", no "." or ".." segments, no
// URL-encoding, no query string, exactly one optional non-empty
// "#field" suffix.
func canonicalizeTarget(target, ownMount string) (mount string, err error) {
	if target == "" {
		return "", errors.New("target is required")
	}
	if strings.Contains(target, "%") {
		return "", errors.New("target must not be URL-encoded")
	}
	if strings.Contains(target, "?") {
		return "", errors.New("target must not contain a query string")
	}

	path := target
	if i := strings.IndexByte(target, '#'); i >= 0 {
		field := target[i+1:]
		if field == "" || strings.ContainsRune(field, '#') {
			return "", errors.New("target must have exactly one non-empty #field suffix")
		}
		path = target[:i]
	}

	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return "", errors.New("target must be <mount>/<path>")
	}
	for _, seg := range segments {
		switch seg {
		case "":
			return "", errors.New("target must not have a leading slash, trailing slash, or repeated slashes")
		case ".", "..":
			return "", errors.New("target must not contain . or .. segments")
		}
	}

	mount = segments[0]
	if ownMount != "" && mount == ownMount {
		return "", errors.New("target must not reference the engine's own mount")
	}
	return mount, nil
}
