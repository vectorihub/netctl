# Changelog

All notable changes to netctl are recorded here, newest release first. The format follows
[Keep a Changelog](https://keepachangelog.com). Numbering and upgrade rules are in
[docs/versioning.md](docs/versioning.md).

netctl stays at `v0.x` until the Phase 5 gate. Until then a minor release may contain breaking
changes, and each one is listed under **Breaking Changes** for that release.

## [Unreleased]

## Package Highlights

#### `route` (new)
* The vocabulary of routing: `Route` (prefix, next hop, link, metric, source, path, active), `Source`
  (`connected`, `computed`, `backup`, `static`), `Table` (one node's routes) and `Tables` (one table per node).
  A route's source is never left unset: the zero value is `unknown` and refuses to be written as JSON.
* `Compare` is a total order on routes, and `Table.Sort` uses it, so the same routes always sort the same way.
* `Table.FIB` returns the routes in use; `Table.Routes` also holds backups waiting in reserve.
* `Table.Lookup` is longest-prefix matching over active routes: the most specific route wins, then the lower
  metric, and the answer never depends on the order of the table. An address with no matching route returns
  false; there is no hidden default.
* `SPF` finds the cheapest path from one node to every other over the links that are up. A spoke never
  forwards for others, so two spokes with a direct link still reach a third through the hub. Equal-cost paths
  are settled the same way on every run: fewer hops, then the smaller next hop, then the smaller link, then
  the smaller list of nodes.
* `Compute` builds the table of every node: connected routes for what the node holds, an active computed
  route for what every other node announces, and one inactive backup per prefix found by searching again
  without the primary's first link. The result does not depend on the order of the input.
* `Collect` reads who announces what from the address plan (`ipam`): site blocks, overlay addresses, and the
  transit `/30` of each direct link, which only its two ends hold.
* Nothing is dropped silently. A pair of nodes with no path is returned in `Result.Unreachable` with a reason
  (no healthy path, or the only path crosses a spoke). A static `Override` is accepted only if it points at a
  real neighbour, is always flagged as `static`, and otherwise comes back in `Result.Rejected` with an
  explanation. Broken input, such as one block announced by two nodes, is refused with a coded error.
* `route` defines its own small `Graph` interface and does not import `topology`; the planner will adapt
  `topology.Graph` to it.
* `Explain` says which route a node uses for an address and why: the match, the route in use, the backup that
  would take over, a flagged static override, and link health if you supply it. `Explanation.String` is stable
  text suitable for a golden file.
* `Diff` compares two sets of tables and returns, per node, the routes added, removed and changed. A backup
  that takes over from a failed primary shows as a change with `Activated` true. Identical tables give an empty
  diff.
* Shortest paths run on integers with a typed heap: 500 nodes, one hub and 200 direct links compute in about
  half a second on a 2016 laptop (the target is under one second), and 2,000 nodes with four hubs in about
  eleven seconds. The 2,000-node target in the plan covers validation only, which arrives with the validator.
* `Compare` orders routes by the canonical block, so a route written with host bits set (`10.42.16.5/20`) sorts
  with `10.42.16.0/20`; the spelling only breaks the very last tie, so the order stays total.
* `Compute` refuses a graph whose link costs add up to more than `MaxTotalCost` (2,147,483,647) with
  `INVALID_TOPOLOGY`, so no path cost can wrap round to a negative number on any platform; `SPF`, which returns no
  error, stops a cost at the largest int instead of wrapping.
* Two overrides for one block spelled differently (`10.9.0.0/24`, `10.9.0.5/24`) are decided the same way in any
  input order. `Collect` with no registry returns `INVALID_VALUE`.
* The package is now in the README and the root documentation.

#### `types`
* New error code `INVALID_VALUE`, returned (and found with `errors.Is`, also through `encoding/json`) when a route
  source, hop result, verdict or flow family is unset or not a known name, and when `Flows` is given no input.

#### `sim` (new, not yet complete)
* The vocabulary of simulation: `Packet`, `Hop`, `Trace`, `Flow`, `FlowResult`, `Report`, and the verdicts
  (`OK`, `OK_ASYMMETRIC_ALLOWED`, `ASYMMETRIC`, `NO_RETURN`, `BLACK_HOLE`, `LOOP`, `LINK_DOWN`,
  `WRONG_NEIGHBOUR`, `TTL_EXCEEDED`, `UNEXPECTED_DELIVERY`, `NO_HEALTHY_PATH`, `UNREACHABLE_EXPECTED`), each
  saying whether it fails the gate. The zero verdict fails the gate, so an unjudged flow cannot pass.
* `Net.Send` pushes a packet through the tables hop by hop. A loop is detected exactly, because forwarding is
  deterministic, and long before the hop limit; the hop limit is a second, independent net. Every packet ends
  with a result, whatever the tables hold.
* `Net.Run` sends a flow and its reply, and `Judge` and `Symmetric` turn the two traces into one verdict.
  Symmetric means the exact reverse in nodes and in links. A missing return, an asymmetric path, a down link and
  a route to a node that is not a neighbour each get their own verdict.
* `Flows` derives everything worth simulating from a topology and its address plan: overlay flows between
  every pair of nodes, site flows between every pair of advertised sites, one flow per deny intent, and one
  negative flow per node. The order is fixed.
* A route more specific than anything a node holds, such as a static override for one address of its own
  block, is followed instead of the packet being delivered there, so the override is tested.
* A site whose block is a single `/32` is tested at that address, not the next one. A supernet that leaves no
  address outside it for the negative flows is refused with `INVALID_VALUE` instead of testing nothing.
* `NewReport` orders results that share a label by everything else in them, so the order never depends on the
  input's.
* `NewNet` builds the network under test from a `route.Graph` and the prefixes each node holds.
* `RunScenario` and `RunScenarios` run a failure scenario (`links_down`, `nodes_down`, `expect`): the failures
  are laid over the graph, every route is recomputed with the real `route.Compute` (one call per scenario, and
  a test fails the build if `sim` ever calls it anywhere else or computes routes itself), and every flow is
  simulated over the new tables. A flow that starts or ends at a failed node is expected to be undeliverable
  (`UNREACHABLE_EXPECTED`); live nodes that the failures cut off from each other get `NO_HEALTHY_PATH`, which
  passes the gate and is listed. `Expect` turns a scenario into an assertion: a wrong verdict, or an expectation
  that matches no flow, fails the scenario and names it. A scenario that names a link or node the network does
  not have is refused (`INVALID_SCENARIO`) instead of quietly testing the healthy network.
* `ParseScenarios` reads scenarios from YAML, strictly: unknown keys, verdicts or families, a missing name or
  verdict, a repeated name and a second document are refused. It has a fuzz target (`make fuzz`).
* `CorruptTable` returns a copy of the tables with one node's routes for one prefix deleted, retargeted or
  planted, for tests that plant a black hole, a loop, a one-sided link or a wrong neighbour and check the
  simulator catches it. The input is not changed.
* `Report.Text` and `ScenarioResult.Text` write the report for people: a header, the verdict counts (the zeros
  that matter always shown), PASS or FAIL, and each failing flow's forward and return journey link by link.
  `Report.JSON` and `ScenarioResult.JSON` write JSON with a fixed key order and sorted flows, byte-identical for
  the same run (golden files in `testdata/golden`).
* `NewNet` indexes each table once, so the all-pairs overlay flows of 500 nodes (249,500 flows, each sent and
  answered) take about two seconds against a budget of ten. The index is a snapshot: change the tables, build
  a new `Net`.

#### `route`
* `Table.Index` returns a read-only snapshot of a table's active routes for fast longest-prefix lookup, and
  `Index.Lookup` answers exactly what `Table.Lookup` does (a property test checks it over 10,000 random tables,
  including IPv6, unmasked prefixes, ties and awkward addresses) a hundred times faster on a 2,000-route table.

#### `health` (new, not yet complete)
* `Signal` is one piece of evidence about one direction of one link: a probe, a WireGuard handshake, the state of
  the interface, or an operator instruction. `Config` holds the thresholds and times (three failures to go
  down, five good signals to recover, silence for 15 seconds is a failure, a 60 second hold-down) and refuses
  settings that make no sense, including a stale time that is not longer than the probe interval.
* `Direction` is the state machine of one end of a link: Unknown, Healthy, Degraded, Down, Recovering. A single
  failure only degrades, so routes do not move for a lost probe. Successes during the hold-down are ignored and
  do not add up to a recovery the moment it ends. A failure while recovering restarts the hold-down.
* `Link` and `Evaluator` combine the two directions: a link is usable only when healthy in both, so both ends
  fail over together and paths stay symmetric. A required (direct) link is unused until proven; an assumed
  (hub-spoke) link is used until it fails. An interface signal can take a direction down at once but can never
  raise health: ten minutes of interface-up signals leave a new link Unknown.
* Silence counts as failure after `stale_after`, judged from the moment the link went quiet, not from when
  someone looked, and judged between the signals of a batch as well as at its end. Nothing reads the clock:
  every method is told the time.
* `Evaluator.Apply` returns every change as a `Decision` with a reason, and every signal it could not use as a
  `Skipped` with a reason. `UnusableLinks` is the list to mark down in the routing graph or hand to a
  simulation scenario.
* Not yet built: flap pinning and operator drain (admin signals are skipped with a reason), saving state, and
  recomputing routes on a change.

#### `types`
* New error codes `INVALID_SCENARIO` and `INVALID_CONFIG`.

# Release 2026-11-19 (v0.3.1)

## Announcements
A documentation release. The API is unchanged. The package documentation on
[pkg.go.dev](https://pkg.go.dev/github.com/vectorihub/netctl) now explains what netctl is and how to use each
package, with runnable examples.

## Documentation
* The root package documentation explains what netctl is, its guarantees, and how the packages fit together.
* `ipam` has a full package overview and runnable examples for `New`, `Allocate`, `AllocateSpecific`,
  `AllocateTransit`, `Reserve`, `Release`, `Lookup`, `OwnerOf`, `ByOwner`, `Accounting`, `Marshal` and `Owner`.
* `topology` has runnable examples for `Load`, `Check`, `ParseSelector`, `BuildGraph` and `Hash`.
* The existing `types` and `cidr` examples now appear on pkg.go.dev. They moved into `example_test.go` files
  beside the code.
* New README, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md` and `.github/CODEOWNERS`.

## Removed
* Empty placeholder packages that had no API: `config`, `failover`, `health`, `identity`, `plan`, `policy`,
  `route`, `sim`, `validate`, and the `test/e2e/phase1` suite with its `make e2e-phase1` target. Nothing could
  import anything from them. Each package returns when it is implemented.

# Release 2026-11-19 (v0.3.0)

## Announcements
Release R3 adds `topology`, the one description of what the network is made of: sites, machines, links,
and who must be allowed to reach whom. It reads YAML from bytes (never from a file), checks it against
rules T1 to T11, and reports every problem at once with the file name and line.

## Core Module Highlights
* New package `topology`, with the input format `beaver.topology/v1alpha1`.
* New runtime dependency: `gopkg.in/yaml.v3`, used only by the loader. `topology/types.go` imports no YAML,
  so a `Topology` can be built in Go code (for example from database rows) and is treated the same.

## Package Highlights

#### `topology`
* Types: `Topology`, `Site`, `Node`, `Link`, `Intent`, `Selector`, with `NATMode` and `HealthMode`. A zero
  value always means "not said", so a missing field is never mistaken for a choice.
* Strict loading: `Decode` refuses unknown fields (naming the line and suggesting the likely spelling,
  `rol` to `role`), repeated fields, wrong kinds of value and merge keys. A 50 MB file or a "billion laughs"
  file is refused at once.
* Defaults and order: `Normalize` fills in cost and health, stores each link with its ends in order, derives
  the link ID, and sorts sites, nodes and links, so the same network always looks the same. Intents keep
  their written order.
* Rules: `Check` and `Load` report rules T1 to T10 as a `Report` of `Finding`s, each naming its rule. T8
  (a direct link between two nodes behind NAT) is a warning, not an error. T11 (unknown fields) is `Decode`'s.
* Selectors: `*`, `node:`, `site:` and `label:k=v`, with `Selector.Resolve` returning sorted node IDs.
* Adjacency import: a node may list `peers` instead of the file having `links`. `ImportPeers` turns agreeing
  pairs into links and refuses a pair that only one side lists.
* Graph: `BuildGraph` gives two directed edges per link, sorted by `(To, Link)`, with a health view.
* Hash: `Hash` is a SHA-256 of the canonical form. Reordering, comments, whitespace and the links-or-peers
  choice do not change it; any change to the network does.
* Performance: loading 500 nodes and about 1,500 links takes about 33 ms.
* Covered by unit tests, mutation checks, `FuzzDecode` and `FuzzLoad` (120 s).

## Tooling Highlights
* `make fuzz` covers `FuzzDecode` and `FuzzLoad`; `make bench` includes the topology load budget.

## Documentation
* Test fixtures for each rule live in `testdata/topologies/`.

## Breaking Changes
None.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Not yet defined |
| `beaver.topology/v1alpha1` | Defined in this release |
| `beaver.ipam/v1` | Unchanged |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.3.0
go mod tidy
```

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
