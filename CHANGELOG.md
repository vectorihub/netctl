# Changelog

All notable changes to netctl are recorded here, newest release first. The format follows
[Keep a Changelog](https://keepachangelog.com). Numbering and upgrade rules are in
[docs/versioning.md](docs/versioning.md).

netctl stays at `v0.x` until the Phase 5 gate. Until then a minor release may contain breaking
changes, and each one is listed under **Breaking Changes** for that release.

## [Unreleased]

# Release 2026-11-19 (v0.1.0)

## Announcements
This is the first release of netctl, the Go library that plans and validates Beaver's overlay network:
topology, addressing, routing and per-node configuration.

Release R1 lays the foundation. It ships the repository and build tooling, the shared vocabulary every
later package depends on, and the address maths. There is no planner yet. Packages such as `ipam`,
`topology` and `route` exist as placeholders, and their APIs arrive in later releases.

## Core Module Highlights
* Go module `github.com/vectorihub/netctl`, released at `v0.1.0`.
* Requires Go `1.25` or newer.
* All IP handling uses `net/netip` types; see [ADR 0002](docs/adr/0002-netip-everywhere.md).
* Repository layout and module boundaries are described in [ADR 0001](docs/adr/0001-own-repo-and-module-layout.md).

## Package Highlights

#### `types`
* Identifiers: `SiteID`, `NodeID` and `LinkID`, with `ValidID` for validation and the order-independent
  constructor `LinkIDOf`, so a link between A and B has one ID regardless of direction.
* Enumerations: `Role` (`hub`, `spoke`) and `LinkKind` (`hub-spoke`, `direct`), each with `Parse*` and `String`.
* `Clock` and `FixedClock`. Library code never reads the real time, which keeps every result reproducible.
* Coded errors: `Code`, `Error`, `Errf`, `List` and `HasCode`, with nine stable codes:
  `INVALID_PREFIX`, `OVERLAP`, `POOL_EXHAUSTED`, `NOT_FOUND`, `RESERVED`, `OUT_OF_SUPERNET`,
  `INVALID_TOPOLOGY`, `NO_ROUTE` and `UNSUPPORTED_SCHEMA`. Callers match on the code, not the message text.

#### `cidr`
* Inspection: `Canonical`, `Contains`, `Overlaps`, `Size` and `Compare`.
* Subdivision: `Children` (lazy), `Split`, `Subtract` and `Usable`.
* Covered by unit, property-based and fuzz tests.

#### `arch`
* An architecture guard that runs as part of the test suite. The build fails if any package can run
  programs, use HTTP, open a database or configure the operating system, or if a `package main` appears.
  netctl computes plans; applying them is the job of other modules.

## Tooling Highlights
* `make check lint test race arch fuzz repro` targets for local and CI use.
* `make lint` additionally forbids reading the clock, files or environment from library code.
* GitHub Actions workflow running on Ubuntu and macOS, with a Windows build check.

## Documentation
* [ADR 0001](docs/adr/0001-own-repo-and-module-layout.md): own repository and module layout.
* [ADR 0002](docs/adr/0002-netip-everywhere.md): `net/netip` everywhere.
* [Versioning and release policy](docs/versioning.md).
* [Apache License 2.0](LICENSE) and [NOTICE](NOTICE).

## Breaking Changes
None. This is the first release.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Not yet defined |
| `beaver.topology/v1alpha1` | Not yet defined |
| `beaver.ipam/v1` | Not yet defined |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.1.0
go mod tidy
```
