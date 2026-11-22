# Changelog

All notable changes to netctl are recorded here, newest release first. The format follows
[Keep a Changelog](https://keepachangelog.com). Numbering and upgrade rules are in
[docs/versioning.md](docs/versioning.md).

netctl stays at `v0.x` until the Phase 5 gate. Until then a minor release may contain breaking
changes, and each one is listed under **Breaking Changes** for that release.

## [Unreleased]

# Release 2026-11-22 (v0.10.0)

## Announcements
The Phase 1 end-to-end suite and its gate tooling. This release changes the library in two small ways, both
needed to run the suite end to end; everything else is test code.

## Package Highlights

#### `topology`
* A site can take its block from another pool on purpose: `override: {pool: growth, reason: "approved expansion"}`
  (`Site.Override`). The reason is required and is kept in the allocation's note.

#### `validate`
* V-CFG-005 also scans node labels, so a key typed into the input is refused before it reaches a config. The
  finding names the label and never repeats the value.

#### `plan`
* Honours the site override when it allocates.

## Documentation
* `docs/quickstart.md`: planning, re-planning with state, and reading a refusal.
* `make e2e-phase1`, `e2e-phase1-fast`, `e2e-phase1-chaos` and `cover` (coverage gate: 85% overall, 90% for
  ipam, route, sim and validate).

## Breaking Changes
None.

## Schema Versions
Unchanged.

## Upgrading
Nothing to do.

# Release 2026-11-21 (v0.9.0)

## Announcements
Release R9 makes netctl a package other programs can depend on. The root package is now the one obvious entry point:
`netctl.Plan` takes the network and its state and returns the plan, and `Result.Render` gives the bytes of every
output file without doing any I/O, so every consumer writes the same bytes. The surface is a **public API
candidate**: it is recorded in `docs/api-surface.txt`, and it may still change before `v1.0.0`.

## Package Highlights

#### `netctl` (root)
* `Plan`, `Diff` and the types `Inputs`, `Result`, `Change`, `Decision` and `ConfigDiff` (aliases of the `plan` and
  `config` types), with runnable examples. `go doc github.com/vectorihub/netctl` reads as a manual.

#### `plan`
* `Result.Render` returns `plan.json`, `ipam.json`, `nodes/<node>.json`, `routes.txt`, `validation.json` and
  `simulation.json`, byte for byte the same however the file was ordered. `Result.Explain`, `ExplainText`,
  `ExplainJSON`, `SimulationText`, `SimulationJSON`, `RoutesText`, `plan.Diff` and `plan.DiffJSON` are the pure
  renderers a command line prints.
* The failover guard runs inside the planner: pass `Inputs.Signals` and/or `Inputs.Failover` (the state of the last
  plan) and the routes are computed by `failover.Evaluate`. A failover that would break a flow that worked is refused,
  the routes in force stay and a `FailoverRejected` decision says why. `Result.Failover` holds the state to save.
* A machine that leaves while another joins in the same plan is two machines: the old address stays quarantined, the
  new name gets its own, and an `AddressNotCarried` decision says so.

## Documentation
* `docs/api-surface.txt` records every exported name, field and method, and `arch/tests` fails when it is out of date
  (`go test ./arch/tests -update-surface` rewrites it; review the diff on every tag).
* `testdata/consumer` is a separate Go module that builds a network in code against the working tree and calls
  `netctl.Plan` (`make consumer`, also run in CI). It also checks that depending on netctl pulls in no forbidden package.
* `make apidiff` reports the API change since the latest tag, and CI prints it.

## Breaking Changes
None. Everything in this release is new API or additive.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Unchanged |
| `beaver.topology/v1alpha1` | Unchanged |
| `beaver.ipam/v1` | Unchanged |
| `beaver.failover/v1` | Unchanged |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.9.0
go mod tidy
```

# Release 2026-11-21 (v0.8.0)

## Announcements
Release R8 adds `validate`, the last gate before a configuration can leave the controller, and the first version of
`plan`, the one function that runs the whole pipeline. `validate` checks the whole plan, from the address plan and the
topology to the routes and the generated configs, with a catalogue of 32 rules that each have a stable ID, and refuses
anything that would not work. `plan` takes a network description and its state and returns the addresses, the routes, a
config for every node and the validator's verdict, in a fixed order and the same bytes however the input is ordered. The
shape of `plan` is a candidate and may change before the public API is declared (R9).

## Package Highlights

#### `validate` (new)
* `Validate` runs the catalogue over a plan and returns findings sorted by level, rule, subject, message, hint and
  detail, so the same plan always gives the same report. The rules:
  * addressing (`V-ADDR-001` to `008`): no overlap, the accounting invariant (the snapshot must load, pools included),
    one overlay address per node, a transit /30 whose hosts are the link's two ends, nothing in a reserved pool without
    an override reason, pools over 80% used, forbidden ranges (0/8, 127/8, 169.254/16, 224/4), and a supernet that
    collides with well-known cloud or home defaults;
  * topology and links (`V-TOPO-001`, `002`, `003`, `004`, `020`, `V-LINK-001` to `004`): the loader's own rules again,
    the healthy links connect every node to a hub (without passing through a spoke), a spoke with one link, a direct link
    between two nodes behind NAT, more than one hub, no one-sided peers, both ends agree on kind and transit addresses, a
    required-health link is probed at both ends, a direct link dearer than the hub path;
  * routes (`V-ROUTE-001` to `006`): one active route per prefix, a next hop that is a neighbour over a healthy link, no
    path through a node twice, no spoke forwarding between two others, static overrides always shown, a more specific
    route shadowing another;
  * configs (`V-CFG-001` to `006`): every config has its hash and validates against the JSON Schema, every active route's
    next hop is a peer and its prefix is in that peer's allowed prefixes (compared as the block each stands for), no
    prefix in two peers' lists, no secret material, and the return-path rule;
  * simulation (`V-SIM-001` to `003`): every flow is delivered over the tables as they are (with the packet's journey
    attached to a failure), every declared scenario's expectations hold and none of its flows fails the gate, and the
    single-failure sweep, which recomputes the routes once for every link and every hub and warns where one failure
    leaves a node with no way to any other. `Options.SkipSweep` leaves it out on a large plan.
* A warning can be suppressed in the topology file (`validation.suppress`) with a mandatory reason. An error can never be
  suppressed; the attempt, a missing reason, an unknown rule and a suppression that matched nothing each give a finding
  of their own (`V-SUP-001` to `004`), and what was accepted stays on the record.
* `Result.Text` and `Result.JSON` (stable bytes), `CatalogueText`, and `Result.Err`, which returns the new error code
  `VALIDATION_FAILED` when there is any error. A test fails for any rule that has no test planting its fault.
* On the clean three-site plan there are no errors and one warning, `V-TOPO-004`; before the direct link is proven the
  two hub links also get `V-SIM-003`.

#### `plan` (new)
* `Plan` runs the pipeline in a fixed order: load the address plan, release what is gone (nodes and links before sites),
  allocate sites, overlay addresses and transit /30s in sorted order, carry each link's health over by ID (taking whether
  it must be proven from the topology), compute the routes on the usable links, generate the configs, validate, and
  guard. Fifty shuffled plans are byte-identical, a second plan from the first one's address plan changes nothing, and
  with the direct link proven or failed the configs equal the reviewed golden files.
* A failure while allocating reports every problem at once and fails the plan before any config exists. A plan the
  validator refuses has no configs and returns `VALIDATION_FAILED`. If a plan was in force it stays in force, address
  plan included, and the result says `FailoverRejected`, however many times in a row. `Plan` changes none of its inputs.
* Not in yet: pins for addresses, and calling `failover.Evaluate` (the guard is a plain "fails validation while the plan
  in force passed").

#### `sim`, `topology`, `types`
* `sim.Degrade` (a graph with links and nodes failed) and `sim.Simulate` (flows over given tables, so a fault planted in
  them is seen); `RunScenario` and `failover` now share them.
* `topology` reads an optional `validation.suppress` section; it is not part of the hash.
* `types.ErrValidationFailed`.

#### `config`
* The determinism proof is finished: every config survives a write and a read byte for byte, and the configs from the
  tables `failover.Evaluate` installs equal those from tables computed directly. ADR 0005 records the NodeConfig contract
  and the schema versioning policy.

## Documentation
* Every exported identifier in `validate` and `plan` has a doc comment with an `Example:` block, and every exported
  function has a runnable example on pkg.go.dev.
* `README.md` lists `validate` and `plan`. `docs/adr/0005-nodeconfig-contract.md` is new.

## Breaking Changes
None. Everything in this release is new API or additive.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Unchanged |
| `beaver.topology/v1alpha1` | Unchanged; gains an optional `validation` section |
| `beaver.ipam/v1` | Unchanged |
| `beaver.failover/v1` | Unchanged |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.8.0
go mod tidy
```

# Release 2026-11-21 (v0.7.0)

## Announcements
Release R7 adds `config`, the line between the control plane and the data plane. For every node it writes one
document that says what that node's network should look like: its overlay address, its peers and the prefixes each
may carry, its routes, and what to probe. It never says how. There are no interface names, commands, sysctls,
table numbers or paths, and no secrets. The same document can be applied by a Linux agent today and by another
kind of agent later. When a direct link fails, the next config moves the far node's prefixes from the direct peer
to the hub, so WireGuard follows the new route.

## Package Highlights

#### `config` (new)
* `NodeConfig` is the desired state of one node, with a strict JSON Schema published in
  `config/schema/v1alpha1.json` (and returned by `SchemaJSON()`). A peer is named, and its public key is
  referred to by identity; a private key can never appear.
* `Generate` builds every node's config from one plan, and `FromPlan` collects its inputs from a topology and the
  address plan. A peer's `allowed_prefixes` are its probe address and every prefix whose active route goes through
  it, so a prefix is with one peer at a time, as WireGuard needs. Keepalive and `needs_traversal` follow NAT: the
  hub keeps no keepalive, and two spokes behind NAT need traversal. It refuses a prefix active through two peers,
  a route to a node that is not a peer, an address that is not a usable IPv4 prefix and a bad health setting, and
  reports every problem at once.
* `Encode` writes a config in one canonical form (sorted lists, masked prefixes, `[]` and never `null`, fixed key
  order, a rule's ports as a set) with a SHA-256 `hash`. The same plan gives the same bytes on any run, input
  order or `GOMAXPROCS`. `Verify`, a strict `Decode`, `Clone`, and `PlanID` (the topology's name and eight hex
  digits of a hash of every node's content) complete it.
* `Diff` and `DiffPlans` say what changed between two plans: peers added, removed and changed, prefixes moved from
  one peer to another (including to or from a peer that was added or removed), and routes added, removed and
  changed, with `Text` for people.
* `ScanSecrets` and `ScanOSFields` look for key material, tokens, interface names, file paths, commands and table
  numbers in a config, and never repeat what they find.

## Documentation
* Every exported identifier in `config` has a doc comment with an `Example:` block, every exported function has a
  runnable example on pkg.go.dev, and the package documentation explains the allowed-prefixes rule.
* `README.md` lists `config` and `failover`.

## Breaking Changes
None. Everything in this release is new API.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | **Defined** (`NodeConfig`). Alpha: it may change until declared stable |
| `beaver.topology/v1alpha1` | Unchanged |
| `beaver.ipam/v1` | Unchanged |
| `beaver.failover/v1` | Unchanged |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.7.0
go mod tidy
```

# Release 2026-11-21 (v0.6.0)

## Announcements
Release R6 completes link health and adds `failover`. A link that keeps failing and recovering is now pinned down,
and an operator can drain a link. The new `failover` package turns health into routes: it folds in new evidence,
recomputes every node's routing table with the real engine, says exactly what changed on each node and why, and
refuses to install a failover that the simulation shows would break something that worked. Its state can be saved
and reloaded, so a restart does not forget a hold-down.

## Package Highlights

#### `failover` (new)
* `Evaluate` folds health signals into every link's state machine, marks the links that are not usable as down,
  recomputes every node's table with `route.Compute` (once per evaluation), compares with the tables from before,
  and returns a `Decision` per link with the routes it activated and withdrew on each node, and why. When a direct
  link between two spokes fails, one decision lists the backups promoted on both ends; when it recovers, the
  primaries return on both ends together.
* The simulation guard: the new tables are simulated over the network as health now has it. A failover that breaks
  a flow that worked before (a loop made by a bad static route, say) is not installed: `Result.Rejected` names the
  flows, the old tables stay, and the health change is still recorded. It is judged against the network the kept
  tables were made for, so it stays rejected until the problem is gone. A node cut off because all its links failed
  is not a reason to reject. Empty or repeated flow labels are refused.
* `State` is everything a restart must not forget: link states, hold-down, pin and flap timers, and the installed
  tables, in the schema `beaver.failover/v1` (`State.JSON`, `ParseState`, which refuses damaged input). A run that
  stops and resumes between every batch ends byte-identical to one that never stopped (a 300-run property test).
* `RequiredLinks` reads which links must be proven from a topology.

#### `health`
* Flap pinning: a link that goes Down more than `flap_limit` times within `flap_window` is pinned Down for
  `pin_for`, whatever the evidence says; the decision says "flap limit".
* Operator drain: an admin signal that is not OK drains a link (Down at once, until released); one that is OK
  releases a drain and a pin. A release restarts the hold-down, so the link must prove itself again through
  Recovering. An admin order older than the newest one already applied is skipped, so a late release cannot undo a
  newer drain.
* `Link.Clone` and `LinkHealth.Usable`. `Link` gains `Drained`, `Pinned`, `PinnedUntil`, `LastAdmin` and `Flaps`.

#### `route` and `sim`
* `route.Diff` is much faster on tables that did not change (500 nodes: 1.3 s to 0.05 s) and gives the same output.
* `sim.NewNet` indexes the tables across the available processors, with the same output.

## Documentation
* Every exported identifier in `failover`, and the new exported fields of `health`, has a doc comment with an
  `Example:` block, and every exported function has a runnable example on pkg.go.dev.

## Breaking Changes
* `route.Table` now has JSON tags: it writes `node` and `routes`, where it wrote `Node` and `Routes`.
* `health.Evaluator.Apply` applies admin signals instead of skipping them; one that cannot be applied (draining a
  drained link, releasing a link that is neither drained nor pinned) is still returned in `Skipped`, with the reason.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Not yet defined |
| `beaver.topology/v1alpha1` | Unchanged |
| `beaver.ipam/v1` | Unchanged |
| `beaver.failover/v1` | **New** (`State`) |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.6.0
go mod tidy
```

# Release 2026-11-21 (v0.5.0)

## Announcements
Release R5 completes the packet simulator and adds link health. `sim` can now fail links and nodes,
recompute every route with the real engine, simulate every flow over the result, plant faults on
purpose, and write reports that are byte-identical run after run. The new `health` package decides which
links may carry traffic from probes, handshakes and interface signals, without ever reading the clock.

## Package Highlights

#### `sim`
* `RunScenario` and `RunScenarios` run a failure scenario (`links_down`, `nodes_down`, `expect`): the failures
  are laid over the graph, every route is recomputed with the real `route.Compute` (one call per scenario, and
  a test fails the build if `sim` ever calls it anywhere else or computes routes itself), and every flow is
  simulated over the new tables. A flow that starts or ends at a failed node is expected to be undeliverable
  (`UNREACHABLE_EXPECTED`); live nodes that the failures cut off from each other get `NO_HEALTHY_PATH`, which
  passes the gate and is listed. `Expect` turns a scenario into an assertion: a wrong verdict, or an expectation
  that matches no flow, fails the scenario and names it.
* Scenarios are checked before anything runs. `RunScenarios` reports every problem at once as
  `INVALID_SCENARIO`: a missing or repeated name, and every link or node a scenario names that the network does
  not have, so a typo can never quietly test the healthy network.
* `ParseScenarios` reads scenarios from YAML, strictly: unknown keys, verdicts or families, a missing name or
  verdict, a repeated name and a second document are refused. It has a fuzz target (`make fuzz`).
* `CorruptTable` returns a copy of the tables with one node's routes for one prefix deleted, retargeted or
  planted, for tests that plant a black hole, a loop, a one-sided link or a wrong neighbour and check the
  simulator catches it. The prefix is masked first, so `10.42.32.7/20` means `10.42.32.0/20`. The input is not
  changed.
* `Report.Text` and `ScenarioResult.Text` write the report for people: a header, the verdict counts (the zeros
  that matter always shown), PASS or FAIL, and each failing flow's forward and return journey link by link.
  `Report.JSON` and `ScenarioResult.JSON` write JSON with a fixed key order and sorted flows, byte-identical for
  the same run (golden files in `testdata/golden`).
* `NewReport` sorts by label, and breaks ties with a key that covers every field, quotes every string and tells
  a nil list from an empty one, so the report is the same in every input order.
* `NewNet` indexes each table once, so the all-pairs overlay flows of 500 nodes (249,500 flows, each sent and
  answered) take about two seconds against a budget of ten. The `Net` is a snapshot: change the tables, build a
  new one. A link counts as up only when none of its directions is marked down, and the prefixes each node
  holds are masked as they are read.

#### `route`
* `Table.Index` returns a read-only snapshot of a table's active routes for fast longest-prefix lookup, and
  `Index.Lookup` answers exactly what `Table.Lookup` does, masked prefixes included (a property test checks it
  over 10,000 random tables, including IPv6, unmasked prefixes, ties and awkward addresses), a hundred times
  faster on a 2,000-route table.
* The running total of link costs that `Compute` checks against `MaxTotalCost` can no longer wrap round: two
  edges of `math.MaxInt` are refused with `INVALID_TOPOLOGY` instead of slipping through as a negative sum.

#### `health` (new, not yet complete)
* `Signal` is one piece of evidence about one direction of one link: a probe, a WireGuard handshake, the state of
  the interface, or an operator instruction. `Config` holds the thresholds and times (three failures to go
  down, five good signals to recover, silence for 15 seconds is a failure, a 60 second hold-down) and refuses
  settings that make no sense with `INVALID_CONFIG`, including a stale time that is not longer than the probe
  interval.
* `Direction` is the state machine of one end of a link: Unknown, Healthy, Degraded, Down, Recovering. A single
  failure only degrades, so routes do not move for a lost probe. Successes during the hold-down are ignored and
  do not add up to a recovery the moment it ends. A failure while recovering restarts the hold-down.
* `Link` and `Evaluator` combine the two directions: a link is usable only when healthy in both, so both ends
  fail over together and paths stay symmetric. A required (direct) link is unused until proven; an assumed
  (hub-spoke) link is used until it fails. An interface signal can take a direction down at once but can never
  raise health: ten minutes of interface-up signals leave a new link Unknown.
* Silence counts as failure after `stale_after`, judged from the moment the link went quiet, not from when
  someone looked, and judged between the signals of a batch as well as at its end. When both ends go quiet, the
  one that went stale first is recorded. Nothing reads the clock: every method is told the time.
* `Evaluator.Apply` returns every change as a `Decision` with a reason, and every signal it could not use as a
  `Skipped` with a reason. A signal older than the newest one already applied to the same direction
  (interface signals included) is skipped as outdated, so a late success cannot erase a later failure. Admin
  and unknown signals are skipped before they change anything. `UnusableLinks` is the list to mark down in the
  routing graph or hand to a simulation scenario.
* Not yet built: flap pinning and operator drain (admin signals are skipped with a reason), saving state, and
  recomputing routes on a change.

#### `types`
* New error codes `INVALID_SCENARIO` and `INVALID_CONFIG`.

## Documentation
* Every exported identifier in `route`, `sim` and `health` has a doc comment with an `Example:` block, and every
  exported function has a runnable example on pkg.go.dev.

## Breaking Changes
None. Everything in this release is new API.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Not yet defined |
| `beaver.topology/v1alpha1` | Unchanged |
| `beaver.ipam/v1` | Unchanged |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.5.0
go mod tidy
```

# Release 2026-11-21 (v0.4.0)

## Announcements
Release R4 adds routing and the foundations of the packet simulator. `route` computes every node's
routing table from the graph and the address plan, with one backup per route, and explains and compares
tables. `sim` pushes packets through those tables hop by hop and judges every flow and its reply.

## Package Highlights

#### `route` (new)
* The vocabulary of routing: `Route` (prefix, next hop, link, metric, source, path, active), `Source`
  (`connected`, `computed`, `backup`, `static`), `Table` (one node's routes) and `Tables` (one table per node).
  A route's source is never left unset: the zero value is `unknown` and refuses to be written as JSON.
* `Compare` is a total order on routes, and `Table.Sort` uses it, so the same routes always sort the same way.
* Prefixes are masked once, where routes enter the package: `Table.Sort`, `Table.Lookup`, `Explain` and `Diff`
  treat a route written `10.42.16.5/20` as `10.42.16.0/20`, and return it in that form.
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
  transit `/30` of each direct link, which only its two ends hold. With no registry it returns `INVALID_VALUE`.
* Nothing is dropped silently. A pair of nodes with no path is returned in `Result.Unreachable` with a reason
  (no healthy path, or the only path crosses a spoke). A static `Override` is accepted only if it points at a
  real neighbour, is always flagged as `static`, and otherwise comes back in `Result.Rejected` with an
  explanation. Two overrides for one block spelled differently are decided the same way in any input order.
  Broken input, such as one block announced by two nodes, is refused with a coded error.
* `Compute` refuses a graph whose link costs add up to more than `MaxTotalCost` (2,147,483,647) with
  `INVALID_TOPOLOGY`, so no path cost can wrap round to a negative number on any platform; `SPF`, which returns
  no error, stops a cost at the largest int instead of wrapping.
* `route` defines its own small `Graph` interface and does not import `topology`; the planner will adapt
  `topology.Graph` to it.
* `Explain` says which route a node uses for an address and why: the match, the route in use, the backup that
  would take over, a flagged static override, and link health if you supply it. `Explanation.String` is stable
  text suitable for a golden file.
* `Diff` compares two sets of tables and returns, per node, the routes added, removed and changed. A backup
  that takes over from a failed primary shows as a change with `Activated` true. Identical tables give an empty
  diff.
* Shortest paths run on integers with a typed heap: 500 nodes, one hub and 200 direct links compute in well
  under a second, and 2,000 nodes with four hubs in about eleven seconds.

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
  negative flow per node. The order is fixed and total.
* A route more specific than anything a node holds, such as a static override for one address of its own
  block, is followed instead of the packet being delivered there, so the override is tested.
* A site whose block is a single `/32` is tested at that address, not the next one. A supernet that leaves no
  address outside it for the negative flows is refused with `INVALID_VALUE` instead of testing nothing.
* `NewReport` orders results that share a label by everything else in them, so the order never depends on the
  input's. `NewNet` builds the network under test from a `route.Graph` and the prefixes each node holds.

#### `types`
* New error code `INVALID_VALUE`, returned (and found with `errors.Is`, also through `encoding/json`) when a route
  source, hop result, verdict or flow family is unset or not a known name, and when `Flows` is given no input.

## Documentation
* `route` and `sim` have package overviews and runnable examples, and appear in the README and the root
  package documentation.

## Breaking Changes
None. Everything in this release is new API.

## Schema Versions
| Schema | Status |
|---|---|
| `beaver.netctl/v1alpha1` | Not yet defined |
| `beaver.topology/v1alpha1` | Unchanged |
| `beaver.ipam/v1` | Unchanged |

## Upgrading
```
go get github.com/vectorihub/netctl@v0.4.0
go mod tidy
```

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
