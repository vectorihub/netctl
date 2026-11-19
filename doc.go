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
//   - Typed errors: every failure carries a stable
//     [github.com/vectorihub/netctl/types.Code], so callers match errors with
//     [github.com/vectorihub/netctl/types.HasCode] or [errors.Is] instead of
//     message text.
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
//
// # A typical flow
//
// Describe the network in a topology file and load it with
// [github.com/vectorihub/netctl/topology.Load], which returns every rule
// violation at once. Create an address registry with
// [github.com/vectorihub/netctl/ipam.New] from a saved snapshot, allocate
// blocks for each site and node in the topology, and store the new snapshot
// for next time. Each package's documentation has runnable examples.
//
// This root package holds documentation only; import the packages above.
package netctl
