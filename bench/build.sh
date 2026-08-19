#!/usr/bin/env bash
# Task 9 bench gate: cross-compile the voidstar plugin and the
# throwaway dynfake lease-emitting test plugin for the bench container
# (linux/arm64 — matches the arm64 Mac host's native docker platform;
# swap GOARCH=amd64 on an amd64 host). Mirrors the sibling's
# bench/build.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

mkdir -p bench/dist

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -o bench/dist/openbao-plugin-secrets-voidstar \
  ./cmd/openbao-plugin-secrets-voidstar

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -o bench/dist/dynfake \
  ./bench/dynfake

echo "built: bench/dist/openbao-plugin-secrets-voidstar"
shasum -a 256 bench/dist/openbao-plugin-secrets-voidstar
echo "built: bench/dist/dynfake"
shasum -a 256 bench/dist/dynfake
