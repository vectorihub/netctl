// Package netctl plans private overlay networks.
//
// An overlay network joins machines in different places (cloud regions,
// offices, data centres, a server under a desk) into one private network that
// runs on top of the internet. Before such a network can be built, someone has
// to decide how it should look: which address each site and machine gets,
// which machines connect to which, and who may reach whom. netctl makes those
// decisions, checks them, and returns the answer as plain Go values.
//
// netctl is a planner, not an agent. It never configures a machine, opens a
// connection, reads a file or starts a process. You give it a description of
// the network and its current state, and it gives back data that your own
// program stores, displays or applies. Because it does no I/O, it runs anywhere
// Go runs, it is fast to test, and it is safe to embed in a larger system.
//
// # Guarantees
//
//   - Deterministic: the same input always produces the same output, byte for
//     byte. Lists are sorted, and time is passed in through a
//     [github.com/vectorihub/netctl/types.Clock] rather than read from the
//     system.
//   - Strict: ambiguous or invalid input is refused rather than guessed at, and
//     every problem is reported at once, not one per run.
//   - Typed errors: failures caused by the network or its state, such as an
//     overlap, an exhausted pool or an invalid topology, carry a stable
//     [github.com/vectorihub/netctl/types.Code], so callers match them with
//     [github.com/vectorihub/netctl/types.HasCode] instead of message text.
//     Programming mistakes, such as a nil clock or an empty owner, and
//     malformed JSON return plain errors, so always handle a non-nil error
//     even when no code matches.
//   - Standard types: addresses and prefixes are [net/netip] values throughout.
//
// # Packages
//
// netctl is split into small packages that can be used on their own:
//
//   - [github.com/vectorihub/netctl/types]: shared identifiers (sites, nodes,
//     links), roles, coded errors and an injectable clock.
//   - [github.com/vectorihub/netctl/cidr]: address-block arithmetic on
//     net/netip, such as splitting, subtracting and comparing prefixes.
//   - [github.com/vectorihub/netctl/ipam]: IP address management. Hands out
//     non-overlapping address blocks to sites, machines and links, answers who
//     owns an address, accounts for every address, and saves its state as a
//     byte-stable snapshot.
//   - [github.com/vectorihub/netctl/topology]: the network model. Loads a
//     network description from YAML or Go values, validates it against a set
//     of rules, and exposes it as a graph with a canonical hash.
//   - [github.com/vectorihub/netctl/route]: routing. Computes a routing table
//     for every machine, with shortest paths that keep the network
//     hub-and-spoke, one backup per route, and unreachable pairs reported as
//     data. Answers longest-prefix lookups, explains why a route is used, and
//     diffs two sets of tables.
//   - [github.com/vectorihub/netctl/sim]: a virtual test bench. Pushes
//     simulated packets through the tables, judges every flow (black holes,
//     loops, missing returns, asymmetric paths), runs failure scenarios
//     through the real routing engine, and writes stable text and JSON reports.
//   - [github.com/vectorihub/netctl/health]: link health. Turns evidence
//     (probes, handshakes, interface state) into which links may carry
//     traffic, with hold-down, staleness, and a link counting as usable only
//     when it is healthy in both directions.
//   - [github.com/vectorihub/netctl/failover]: the failover guard. Folds link
//     evidence into the routes, simulates the new routes before they are used,
//     and keeps the routes in force if the change would break a flow.
//   - [github.com/vectorihub/netctl/config]: the config of every node, in a
//     canonical form with a hash, a JSON Schema and a diff.
//   - [github.com/vectorihub/netctl/validate]: the catalogue of rules a plan must
//     pass before it can leave the controller.
//   - [github.com/vectorihub/netctl/plan]: the pipeline that runs all of the
//     above in a fixed order.
//
// # A typical flow
//
// Describe the network in a topology file and load it with
// [github.com/vectorihub/netctl/topology.Load], which returns every rule
// violation at once, or build a [github.com/vectorihub/netctl/topology.Topology]
// in code. Pass it to [Plan] with the address plan saved by the last run (empty on
// the first) and a clock. The [Result] holds the routes, a config for every node and
// the verdict of the validator. Call Result.Render for the bytes of every output
// file, write them wherever your program keeps its state, and keep Result.IPAM for
// the next run. A plan the validator refuses returns an error with the code
// VALIDATION_FAILED and no configs, so a program that writes files writes nothing.
//
// This root package is the one obvious entry point: [Plan], [Diff] and the types they
// use. Each package's documentation has runnable examples.
package netctl
