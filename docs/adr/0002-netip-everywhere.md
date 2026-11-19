# 0002. Use `net/netip` for every address and prefix

- **Status:** Accepted
- **Date:** 2026-11-19 (the rule appears in the architecture plan's "traps" list, trap 1)

## Context

netctl is mostly arithmetic on addresses and blocks: allocate a block, check two blocks do not overlap,
find the longest matching route. Go offers two families of types for this: the older `net.IP` and
`net.IPNet`, and the newer `net/netip` (`Addr`, `Prefix`).

The older types cause real planning bugs:

- `net.IP` is a byte slice. It can be 4 or 16 bytes long for the same address, so `==` is wrong and
  it cannot be a map key.
- `net.IPNet` stores a mask, not a length, and does not require host bits to be zero.
- `netip.ParsePrefix("10.42.16.5/20")` also keeps host bits. Two spellings of one block
  (`10.42.16.5/20` and `10.42.16.0/20`) are then different values and slip past overlap checks.

## Decision

1. **Every address is a `netip.Addr` and every block is a `netip.Prefix`.** `net.IP` and `net.IPNet`
   are not used in any netctl type or function signature.
2. **Prefixes are masked once, at the boundary.** Input is parsed and normalised by the `cidr` package
   (V10), which calls `Prefix.Masked()` and rejects invalid values with a coded error. Past that point,
   a prefix in netctl always has zero host bits.
3. **Types are comparable and usable as map keys.** `netip.Addr` and `netip.Prefix` support `==` and
   work as keys, which keeps lookups and de-duplication simple and deterministic.
4. **IPv4 first, IPv6 not excluded.** The types already carry IPv6. Golden tests for IPv6 are Phase 5.
5. **The standard library only.** `net/netip` adds no dependency, which keeps the one-runtime-dependency
   rule (D8) intact.

## Consequences

- Overlap checks, equality and map lookups are plain Go. There is no hand-written byte comparison.
- A prefix that reaches `ipam`, `topology`, `route` or `config` is already canonical, so two spellings
  of one block cannot both exist.
- Any code that must call a `net.IP` API (none is expected in this module) converts at the edge and
  does not let the old type leak back in.
- Reviewers can reject `net.IP`, `net.IPNet` and `net.ParseCIDR` on sight.
