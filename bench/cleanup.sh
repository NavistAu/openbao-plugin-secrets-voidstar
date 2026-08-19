#!/usr/bin/env bash
# Bench gate cleanup. Removes the scratch vs-bench container.
# Safe to run multiple times.
set -uo pipefail
cd "$(dirname "$0")/.."

CONTAINER="${BENCH_CONTAINER:-vs-bench}"

echo "== removing ${CONTAINER} container =="
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true

echo "cleanup done"
