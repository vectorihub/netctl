# 0001. netctl lives in its own repo, as a library, with packages at the root

- **Status:** Accepted
- **Date:** 2026-11-19 (decisions D1–D4 were ruled by the owner on 2026-11-15)
- **Decisions covered:** D1, D2, D3, D4 (and D18, which amends D1)

## Context

Beaver Cloud needs one place that decides how the network should look: addresses, routes, failover and
the per-machine configuration. Several programs will need those decisions: the backend that stores
tenants and plans, the agent on each machine, and the command line users type into.

If the planner lived inside any one of those, it would pick up that program's habits: a database, a
clock, files, a network port. A planner that does I/O cannot be tested with plain function calls, and
its output cannot be guaranteed identical for identical input.

## Decision

1. **A library, in its own repository (D1).** netctl is imported by the programs that do I/O. It has no
   server, port, router, database or daemon. D18 goes one step further: there is no command-line
   program either. Every command-line surface is built in `beaver-cli` and imports a tagged netctl.
   The module therefore has no `cmd/` folder and no `package main` anywhere. The architecture test
   (`arch/`, V5) enforces this.
2. **Module path `github.com/VectorIHub/netctl` (D2).** The GitHub remote decides the path, so other
   modules can import it without a `replace` line in CI.
3. **Packages sit at the repository root (D3).** There is no `netctl/` prefix, and everything is public
   except `internal/`. The agent needs `config.NodeConfig`, and the control plane needs `ipam`,
   `topology`, `health` and `plan`, so they must be importable.
4. **A root facade (D4).** The root package `netctl` re-exports `Plan`, `Inputs` and `Result`, so callers
   write `netctl.Plan(...)`. It arrives in V74.

Package order is strict: a package may import only the ones above it.

```
types → cidr → ipam, topology, health → route → sim, config → failover → validate → plan → (root facade)
```

## Consequences

- The line between deciding and doing is physical. Anything that touches the machine lives in another
  module (the agent, D12), so netctl's import graph stays free of I/O packages.
- netctl is tested by calling functions. No ports, fixtures for servers or process management.
- Every public package is a compatibility promise. Versioning rules are in `docs/versioning.md` (V7).
- Consumers must pin a netctl tag. A separate wrapper binary for non-Go callers is not planned; if it
  is ever needed, it goes in its own module.
- Folder names and the module path are hard to change once other modules import them, which is why this
  is recorded now.
