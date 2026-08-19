#!/usr/bin/env bash
# Task 9 bench gate: stand up a scratch OpenBao 2.5.5 server-mode
# container, init/unseal it, register+mount voidstar and the dynfake
# lease-emitting test plugin (mechanism verbatim
# down to the container/port cosmetics differing from the sibling's
# 18200), seed KV v2 + KV v1 targets, wire the loopback AppRole
# (contract: secret_id_num_uses=0, secret_id_ttl=0),
# write vs/admin/config, and write every bench mapping (kv2 adapter,
# raw adapter, #field select, structured whole-map, nested tree for
# list, the dynfake lease-drill mapping). Does NOT run the drive
# script — that's bench/run.sh. Scratch state (root token, unseal key,
# role/secret IDs) is never committed — it lives under bench/scratch/,
# gitignored.
set -euo pipefail
cd "$(dirname "$0")/.."

CONTAINER="${BENCH_CONTAINER:-vs-bench}"
PORT="${BENCH_PORT:-18202}"
SCRATCH_DIR="${BENCH_SCRATCH_DIR:-bench/scratch}"
mkdir -p "$SCRATCH_DIR"

docker rm -f "$CONTAINER" >/dev/null 2>&1 || true

# On any failure partway through setup, remove the half-configured
# container rather than leaving it dangling for the next run to trip
# over (task 9: "containers removed ... on script exit traps").
cleanup_on_failure() {
  local ec=$?
  if [ "$ec" -ne 0 ]; then
    echo "setup failed (exit $ec) — removing partial container ${CONTAINER}" >&2
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  fi
}
trap cleanup_on_failure EXIT

docker run -d --name "$CONTAINER" \
  --cap-add=IPC_LOCK \
  -p "${PORT}:8200" \
  -v "$(pwd)/bench/config/bao.hcl:/bao/config/bao.hcl:ro" \
  -v "$(pwd)/bench/dist:/bao/plugins:ro" \
  --entrypoint bao \
  ghcr.io/openbao/openbao:2.5.5 \
  server -config=/bao/config/bao.hcl

sleep 3

bao_() {
  docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" "$CONTAINER" bao "$@"
}

docker exec -e BAO_ADDR=http://127.0.0.1:8200 "$CONTAINER" \
  bao operator init -key-shares=1 -key-threshold=1 -format=json \
  > "$SCRATCH_DIR/init-output.json"

UNSEAL_KEY=$(python3 -c "import json; print(json.load(open('$SCRATCH_DIR/init-output.json'))['unseal_keys_b64'][0])")
ROOT_TOKEN=$(python3 -c "import json; print(json.load(open('$SCRATCH_DIR/init-output.json'))['root_token'])")
echo "$ROOT_TOKEN" > "$SCRATCH_DIR/root-token.txt"
chmod 600 "$SCRATCH_DIR/root-token.txt"

docker exec -e BAO_ADDR=http://127.0.0.1:8200 "$CONTAINER" bao operator unseal "$UNSEAL_KEY"

# --- plugin catalog registration (sha256-verified) + mount ---

SHA_VS=$(shasum -a 256 bench/dist/openbao-plugin-secrets-voidstar | awk '{print $1}')
SHA_DF=$(shasum -a 256 bench/dist/dynfake | awk '{print $1}')
echo "voidstar sha256: $SHA_VS"
echo "dynfake  sha256: $SHA_DF"

bao_ plugin register -sha256="$SHA_VS" -command=openbao-plugin-secrets-voidstar secret vs
bao_ secrets enable -path=vs vs
bao_ plugin register -sha256="$SHA_DF" -command=dynfake secret dynfake
bao_ secrets enable -path=dynfake dynfake

# --- target mounts + seed data ---

bao_ secrets enable -path=kv kv-v2
bao_ secrets enable -path=kv1 -version=1 kv

bao_ kv put kv/simple password=hunter2
bao_ kv put kv/structured username=bob password=hunter2 host=db.internal
bao_ kv put kv/tree/a val=a1
bao_ kv put kv/tree/b val=b1
bao_ kv put kv/tree/nested/c val=c1
bao_ write kv1/raw-item foo=bar

# --- loopback AppRole (contract: multi-use, non-expiring secret_id) ---

bao_ auth enable approle

cat > "$SCRATCH_DIR/vs-loopback-policy.hcl" <<'EOF'
path "kv/data/*" {
  capabilities = ["read"]
}
path "kv1/*" {
  capabilities = ["read"]
}
path "dynfake/leaky" {
  capabilities = ["read"]
}
path "sys/leases/revoke" {
  capabilities = ["update", "sudo"]
}
EOF
docker cp "$SCRATCH_DIR/vs-loopback-policy.hcl" "$CONTAINER:/tmp/vs-loopback-policy.hcl"
bao_ policy write vs-loopback-policy /tmp/vs-loopback-policy.hcl

bao_ write auth/approle/role/vs-loopback \
  token_policies="default,vs-loopback-policy" \
  secret_id_num_uses=0 \
  secret_id_ttl=0 \
  token_ttl=5m \
  token_max_ttl=15m

ROLE_ID=$(bao_ read -field=role_id auth/approle/role/vs-loopback/role-id)
SECRET_ID=$(bao_ write -f -field=secret_id auth/approle/role/vs-loopback/secret-id)
echo "$ROLE_ID" > "$SCRATCH_DIR/role-id.txt"
echo "$SECRET_ID" > "$SCRATCH_DIR/secret-id.txt"
chmod 600 "$SCRATCH_DIR/role-id.txt" "$SCRATCH_DIR/secret-id.txt"

# --- voidstar admin/config ---

bao_ write vs/admin/config \
  role_id="$ROLE_ID" \
  secret_id="$SECRET_ID" \
  api_addr="http://127.0.0.1:8200"

# --- mappings: kv2 adapter, structured whole-map, #field select,
#     raw adapter, nested tree for list, dynfake lease-drill target ---

bao_ write vs/admin/map/team/simple target="kv/data/simple"
bao_ write vs/admin/map/team/structured target="kv/data/structured"
bao_ write vs/admin/map/team/structured_field target="kv/data/structured#password"
bao_ write vs/admin/map/team/raw target="kv1/raw-item" adapter="raw"
bao_ write vs/admin/map/team/tree/a target="kv/data/tree/a"
bao_ write vs/admin/map/team/tree/b target="kv/data/tree/b"
bao_ write vs/admin/map/team/tree/nested/c target="kv/data/tree/nested/c"
bao_ write vs/admin/map/team/dynfake target="dynfake/leaky" adapter="raw"

echo "$PORT" > "$SCRATCH_DIR/port.txt"
echo "vs-bench ready: mounted at vs/, unsealed, plugins registered, config+mappings written."
echo "root token: $SCRATCH_DIR/root-token.txt"
echo "next: bench/run.sh to drive the assertions."
