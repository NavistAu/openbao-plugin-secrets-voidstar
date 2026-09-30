#!/usr/bin/env bash
# Integration checks for the grant-model voidstar changes on a throwaway
# OpenBao (dev mode, fake data only). Builds the plugin, starts OpenBao +
# Postgres containers on a private network, runs checks, removes everything.
# Requires: docker, curl, jq.  Exit code is non-zero on any FAIL.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PORT=${PORT:-18310}
A=http://127.0.0.1:$PORT/v1
BAO=vs-grant-bao PG=vs-grant-pg NET=vs-grant-net
IMG=${OPENBAO_IMAGE:-openbao/openbao:2.7.0}
WORK=$(mktemp -d)
FAILS=0

cleanup() {
  docker rm -f "$BAO" "$PG" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT
cleanup_start() { docker rm -f "$BAO" "$PG" >/dev/null 2>&1 || true; docker network rm "$NET" >/dev/null 2>&1 || true; }
cleanup_start

pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; FAILS=$((FAILS + 1)); }
check() { # name expected actual
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1 (want '$2', got '$3')"; fi
}

# root <curl args>: as root; tok <token> <curl args>: as token
root() { curl -s -H "X-Vault-Token: root" "$@"; }
tok()  { local t=$1; shift; curl -s -H "X-Vault-Token: $t" "$@"; }
code() { local t=$1; shift; curl -s -o /dev/null -w '%{http_code}' -H "X-Vault-Token: $t" "$@"; }
post() { root -X POST "$A/$1" -d "$2"; }

echo "== build plugin"
ARCH=$(docker info --format '{{.Architecture}}')
case "$ARCH" in aarch64|arm64) GOARCH=arm64 ;; *) GOARCH=amd64 ;; esac
docker run --rm -v "$ROOT":/src -v "$WORK":/out -w /src \
  -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=$GOARCH -e GOFLAGS=-buildvcs=false \
  golang:1.25-alpine go build -o /out/openbao-plugin-secrets-voidstar ./cmd/openbao-plugin-secrets-voidstar
SHA=$(shasum -a 256 "$WORK/openbao-plugin-secrets-voidstar" | cut -d' ' -f1)

echo "== start containers"
docker network create "$NET" >/dev/null
docker run -d --name "$PG" --network "$NET" -e POSTGRES_PASSWORD=pgpass postgres:16-alpine >/dev/null
docker run -d --name "$BAO" --network "$NET" -p "$PORT":8200 -v "$WORK":/plugins:ro "$IMG" \
  server -dev -dev-root-token-id=root -dev-listen-address=0.0.0.0:8200 -dev-plugin-dir=/plugins >/dev/null
for _ in $(seq 30); do curl -s "$A/sys/health" >/dev/null && break; sleep 1; done
for _ in $(seq 30); do docker exec "$PG" pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
sleep 2

echo "== configure"
post sys/plugins/catalog/secret/vs "{\"sha256\":\"$SHA\",\"command\":\"openbao-plugin-secrets-voidstar\"}" >/dev/null
post sys/mounts/vs '{"type":"vs"}' >/dev/null
post sys/mounts/kv '{"type":"kv","options":{"version":"2"}}' >/dev/null
post sys/mounts/database '{"type":"database"}' >/dev/null
post database/config/pg '{"plugin_name":"postgresql-database-plugin","allowed_roles":"ro","connection_url":"postgresql://{{username}}:{{password}}@'"$PG"':5432/postgres?sslmode=disable","username":"postgres","password":"pgpass"}' >/dev/null
post database/roles/ro '{"db_name":"pg","default_ttl":"1h","max_ttl":"1h","creation_statements":"CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '"'{{password}}'"' VALID UNTIL '"'{{expiration}}'"';"}' >/dev/null
post sys/auth/approle '{"type":"approle"}' >/dev/null
root -X PUT "$A/sys/policies/acl/voidstar-loopback" -d '{"policy":"path \"kv/data/*\" {capabilities=[\"read\"]}\npath \"database/creds/*\" {capabilities=[\"read\"]}"}' >/dev/null
post auth/approle/role/voidstar-loopback '{"token_policies":"voidstar-loopback","secret_id_num_uses":0,"secret_id_ttl":0}' >/dev/null
RID=$(root "$A/auth/approle/role/voidstar-loopback/role-id" | jq -r .data.role_id)
SID=$(root -X POST "$A/auth/approle/role/voidstar-loopback/secret-id" | jq -r .data.secret_id)
post vs/admin/config "{\"role_id\":\"$RID\",\"secret_id\":\"$SID\",\"api_addr\":\"http://127.0.0.1:8200\"}" >/dev/null
post kv/data/src/one '{"data":{"v":"FAKE-one"},"options":{}}' >/dev/null
root -X POST "$A/kv/metadata/src/one" -d '{"custom_metadata":{"upstream":"leak"}}' >/dev/null

echo "== checks"
# 1. unknown field on a mapping write is 400
check "unknown field -> 400" 400 "$(root -o /dev/null -w '%{http_code}' -X POST "$A/vs/admin/map/bad" -d '{"target":"kv/data/src/one","foo":1}')"
check "unknown field names it" "unknown field(s): foo" "$(root -X POST "$A/vs/admin/map/bad" -d '{"target":"kv/data/src/one","foo":1}' | jq -r '.errors[0]')"

# 2. mapping custom_metadata on data + metadata reads; upstream's not copied
post vs/admin/map/cm/one '{"target":"kv/data/src/one","custom_metadata":{"ironbark_images":"a,b"}}' >/dev/null
check "data read: mapping custom_metadata only" '{"ironbark_images":"a,b"}' "$(root "$A/vs/data/cm/one" | jq -c .data.metadata.custom_metadata)"
check "data read: value intact" FAKE-one "$(root "$A/vs/data/cm/one" | jq -r .data.data.v)"
check "metadata read: mapping custom_metadata only" '{"ironbark_images":"a,b"}' "$(root "$A/vs/metadata/cm/one" | jq -c .data.custom_metadata)"
check "data read: fetch has no voidstar key" null "$(root "$A/vs/data/cm/one" | jq -c '.data.voidstar')"

# 3. metadata-only mapping
post vs/admin/map/cm/marker '{"mode":"metadata","custom_metadata":{"vault_token":"true"}}' >/dev/null
check "metadata-only shape" '{"data":{},"cm":{"vault_token":"true"},"vs":{"mode":"metadata"}}' \
  "$(root "$A/vs/data/cm/marker" | jq -c '{data:.data.data,cm:.data.metadata.custom_metadata,vs:.data.voidstar}')"
check "metadata-only + target -> 400" 400 "$(root -o /dev/null -w '%{http_code}' -X POST "$A/vs/admin/map/cm/bad" -d '{"mode":"metadata","target":"kv/data/src/one"}')"
check "metadata-only listed" '["marker","one"]' "$(root -X LIST "$A/vs/metadata/cm/" | jq -c .data.keys)"

# 4. resolve: shape, zero leases, then a real read by a scoped token
post vs/admin/map/db/ro '{"mode":"resolve","target":"database/creds/ro","custom_metadata":{"k":"v"}}' >/dev/null
check "resolve shape" '{"data":{},"cm":{"k":"v"},"vs":{"mode":"resolve","path":"database/creds/ro"}}' \
  "$(root "$A/vs/data/db/ro" | jq -c '{data:.data.data,cm:.data.metadata.custom_metadata,vs:.data.voidstar}')"
check "resolve created no lease" 404 "$(code root -X LIST "$A/sys/leases/lookup/database/creds/ro/")"
root -X PUT "$A/sys/policies/acl/dbro" -d '{"policy":"path \"database/creds/ro\" {capabilities=[\"read\"]}"}' >/dev/null
DBTOK=$(post auth/token/create '{"policies":["dbro"],"ttl":"10m"}' | jq -r .auth.client_token)
RESOLVED=$(root "$A/vs/data/db/ro" | jq -r .data.voidstar.path)
LEASE=$(tok "$DBTOK" "$A/$RESOLVED" | jq -r .lease_id)
case "$LEASE" in database/creds/ro/*) pass "scoped token read of resolved path yields a lease" ;; *) fail "scoped token lease ('$LEASE')" ;; esac
check "one lease now exists" 1 "$(root -X LIST "$A/sys/leases/lookup/database/creds/ro/" | jq '.data.keys|length')"
tok "$DBTOK" -X POST "$A/auth/token/revoke-self" >/dev/null
sleep 1
check "lease owned by token: revoked with it" 0 "$(root -X LIST "$A/sys/leases/lookup/database/creds/ro/" | jq '.data.keys // [] | length')"

# 5. level metadata
post vs/admin/level/repo/acme/mono '{"custom_metadata":{"branches":"main,release/*"}}' >/dev/null
post vs/admin/level/repo/other/thing '{"custom_metadata":{"branches":"x"}}' >/dev/null
check "level GET 200" '{"branches":"main,release/*"}' "$(root "$A/vs/metadata/repo/acme/mono/" | jq -c .data.custom_metadata)"
check "level GET none -> 404" 404 "$(code root "$A/vs/metadata/nothing/here/")"
post vs/admin/map/repo/acme/mono/db '{"target":"kv/data/src/one"}' >/dev/null
check "LIST unchanged by level" '["db"]' "$(root -X LIST "$A/vs/metadata/repo/acme/mono/" | jq -c .data.keys)"
# OpenBao core refuses writes to a path ending in '/' (400) before the plugin sees it.
check "level write via metadata path refused" 400 "$(code root -X POST "$A/vs/metadata/repo/acme/mono/" -d '{"custom_metadata":{"a":"b"}}')"

# 6. discovery grant: templated policy on level docs (spec 6.3)
post sys/auth/userpass '{"type":"userpass"}' >/dev/null
ACC=$(root "$A/sys/auth" | jq -r '.data["userpass/"].accessor')
root -X PUT "$A/sys/policies/acl/discover" -d "{\"policy\":\"path \\\"vs/metadata/repo/{{identity.entity.aliases.$ACC.name}}/+/\\\" {capabilities=[\\\"read\\\"]}\"}" >/dev/null
post auth/userpass/users/acme '{"password":"pw","token_policies":"discover"}' >/dev/null
UTOK=$(curl -s -X POST "$A/auth/userpass/login/acme" -d '{"password":"pw"}' | jq -r .auth.client_token)
check "discovery: own level 200" 200 "$(code "$UTOK" "$A/vs/metadata/repo/acme/mono/")"
check "discovery: other org 403" 403 "$(code "$UTOK" "$A/vs/metadata/repo/other/thing/")"
check "discovery: one level deeper 403" 403 "$(code "$UTOK" "$A/vs/metadata/repo/acme/mono/sub/")"

# 7. Q12: %xx view names, wire-encoded
root -X POST "$A/vs/admin/map/grp%252fbeta" -d '{"target":"kv/data/src/one"}' >/dev/null
check "Q12: %xx data read 200" 200 "$(code root "$A/vs/data/grp%252fbeta")"
check "Q12: %xx metadata read 200" 200 "$(code root "$A/vs/metadata/grp%252fbeta")"
check "Q12: LIST shows grp%2fbeta" true "$(root -X LIST "$A/vs/metadata/" | jq '.data.keys|index("grp%2fbeta")!=null')"
check "Q12: decoded grp/beta is a different view (404)" 404 "$(code root "$A/vs/data/grp/beta")"

echo
if [ "$FAILS" -ne 0 ]; then echo "$FAILS check(s) FAILED"; exit 1; fi
echo "all checks passed"
