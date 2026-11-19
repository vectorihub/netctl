# Changelog

All notable changes to netctl are recorded here, newest release first. The format follows
[Keep a Changelog](https://keepachangelog.com). Numbering and upgrade rules are in
[docs/versioning.md](docs/versioning.md).

netctl stays at `v0.x` until the Phase 5 gate. Until then a minor release may contain breaking
changes, and each one is listed under **Breaking Changes** for that release.

## [Unreleased]

# Release 2026-11-19 (v0.2.0)

## Announcements
Release R2 adds `ipam`, the address book. It decides which address block belongs to which site, machine,
link or piece of infrastructure, and guarantees that no two owners ever overlap. It reads and writes no
files and never looks at the real clock; the caller passes in the time and the saved state.

## Core Module Highlights
* New package `ipam`, with the snapshot format `beaver.ipam/v1`.
* The default plan carves the supernet `10.42.0.0/16` into pools for sites, machine overlay addresses,
  transit links, infrastructure and growth. See [ADR 0003](docs/adr/0003-overlay-address-block.md).
* Test files now live in each package's `tests/` folder and exercise the exported API only.

## Package Highlights

#### `ipam`
* Allocation: `New`, `Allocate`, `AllocateSpecific`, `Reserve`, `Release`, with `WithOverride` and `WithNote`.
  A reserved pool refuses an allocation unless a reason is given. A prefix with host bits set is cleaned up
  and a warning is recorded (`Warnings`).
* Owners: `Site`, `Node`, `Link` and `Infra`, written as one string such as `node:builder-1`.
* Quarantine: a released block is not handed out again for a set time (default 24 hours), so a stale route
  cannot point at a new owner. See [ADR 0004](docs/adr/0004-quarantine-period.md).
* Lookup: `Lookup`, `OwnerOf` ("who owns 10.42.240.2?"), `ByOwner`, and `AllocateTransit`, which numbers a
  direct link with a `/30` and gives the lower address to the node with the smaller ID.
* Accounting: `Accounting` returns allocated, reserved, quarantined and free space, the free blocks and the
  usage of each pool, and checks that the parts add up to the whole.
* Persistence: `Marshal`, `Unmarshal` and `Snapshot` give byte-stable output (equal states give equal bytes).
  Loading is strict about unknown fields, bad owners and trailing data.
* Performance: 2,000 overlay allocations in about 130 ms and accounting at 5,000 allocations in about 17 ms.
* Covered by unit tests, property tests (random programs of 5,000 operations, shrunk to the shortest
  failing program) and a fuzz test for snapshots.

## Tooling Highlights
* `make bench` runs the IPAM performance budgets; a benchmark fails when its best run is over budget.
* `make fuzz` covers `FuzzSnapshot` as well as the `cidr` targets.

## Documentation
* [ADR 0003](docs/adr/0003-overlay-address-block.md): machines get a dedicated overlay address block.
* [ADR 0004](docs/adr/0004-quarantine-period.md): the quarantine period.

## Breaking Changes
None to the public API. Tests moved from beside the code into `tests/` folders.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Not yet defined |
| `beaver.topology/v1alpha1` | Not yet defined |
| `beaver.ipam/v1` | Defined in this release |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.2.0
go mod tidy
```

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
