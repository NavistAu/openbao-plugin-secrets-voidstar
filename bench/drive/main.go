// Command drive is the Task 9 bench gate's assertion runner. It hits
// the scratch vs-bench server (bench/setup.sh already wrote config +
// every mapping) with real HTTP reads via the api/v2 client — no
// backend-internal calls, no fakes — and checks every behavior
// docs/PLAN.md Task 9 requires, printing PASS/FAIL per assertion
// group. Exits non-zero if anything failed.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	api "github.com/openbao/openbao/api/v2"
)

var (
	groupsFailed = map[string]bool{}
	anyFail      bool
)

func record(group string, pass bool, format string, args ...interface{}) {
	status := "PASS"
	if !pass {
		status = "FAIL"
		groupsFailed[group] = true
		anyFail = true
	}
	fmt.Printf("[%s] %-24s %s\n", status, group, fmt.Sprintf(format, args...))
}

// httpStatus extracts the HTTP status code from an api/v2 error, if
// any (mirrors backend/loopback.go's is403 classification approach).
func httpStatus(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var respErr *api.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode, true
	}
	return 0, false
}

func errContains(err error, substr string) bool {
	if err == nil {
		return false
	}
	var respErr *api.ResponseError
	if errors.As(err, &respErr) {
		for _, e := range respErr.Errors {
			if strings.Contains(e, substr) {
				return true
			}
		}
	}
	return strings.Contains(err.Error(), substr)
}

func main() {
	addr := os.Getenv("BAO_ADDR")
	token := os.Getenv("BAO_TOKEN")
	if addr == "" || token == "" {
		fmt.Fprintln(os.Stderr, "BAO_ADDR and BAO_TOKEN must be set")
		os.Exit(2)
	}

	cfg := api.DefaultConfig()
	cfg.Address = addr
	c, err := api.NewClient(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "client: %v\n", err)
		os.Exit(2)
	}
	c.SetToken(token)

	dataReads(c)
	metadataRead(c)
	listSweep(c)
	notFound(c)
	verbMatrix(c)
	leaseQuarantineDrill(c)

	if anyFail {
		fmt.Println("\nRESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: PASS")
}

// --- group: data-reads ---

func dataReads(c *api.Client) {
	const group = "data-reads"

	// kv2 adapter, whole map.
	assertDataEquals(c, group, "vs/data/team/simple", map[string]interface{}{"password": "hunter2"})

	// structured whole-map (multi-field).
	assertDataEquals(c, group, "vs/data/team/structured", map[string]interface{}{
		"username": "bob", "password": "hunter2", "host": "db.internal",
	})

	// #field select -> {value: ...}.
	assertDataEquals(c, group, "vs/data/team/structured_field", map[string]interface{}{"value": "hunter2"})

	// raw adapter (kv v1 target, explicit override).
	assertDataEquals(c, group, "vs/data/team/raw", map[string]interface{}{"foo": "bar"})
}

func assertDataEquals(c *api.Client, group, path string, want map[string]interface{}) {
	secret, err := c.Logical().Read(path)
	if err != nil {
		record(group, false, "%s: read error: %v", path, err)
		return
	}
	if secret == nil {
		record(group, false, "%s: nil secret", path)
		return
	}
	got, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		record(group, false, "%s: data field missing or wrong shape: %#v", path, secret.Data["data"])
		return
	}
	if !reflect.DeepEqual(got, want) {
		record(group, false, "%s: data = %#v, want %#v", path, got, want)
		return
	}
	if _, ok := secret.Data["metadata"].(map[string]interface{}); !ok {
		record(group, false, "%s: metadata field missing from KV v2 data-read shape", path)
		return
	}
	record(group, true, "%s -> %#v", path, got)
}

// --- group: metadata ---

func metadataRead(c *api.Client) {
	const group = "metadata"
	secret, err := c.Logical().Read("vs/metadata/team/simple")
	if err != nil || secret == nil {
		record(group, false, "vs/metadata/team/simple: read error: %v", err)
		return
	}
	d := secret.Data
	checks := map[string]interface{}{
		"current_version": json1,
		"oldest_version":  json1,
		"max_versions":    json0,
	}
	ok := true
	for field, want := range checks {
		if got := numeric(d[field]); got != want {
			record(group, false, "%s = %v, want %v", field, d[field], want)
			ok = false
		}
	}
	versions, vok := d["versions"].(map[string]interface{})
	if !vok || versions["1"] == nil {
		record(group, false, `versions map missing key "1": %#v`, d["versions"])
		ok = false
	}
	if cm, ok2 := d["custom_metadata"].(map[string]interface{}); !ok2 || len(cm) != 0 {
		record(group, false, "custom_metadata = %#v, want {} (expose_targets=false)", d["custom_metadata"])
		ok = false
	}
	if d["created_time"] == nil || d["created_time"] != d["updated_time"] {
		record(group, false, "created_time (%v) != updated_time (%v)", d["created_time"], d["updated_time"])
		ok = false
	}
	if ok {
		record(group, true, "vs/metadata/team/simple document matches spec §5 shape")
	}
}

const (
	json1 = float64(1)
	json0 = float64(0)
)

// numeric normalizes a decoded JSON number to float64. The api/v2
// client parses response bodies with json.Decoder.UseNumber(), so
// numbers land as json.Number (a string underneath), not float64 —
// this must handle both.
func numeric(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return -1
		}
		return f
	default:
		return -1
	}
}

// --- group: list-sweep ---

func listSweep(c *api.Client) {
	const group = "list-sweep"
	secret, err := c.Logical().List("vs/metadata/team/tree")
	if err != nil || secret == nil {
		record(group, false, "LIST vs/metadata/team/tree: error: %v", err)
		return
	}
	raw, ok := secret.Data["keys"].([]interface{})
	if !ok {
		record(group, false, "keys field missing or wrong shape: %#v", secret.Data["keys"])
		return
	}
	got := make([]string, 0, len(raw))
	for _, k := range raw {
		got = append(got, k.(string))
	}
	sort.Strings(got)
	want := []string{"a", "b", "nested/"}
	if !reflect.DeepEqual(got, want) {
		record(group, false, "keys = %v, want %v", got, want)
		return
	}
	record(group, true, "direct children only, dir suffix, sorted: %v", got)
}

// --- group: 404-unmapped ---

func notFound(c *api.Client) {
	const group = "404-unmapped"
	// api/v2's Logical().Read special-cases 404 responses whose body
	// doesn't carry warnings/data (exactly voidstar's {"errors":[...]}
	// shape, docs/NOTES.md F2) into (nil secret, nil error) rather than
	// surfacing it as a Go error — so a clean 404 here is secret==nil,
	// err==nil, not an *api.ResponseError.
	secret, err := c.Logical().Read("vs/data/team/nope")
	if err != nil {
		record(group, false, "vs/data/team/nope: want 404 (nil,nil), got error: %v", err)
		return
	}
	if secret != nil {
		record(group, false, "vs/data/team/nope: want 404 (nil,nil), got secret: %#v", secret.Data)
		return
	}
	record(group, true, "vs/data/team/nope -> 404 (nil,nil)")
}

// --- group: 405-matrix ---

func verbMatrix(c *api.Client) {
	const group = "405-matrix"

	_, err := c.Logical().Write("vs/data/team/simple", map[string]interface{}{"foo": "bar"})
	code, ok := httpStatus(err)
	if !ok || code != 405 || !errContains(err, "voidstar") || !errContains(err, "read-only") {
		record(group, false, "POST vs/data/team/simple: status=%v ok=%v err=%v, want 405 voidstar read-only text", code, ok, err)
	} else {
		record(group, true, "POST vs/data/team/simple -> 405, voidstar read-only text")
	}

	_, err = c.Logical().Read("vs/config")
	code, ok = httpStatus(err)
	if !ok || code != 405 || !errContains(err, "voidstar") || !errContains(err, "vs/admin/config") {
		record(group, false, "GET vs/config: status=%v ok=%v err=%v, want 405 voidstar-specific text", code, ok, err)
	} else {
		record(group, true, "GET vs/config -> 405, voidstar-specific text")
	}
}

// --- group: lease-quarantine-drill (mandatory, plan Task 9) ---

func leaseQuarantineDrill(c *api.Client) {
	const group = "lease-quarantine-drill"

	before, err := readCount(c)
	if err != nil {
		record(group, false, "dynfake/count (before): %v", err)
		return
	}

	// First read: mints a real lease on dynfake, voidstar must detect
	// the static-contract violation, revoke it, quarantine the
	// mapping, and fail this read 502.
	_, err = c.Logical().Read("vs/data/team/dynfake")
	code, ok := httpStatus(err)
	if !ok || code != 502 || !errContains(err, "voidstar") {
		record(group, false, "first read vs/data/team/dynfake: status=%v ok=%v err=%v, want 502 voidstar", code, ok, err)
		return
	}
	record(group, true, "first read vs/data/team/dynfake -> 502 contract-violation")

	after, err := readCount(c)
	if err != nil {
		record(group, false, "dynfake/count (after 1st): %v", err)
		return
	}
	if after != before+1 {
		record(group, false, "dynfake read counter = %v, want %v (proves dynfake WAS read once, a real lease was minted)", after, before+1)
		return
	}
	record(group, true, "dynfake read counter incremented exactly once (%v -> %v): real lease was minted", before, after)

	// status: mapping must be quarantined.
	statusSecret, err := c.Logical().Read("vs/admin/status")
	if err != nil || statusSecret == nil {
		record(group, false, "vs/admin/status: error: %v", err)
		return
	}
	quarantined, _ := statusSecret.Data["quarantined_mappings"].([]interface{})
	found := false
	for _, q := range quarantined {
		m, _ := q.(map[string]interface{})
		if m["view"] == "team/dynfake" {
			found = true
			if m["revocation_outcome"] != "revoked" {
				record(group, false, "team/dynfake revocation_outcome = %v, want \"revoked\"", m["revocation_outcome"])
				return
			}
		}
	}
	if !found {
		record(group, false, "vs/admin/status quarantined_mappings missing team/dynfake: %#v", quarantined)
		return
	}
	record(group, true, "vs/admin/status shows team/dynfake quarantined, revocation_outcome=revoked")

	// The real sys/leases/revoke path proven live: no active lease
	// remains under dynfake/leaky (LIST sys/leases/lookup fails/empty).
	// Like Read, a clean 404 List surfaces as (nil secret, nil error).
	leaseList, err := c.Logical().List("sys/leases/lookup/dynfake/leaky")
	if err != nil {
		record(group, false, "sys/leases/lookup/dynfake/leaky: want 404 (nil,nil), got error: %v", err)
		return
	}
	if leaseList != nil {
		record(group, false, "sys/leases/lookup/dynfake/leaky: want 404 (nil,nil), got leases: %#v", leaseList.Data)
		return
	}
	record(group, true, "sys/leases/lookup/dynfake/leaky -> 404 (nil,nil): minted lease is gone, sys/leases/revoke proven live")

	// Second read: must fast-fail without touching dynfake again.
	_, err = c.Logical().Read("vs/data/team/dynfake")
	code, ok = httpStatus(err)
	if !ok || code != 502 || !errContains(err, "quarantined") {
		record(group, false, "second read vs/data/team/dynfake: status=%v ok=%v err=%v, want 502 quarantined", code, ok, err)
		return
	}
	afterSecond, err := readCount(c)
	if err != nil {
		record(group, false, "dynfake/count (after 2nd): %v", err)
		return
	}
	if afterSecond != after {
		record(group, false, "dynfake read counter after 2nd voidstar read = %v, want unchanged %v (no loopback happened)", afterSecond, after)
		return
	}
	record(group, true, "second read fast-fails 502 quarantined; dynfake counter unchanged (%v): no loopback happened", afterSecond)
}

func readCount(c *api.Client) (float64, error) {
	secret, err := c.Logical().Read("dynfake/count")
	if err != nil {
		return 0, err
	}
	if secret == nil {
		return 0, fmt.Errorf("nil secret")
	}
	return numeric(secret.Data["count"]), nil
}
