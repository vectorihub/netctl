# 0004. Released addresses are quarantined before reuse

- **Status:** Accepted
- **Date:** 2026-11-19
- **Decisions covered:** rule R8 (with R9 and R10), V18

## Context

When a machine or site gives up an address block, something may still point at it: a route on another
node, a firewall rule, a DNS record, a cached peer entry. If the block were handed to a new owner
straight away, traffic meant for the old owner would reach the new one.

## Decision

1. **`Release` does not free a block. It quarantines it.** The allocation stays in the registry with
   state `quarantined` and a `QuarantineUntil` time. While it is there it still counts as taken: no
   allocation may overlap it (R4), and `Allocate` skips it.
2. **The period is chosen by the caller and defaults to 24 hours.** `New` takes the period as an
   argument; `ipam.DefaultQuarantine` is 24 h. A period of zero frees the block at once.
3. **The injected clock ends the quarantine.** Every public method that reads or changes the registry
   first drops quarantined blocks whose time has come (`!now.Before(QuarantineUntil)`). The library
   never reads the real clock (D7), so tests move time with `types.FixedClock.Advance`.
4. **The old owner is not special.** During quarantine the previous owner gets a fresh block, like
   anyone else. `Lookup`, `OwnerOf` and `ByOwner` do not report quarantined blocks.
5. **Releasing something the owner does not hold is `NOT_FOUND`** (R9), including a block that is
   already quarantined. A site's block cannot be released while a non-site holder is inside it (R10);
   the error lists every holder.

## Consequences

- Address reuse is delayed by the period, which costs free space only briefly. A pool near its limit can
  report `POOL_EXHAUSTED` for blocks that are quarantined, not lost.
- Snapshots keep quarantined entries, so the state survives a save and load.
- Expiry is a side effect of reads, so a reader that forgets to call it would see stale state. Every
  entry point calls it first.
- R10 cannot fire through the public API today, because R4 forbids a node allocation inside a site's
  block. The check is kept as a guard against a corrupted registry.
