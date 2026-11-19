# 0003. Machine overlay addresses come from a dedicated block

- **Status:** Accepted
- **Date:** 2026-11-19 (the address plan was ruled on 2026-11-15 as D14)
- **Decisions covered:** D14 (the PDF carve-up), the golden test P1-GOLD-004

## Context

The discussion that started this project gave sites their blocks and gave machines their addresses
separately:

- AWS gets `10.42.0.0/20`, Builder gets `10.42.16.0/20`, DGX Spark gets `10.42.32.0/20`.
- The AWS hub, Builder and DGX Spark use `10.42.0.2`, `10.42.0.3` and `10.42.0.4`.

These conflict. `10.42.0.3` (Builder) lies inside `10.42.0.0/20` (AWS's site block), so two owners would
hold the same address. The conflict also ties a machine's address to the site it first lived in: moving a
machine to another site would change its address.

## Decision

1. **Machines get overlay addresses from their own block, `10.42.240.0/21`, one `/32` each.** The first
   address, `10.42.240.0`, is never handed out (`skip_first: 1`), so the first machine is `10.42.240.1`.
2. **Site blocks (`10.42.0.0/17`, one `/20` per site) are for workloads and LAN addressing only.** No
   machine's overlay address lies inside one.
3. **Direct links are numbered from `10.42.252.0/22`, one `/30` each.** The two usable addresses go to the
   two ends, the lower to the lexically smaller node ID.
4. **The plan is configuration, not code.** The carve-up is the default snapshot; a customer can have another.
5. **The discussion's example becomes a golden test, P1-GOLD-004, that IPAM must refuse.** Asking for
   `10.42.0.3/32` for `node:builder-1` while `site:aws` holds `10.42.0.0/20` returns `OVERLAP` naming both
   owners. The unit test for it is `TestAllocateSpecificInsideExisting`; the golden file arrives with Era 12.

## Consequences

- IPAM has these top-level pools: `sites`, `growth` (reserved), `overlay`, `infra` (reserved) and `transit`.
- A machine keeps its node ID and overlay address when it moves between sites. Only its site prefix changes.
- Up to 2,047 machines fit in the overlay block, 8 sites in `sites` before `growth` is needed, and 256
  direct links in `transit`. Larger networks need a different plan, not different code.
- The alternative (the existing system's layout: lowest-free hosts from `.2`, `.1` reserved for ingress, a
  `/24` per node for containers) is not carried over. Nothing migrates, so no production ranges constrain us.
