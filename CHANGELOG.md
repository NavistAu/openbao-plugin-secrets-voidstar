# Changelog

All notable changes to this project are documented here. Changes
affecting dereference semantics or the read-only contract are always
called out explicitly.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-08-19

### Added

- Initial public release: the voidstar secrets engine, serving
  read-only virtual views (`data/<view>`, `metadata/<view>`) that
  dereference server-side to targets in other mounts.
- Admin surface for mapping CRUD (`admin/map/<view>`), engine
  configuration (`admin/config`), and status (`admin/status`).
- Static-contract enforcement: a target read that returns a lease or a
  renewable response quarantines its mapping and fails the read.
