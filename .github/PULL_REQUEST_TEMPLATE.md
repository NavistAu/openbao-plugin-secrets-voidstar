## Target branch

This pull request targets `develop`. Pull requests opened against `main`
will be redirected — `main` only receives merges from `develop`.

## What

<!-- What does this change do? -->

## Why

<!-- Why is this change needed? -->

## Tests run

<!-- Commands run and their results, e.g.:
mise exec -- go build ./...
mise exec -- go vet ./...
mise exec -- go test ./...
gofmt -l . -->

## Checklist

- [ ] Targets `develop`, not `main`.
- [ ] `mise exec -- go build ./...`, `mise exec -- go vet ./...`, and
      `mise exec -- go test ./...` pass.
- [ ] `gofmt -l .` reports no files.
- [ ] If this adds a new configuration option, it meets the bar in
      [CONTRIBUTING.md](../CONTRIBUTING.md#adding-configuration-options): a
      concrete need that existing options cannot express, stated in this
      description.
- [ ] If this changes dereference semantics or the read-only contract,
      `CHANGELOG.md` calls it out per
      [CONTRIBUTING.md](../CONTRIBUTING.md#changelog).
