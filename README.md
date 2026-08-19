# openbao-plugin-secrets-voidstar

A custom OpenBao secrets engine that serves read-only virtual views:
paths under its mount hold no secret material of their own, only
pointers ("mappings") to real paths in other mounts (KV v2, other
static engines). Reading a view path dereferences the pointer
server-side and returns the target's value. Secrets keep existing
exactly once, at their canonical subject-organized path; consumers get
a consumer-organized view onto them without copies, so N copies never
means N rotation targets. Dereference is server-side, so every client
(CI secret brokers, deploy-time resolvers, humans with the CLI) stays
a dumb reader — no client anywhere contains resolution logic. `void*`:
an untyped reference, meaningful only once dereferenced.

Status: **implemented** — tasks 0–10 complete, bench gate PASS
(2026-08-11).

May work with HashiCorp Vault — untested, unsupported.

## API surface

Mounted at an operator-chosen path (`vs/` below). Read-only KV v2 wire
shapes for raw API clients (`bao read`/`bao list`, HTTP clients) —
the `bao kv` CLI does not identify a custom plugin mount as KV v2, so
CLI compatibility is not claimed (spec §2).

**Consumer surface:**

| Verb/path | Behavior |
|---|---|
| `GET vs/data/<view>` | dereference; `{data: {data: <map>, metadata: <synthetic>}}` |
| `GET vs/metadata/<view>` | synthetic metadata document (`current_version`/`oldest_version` 1, `versions`, `custom_metadata`) |
| `LIST vs/metadata/<prefix>` | synthesized from mapping keys: direct children only, dir suffix `/`, sorted; empty → 404 |
| POST/PUT/PATCH/DELETE on `vs/data/*` or `vs/metadata/*` | 405, error text naming voidstar as read-only |
| any verb, including GET, on `vs/config`, `vs/delete/*`, `vs/undelete/*`, `vs/destroy/*`, `vs/detailed-metadata/*` | 405, same error (KV-reserved paths; `vs/config` is deliberately not the KV config endpoint) |
| anything else under the mount, excluding `vs/admin/*` | 404 |

**Admin surface** (separate path family, separate grants):

```
GET/POST/DELETE vs/admin/map/<view>    mapping CRUD; POST body
                                        {target: "...", adapter: "kv2|raw"}
LIST            vs/admin/map/<prefix>  enumerate mappings
GET/POST        vs/admin/config        engine config (spec §8)
GET             vs/admin/status        loopback client state, mapping
                                        count, failure counters keyed by
                                        target mount, quarantined
                                        mappings; no secret material
```

Target canonicalization, adapters (`kv2`/`raw`), `#field` selection,
lease/quarantine mechanics, and the full error table are enforced by
the engine's test suite (`backend/`).

## Deployment: loopback AppRole contract

voidstar authenticates to its own OpenBao instance via AppRole to
perform dereference reads. Two properties are load-bearing and must
hold at the estate layer, not just in engine code:

- **SecretID must be multi-use and non-expiring**
  (`secret_id_num_uses=0`, `secret_id_ttl=0`). The engine re-logs-in
  with the stored SecretID lazily on any 403/token-expiry, including
  after a process restart at any point in the SecretID's life —
  single-use or TTL'd SecretIDs break that restart recovery
  permanently and must not be issued to this engine (spec §4).
- **Policy = the mapped target set, plus one fixed grant.** The
  engine's own policy is derived from the mapping by the same IaC
  that writes the mapping — read on exactly the target paths that
  appear in `vs/admin/map/*`, no more — plus `sys/leases/revoke`
  (the one fixed, non-derived grant, used only for the static-contract
  cleanup path). Token self-renewal and `auth/token/revoke-self` need
  no extra grant — they ride the `default` policy every token
  carries. Grant and mapping cannot drift because the same tofu run
  produces both (spec §4 mitigation 2).

Rotating the loopback SecretID is a config rewrite
(`POST vs/admin/config`) — write-only, never readable back.

## Bench record

Task 9 bench gate run 2026-08-11. Local: scratch
`ghcr.io/openbao/openbao:2.5.5` container (`vs-bench`), server mode
(file storage, `bench/config/bao.hcl`), both plugins cross-compiled
`linux/arm64` (`bench/build.sh`):

- `openbao-plugin-secrets-voidstar` registered as catalog name `vs`,
  mounted at `vs/`, sha256
  `fc01d20b250f1f075c59ac9a4ba93109e7cc9853f70dbfee438c2052b4ca0e81`.
- `dynfake` (throwaway lease-emitting test plugin, `bench/dynfake/`)
  registered as catalog name `dynfake`, mounted at `dynfake/`, sha256
  `a0f4ede602f91babf3b6badfd1e31f3605e2c63808e87bc5320a25d7b83a3639`.

Targets: `kv/` (kv-v2, seeded `simple`/`structured`/`tree/{a,b,nested/c}`),
`kv1/` (kv v1, seeded `raw-item`). Loopback AppRole `vs-loopback`
(`secret_id_num_uses=0`, `secret_id_ttl=0` per spec §4's restart-
recovery contract) with a policy granting read on `kv/data/*`,
`kv1/*`, `dynfake/leaky`, and update+sudo on `sys/leases/revoke`.
Eight mappings written under `vs/admin/map/team/...` covering the kv2
adapter (whole map), structured whole-map, `#field` select, the raw
adapter (explicit override, kv v1 target), the nested tree (list
sweep), and the dynfake lease-drill target. Scripts: `bench/build.sh`,
`bench/setup.sh`, `bench/run.sh` (drives `bench/drive`, a Go program
issuing real HTTP reads via the api/v2 client — no backend-internal
calls), `bench/cleanup.sh`.

### Overall verdict: **PASS**

No engine bug found. (Two bugs were found and fixed in the bench
harness itself — `bench/drive/main.go` — before this recorded run: the
api/v2 client decodes JSON numbers as `json.Number` not `float64`, and
a clean 404 `Read`/`List` response returns `(nil, nil)`, not a Go
error carrying status 404. Neither touched `backend/`.)

| # | Assertion group | Verdict | Notes |
|---|---|---|---|
| 1 | Data reads (kv2 whole-map, structured whole-map, `#field` select, raw adapter) | PASS | All four views returned exact expected values in KV v2 data-read shape (`{data: {...}, metadata: {...}}`) |
| 2 | Metadata read | PASS | `GET vs/metadata/team/simple` matched spec §5's synthetic document exactly: `current_version`/`oldest_version` 1, `max_versions` 0, `created_time`==`updated_time`, `versions["1"]` present, `custom_metadata: {}` (expose_targets=false) |
| 3 | List sweep | PASS | `LIST vs/metadata/team/tree` returned `[a, b, nested/]` — direct children only, dir suffix, sorted |
| 4 | 404 unmapped | PASS | `GET vs/data/team/nope` → 404 |
| 5 | 405 matrix (spot checks) | PASS | `POST vs/data/team/simple` → 405 with voidstar read-only text; `GET vs/config` → 405 with voidstar-specific "not the KV config endpoint" text |
| 6 | **Lease/quarantine drill (mandatory)** | PASS | First `GET vs/data/team/dynfake` minted a real lease on `dynfake` (dynfake's read counter 0→1, proving a genuine loopback read happened, not a fake), voidstar detected the static-contract violation and returned 502; `vs/admin/status` showed `team/dynfake` quarantined with `revocation_outcome: revoked`; `LIST sys/leases/lookup/dynfake/leaky` returned 404 (no active lease) — the real `sys/leases/revoke` path proven live; a second read fast-failed 502 (`view quarantined`) with dynfake's counter unchanged at 1, proving no second loopback call occurred |

Full transcript: `bench/scratch/setup-output.txt`, `bench/scratch/drive-output.txt` (scratch, gitignored, not committed).

## Release and estate pin

Tags matching `v*` trigger the Woodpecker release job (below):
cross-compiled `linux_amd64`/`linux_arm64` tarballs plus a
`SHA256SUMS` file, published to a forge release
(`tea releases create --repo <org>/openbao-plugin-secrets-voidstar`,
`forgejo_token` secret, tag-scoped). The estate installs by checksum,
not by tag alone: the consuming service (in the estate's
infrastructure repo) pins an exact plugin version + release URL in
`vars/main.yaml`, downloads the release tarball, and verifies its
SHA256 against `SHA256SUMS` before the binary reaches the plugin
directory — the same checksum-pinned-artifact pattern used for every
other estate-installed binary. Bumping the estate pin is a
`vars/main.yaml` edit in the estate repo, not a push here.

## CI

Woodpecker (the forge's CI, `.woodpecker.yml`): `go build
./...` + `go test -race ./...` on push/PR/manual; tagged releases
(`v*`) build the cross-platform artifacts and publish them to the
forge with checksums, per Release above. Registered 2026-08-11; push
pipeline green (run 1) and tag release path validated same day —
v0.1.0 published (run 13) with both tarballs + SHA256SUMS after four
real CI defects were fixed at the gate: Woodpecker config-time
substitution mangling artifact names, its parser rejecting
escaped/commented dollar-brace forms, a read-scoped forge token
(release create 404), and a greedy release-id parse uploading to the
wrong release. Release logic lives in ci/release.sh (create-or-lookup,
idempotent) for exactly these reasons.

## License

MPL-2.0. See `LICENSE`.
