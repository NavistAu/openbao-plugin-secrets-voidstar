#!/usr/bin/env bash
# Bench gate: drive the assertions (bench/drive) against a
# vs-bench server bench/setup.sh already stood up and configured.
# Builds bench/drive for the host (not cross-compiled — it talks to
# the container over the published port, it doesn't run inside it).
set -euo pipefail
cd "$(dirname "$0")/.."

SCRATCH_DIR="${BENCH_SCRATCH_DIR:-bench/scratch}"
PORT=$(cat "$SCRATCH_DIR/port.txt")
ROOT_TOKEN=$(cat "$SCRATCH_DIR/root-token.txt")

go build -o "$SCRATCH_DIR/drive" ./bench/drive

BAO_ADDR="http://127.0.0.1:${PORT}" BAO_TOKEN="$ROOT_TOKEN" "$SCRATCH_DIR/drive"
