<div align="center">
  <h1>netctl</h1>

  <p><strong>Deterministic planning for private overlay networks, as a Go library.</strong></p>

  <a href="https://github.com/vectorihub/netctl/actions/workflows/ci.yaml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/vectorihub/netctl/ci.yaml?branch=main&style=for-the-badge&labelColor=000000&label=CI"></a>
  <a href="https://github.com/vectorihub/netctl/releases"><img alt="Release" src="https://img.shields.io/github/v/release/vectorihub/netctl?sort=semver&style=for-the-badge&labelColor=000000"></a>
  <a href="https://pkg.go.dev/github.com/vectorihub/netctl"><img alt="Go Reference" src="https://img.shields.io/badge/go-reference-007d9c?style=for-the-badge&labelColor=000000&logo=go&logoColor=white"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-blue?style=for-the-badge&labelColor=000000"></a>
</div>

<br>

`netctl` models a private network, validates it, and returns plain data that other programs act on. It runs no
daemon and needs no network or file access, and the same input always produces the same output.

The library requires a minimum of **Go 1.25**. Release notes are in the [changelog](CHANGELOG.md).

**Jump to:**
- [Getting started](#getting-started)
- [Documentation](#documentation)
- [Getting help](#getting-help)
- [Contributing](#contributing)
- [Security](#security)

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

## Documentation

The full API reference, with runnable examples, is on [pkg.go.dev](https://pkg.go.dev/github.com/vectorihub/netctl).

| Package | Purpose |
|---|---|
| [`types`](https://pkg.go.dev/github.com/vectorihub/netctl/types) | Shared identifiers, coded errors, and an injectable clock. |
| [`cidr`](https://pkg.go.dev/github.com/vectorihub/netctl/cidr) | Address-block arithmetic on `net/netip`. |
| [`ipam`](https://pkg.go.dev/github.com/vectorihub/netctl/ipam) | Address allocation, ownership lookup, accounting, and snapshots. |
| [`topology`](https://pkg.go.dev/github.com/vectorihub/netctl/topology) | Network model, YAML loading, validation, and hashing. |

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
