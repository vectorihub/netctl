<div align="center">
  <h1>netctl</h1>

  <p><strong>Deterministic planning for private overlay networks, as a Go library.</strong></p>

  <a href="https://github.com/vectorihub/netctl/actions/workflows/ci.yaml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/vectorihub/netctl/ci.yaml?branch=main&style=for-the-badge&labelColor=000000&label=CI"></a>
  <a href="https://github.com/vectorihub/netctl/releases"><img alt="Release" src="https://img.shields.io/github/v/release/vectorihub/netctl?sort=semver&style=for-the-badge&labelColor=000000"></a>
  <a href="https://pkg.go.dev/github.com/vectorihub/netctl"><img alt="Go Reference" src="https://img.shields.io/badge/go-reference-007d9c?style=for-the-badge&labelColor=000000&logo=go&logoColor=white"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-blue?style=for-the-badge&labelColor=000000"></a>
</div>

<br>

`netctl` is a Go library that plans private overlay networks. You describe the network, and it decides how the
network should look, checks the result, and returns it as plain Go values that your own program stores or applies.

The library requires a minimum of **Go 1.25**. Release notes are in the [changelog](CHANGELOG.md).

**Jump to:**
- [What is netctl?](#what-is-netctl)
- [Getting started](#getting-started)
- [Documentation](#documentation)
- [Getting help](#getting-help)
- [Contributing](#contributing)
- [Security](#security)

## What is netctl?

An **overlay network** joins machines in different places (cloud regions, offices, data centres, a server under a
desk) into one private network that runs on top of the internet. Before one can be built, someone has to decide
how it should look:

- which address block each site gets, and which address each machine gets;
- which machines connect to which, and which connections are preferred;
- who is allowed to reach whom.

`netctl` makes those decisions and proves they are consistent. It hands out addresses that never overlap, checks a
network description against a set of rules, and reports every problem at once with its location.

`netctl` is a **planner, not an agent**. It never configures a machine, opens a connection, reads a file or starts a
process. Everything it needs is passed in as values, and everything it decides comes back as values. That makes it:

- **Deterministic.** The same input always produces the same output, byte for byte.
- **Easy to test.** Plans are computed in memory, in milliseconds, with no infrastructure.
- **Safe to embed.** It can run inside a control plane, a CLI, a CI check or a web service.

Today `netctl` provides address management (`ipam`), the network model (`topology`), routing (`route`), a packet
simulator (`sim`), link health (`health`), failover (`failover`), desired configuration (`config`), the validator (`validate`) and the planner (`plan`), with the supporting `types` and `cidr` packages.

## Status and version support

`netctl` is **pre-1.0**. Minor releases may contain breaking changes to the public API, and each one is called out in
the [changelog](CHANGELOG.md). Patch releases never break the API.

### Go version support policy

`netctl` follows the upstream [Go release policy](https://go.dev/doc/devel/release#policy) and supports the two most
recent major versions of Go. Support for an older version may be dropped in a minor release.

## Getting started

To get started, create a module, add `netctl`, and allocate addresses for a site and a machine.

###### Initialize project

```sh
$ mkdir hellonetctl && cd hellonetctl
$ go mod init hellonetctl
```

###### Add the dependency

```sh
$ go get github.com/vectorihub/netctl@latest
```

###### Write code

Create `main.go`:

```go
package main

import (
	"fmt"
	"log"
	"net/netip"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

func main() {
	// Time is passed in, so results are reproducible.
	clock := &types.FixedClock{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	// Carve a /16 supernet into a pool of /20 site blocks
	// and a pool of single /32 machine addresses.
	registry, err := ipam.New(ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: netip.MustParsePrefix("10.42.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
			{Name: "nodes", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
		},
	}, clock, ipam.DefaultQuarantine)
	if err != nil {
		log.Fatalf("create registry: %v", err)
	}

	site, err := registry.Allocate("sites", ipam.Site("east"))
	if err != nil {
		log.Fatalf("allocate site: %v", err)
	}
	node, err := registry.Allocate("nodes", ipam.Node("east-1"))
	if err != nil {
		log.Fatalf("allocate node: %v", err)
	}

	owner, ok := registry.OwnerOf(node.Prefix.Addr())
	if !ok {
		log.Fatal("address has no owner")
	}

	fmt.Println("site:", site.Prefix)
	fmt.Println("node:", node.Prefix, "owned by", owner)
}
```

###### Compile and run

```sh
$ go run .
site: 10.42.0.0/20
node: 10.42.240.1/32 owned by node:east-1
```

### Plan a whole network

The root package is the one entry point. `netctl.Plan` takes the network and its state, and returns the addresses, the
routes, a config for every node and the validator's verdict. `Result.Render` gives the bytes of every output file
without doing any I/O, so you decide where they are written.

```go
result, err := netctl.Plan(netctl.Inputs{
	Topology: topo,                    // built in code or loaded with topology.Load
	IPAM:     previous.IPAM,           // the address plan saved by the last run; empty on the first
	Clock:    &types.FixedClock{T: t}, // time is passed in, never read
})
if err != nil {
	// VALIDATION_FAILED: the plan was refused and has no configs, so write nothing.
	return err
}
files, err := result.Render() // plan.json, ipam.json, routes.txt, nodes/<node>.json, ...
```

To react to link health, pass `Inputs.Signals` (and the `Result.Failover` state of the last plan as `Inputs.Failover`):
the routes are then computed by the failover guard, which refuses a change that would break a flow that worked and keeps
the routes in force. `netctl.Diff(before, after)` shows what a new plan would change on each machine. The exported
surface is recorded in [`docs/api-surface.txt`](docs/api-surface.txt).

## Documentation

The full API reference, with runnable examples, is on [pkg.go.dev](https://pkg.go.dev/github.com/vectorihub/netctl).

| Package | Purpose |
|---|---|
| [`types`](https://pkg.go.dev/github.com/vectorihub/netctl/types) | Shared identifiers, coded errors, and an injectable clock. |
| [`cidr`](https://pkg.go.dev/github.com/vectorihub/netctl/cidr) | Address-block arithmetic on `net/netip`. |
| [`ipam`](https://pkg.go.dev/github.com/vectorihub/netctl/ipam) | Address allocation, ownership lookup, accounting, and snapshots. |
| [`topology`](https://pkg.go.dev/github.com/vectorihub/netctl/topology) | Network model, YAML loading, validation, and hashing. |
| [`route`](https://pkg.go.dev/github.com/vectorihub/netctl/route) | Routing tables for every node: shortest paths, backups, longest-prefix lookup, explanations and diffs. |
| [`sim`](https://pkg.go.dev/github.com/vectorihub/netctl/sim) | A virtual test bench: pushes packets through the tables, judges every flow, runs failure scenarios, and writes text and JSON reports. |
| [`health`](https://pkg.go.dev/github.com/vectorihub/netctl/health) | Which links may carry traffic: per-direction state machines, hold-down, staleness, and flap pinning, operator drains, and the rule that a link is only as good as its worse direction. |
| [`config`](https://pkg.go.dev/github.com/vectorihub/netctl/config) | One desired-state document per node, in Beaver's own words and independent of the machine that applies it: peers with the prefixes each may carry (so failover moves a prefix from the direct peer to the hub), routes, probes, a canonical encoding with a content hash, a plan ID, a structured diff, a JSON Schema and a secret scanner. |
| [`validate`](https://pkg.go.dev/github.com/vectorihub/netctl/validate) | The last gate before a configuration leaves the controller: a catalogue of 32 rules (addressing, topology, links, routes, configs, simulation) with stable IDs, a single-failure sweep, suppressions for warnings with a mandatory reason (an error can never be suppressed), and text and JSON reports. |
| [`netctl`](https://pkg.go.dev/github.com/vectorihub/netctl) | The root package and the one obvious entry point: `Plan`, `Diff` and their types, with `Result.Render` for the bytes of every output file. |
| [`plan`](https://pkg.go.dev/github.com/vectorihub/netctl/plan) | One pure function from a network description and its state to a plan: the address plan, the routes, a config for every node and the validator's verdict, in a fixed order, the same bytes however the input is ordered; a plan the validator refuses has no configs and a plan in force that was good stays in force. |
| [`failover`](https://pkg.go.dev/github.com/vectorihub/netctl/failover) | What the routing tables should be when links fail or recover: folds health signals, recomputes every table through the real engine, explains each change, refuses a failover the simulation shows makes things worse, and saves its state so a restart does not reset a hold-down. |

## Getting help

- Read the [API reference](https://pkg.go.dev/github.com/vectorihub/netctl).
- Search the [existing issues](https://github.com/vectorihub/netctl/issues) before opening a new one.
- [Open an issue](https://github.com/vectorihub/netctl/issues/new) to report a bug or request a feature.

### Opening issues

GitHub issues are for bug reports and feature requests. When reporting a bug, please include:

- The `netctl` version, Go version, and operating system.
- The smallest input that reproduces the problem.
- The output you got and the output you expected.

## Contributing

Contributions are welcome. For anything larger than a small fix, please open an issue to discuss the change first.

Pull requests must pass `make check lint race arch`, include tests for new behaviour, and are submitted under the
Apache 2.0 license.

### Good first issues

Issues labelled [`good first issue`](https://github.com/vectorihub/netctl/labels/good%20first%20issue) are a
good place to start.

---

## Security

If you believe you have found a security vulnerability, please disclose it responsibly and ***do not*** open a public
issue. Report it privately through
[GitHub security advisories](https://github.com/vectorihub/netctl/security/advisories/new).

## License

`netctl` is licensed under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
