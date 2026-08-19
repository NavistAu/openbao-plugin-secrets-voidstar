# Contributing

Thank you for your interest in improving openbao-plugin-secrets-voidstar.
This document covers the build and test workflow, the branch model, the
documentation style rule, and the bar for adding new configuration options.

## Quick start

Fork the repository, clone your fork, branch off `develop`, make your
change, run `gofmt`, `go vet`, and `go test` (see Toolchain and Tests
below), then open a pull request against `develop`.

## Toolchain

This project uses [mise](https://mise.jdx.dev/) to pin the Go toolchain
version (`mise.toml` specifies Go 1.25). Install mise, then run commands
through it so you build and test with the pinned version:

```sh
mise exec -- go build ./...
mise exec -- go test ./...
```

Format code with `gofmt` before submitting:

```sh
gofmt -l .
```

`gofmt -l` should print no files. Run `gofmt -w .` to fix formatting in
place.

## Branch model

- `develop` is the default branch. Feature branches are cut from `develop`
  and target `develop` in their pull request.
- `main` only ever receives merges from `develop`. That merge is the only
  path to `main`.
- Every push to `main` triggers a release. Do not open pull requests
  directly against `main`.

## Documentation style

The README deliberately mixes two registers of language:

- **Controlled language** (STE100-style: short sentences, one instruction
  per sentence, restricted vocabulary) is used in the Terms, Requirements,
  Installation, and Configuration reference sections, in the Errors and
  logs tables, and in Troubleshooting. These sections are read under time
  pressure while something is broken or being configured, so they favor
  precision and scanability over style.
- **Ordinary prose** is used everywhere else (introduction, value
  proposition, how it works, design constraints, and similar explanatory
  sections).

This split is intentional. When editing the README, keep new or changed
text in a controlled-language section terse and single-instruction-per-
sentence; keep prose elsewhere readable and connected. Do not "smooth out"
the controlled sections into flowing prose, and do not tighten the prose
sections into clipped instructions.

## Adding configuration options

This plugin keeps a deliberately small configuration surface: the mapping
fields (`target`, `adapter`) and the engine config fields (`approle_mount`,
`role_id`, `secret_id`, `api_addr`, `expose_targets`,
`target_mount_allowlist`). Before proposing a new configuration option,
show a concrete need that these existing options cannot express — not a
hypothetical or a convenience. Explain in the pull request what you are
trying to do, why the current options (see the README's Configuration
reference) cannot do it, and why a new option is the right fix rather than
a change to existing behavior.

Pull requests that add configuration surface without that case will be
declined.

## Changelog

`CHANGELOG.md` always calls out changes affecting dereference semantics
or the read-only contract explicitly. If your change touches either, add
an entry under `## [Unreleased]` (create that section if it does not
exist) describing the change.

## Tests

Run the full build, vet, and test suite before opening a pull request:

```sh
mise exec -- go build ./...
mise exec -- go vet ./...
mise exec -- go test ./...
```

All packages should report `ok`. CI additionally runs the test suite with
`go test -race ./...`; if you can, run it locally too before opening a
pull request that touches concurrent code (`backend/loopback.go`,
`backend/dereference.go`).
