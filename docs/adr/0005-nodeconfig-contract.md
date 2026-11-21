# 0005. NodeConfig is the control-plane to data-plane contract

- **Status:** Accepted
- **Date:** 2026-11-21
- **Decisions covered:** D4 (one entry point), D5 (schemas version separately), D6 (`v0.x` until the Phase 5
  gate), AC9 (configuration independent of the machine), rules G1–G10
- **Related:** [versioning policy](../versioning.md), ADR 0003 (address blocks)

## Context

netctl decides; an agent applies. Something has to cross from one to the other, and whatever it is will be
read by programs that are written, released and deployed separately from netctl: today a Linux agent, later
an agent for another operating system or a cloud-API adapter, and a control plane that stores and compares
what was sent.

If that something were a list of commands (`ip route add ...`, `wg set ...`) it would tie every consumer to
one operating system and to the version of one tool, and a planner that emits commands has already decided
how, which is the agent's job. If it were nothing but a routing table, the agent would have to rediscover
WireGuard's rule that a prefix belongs to one peer, and failover would be a place where each agent could
get it wrong in its own way.

## Decision

1. **The contract is one document per node, the `NodeConfig`, in Beaver's own vocabulary.** It says what the
   node's network should be: its overlay address, its peers, the prefixes each peer may carry, its routes,
   what to probe, whether it forwards. It never says how. There are no interface names, commands, sysctls,
   routing-table numbers or file paths, and no secrets: a peer's public key is named by reference
   (`node:aws-hub`) and private keys are made on the node and never leave it. Two scanners (`ScanSecrets`,
   `ScanOSFields`) and an allow-list of keys taken from the schema check this on every generated config.
2. **The hard WireGuard rule is solved in the control plane.** A peer's `allowed_prefixes` are its probe address
   and every prefix whose active route goes through it, so a prefix is with one peer at a time and failover
   moves it from the direct peer to the hub in the next config. The agent applies the document as one
   change and needs no cleverness. The validator checks the result (V-CFG-002 to V-CFG-006).
3. **Same plan, same bytes.** Every list is sorted, prefixes are masked, empty lists are `[]`, keys are written in
   the order of the Go fields, and `hash` is the SHA-256 of the canonical JSON with the hash empty. A control
   plane pushes a config only when its hash changes, and an agent reports the hash it applied. The field order
   of the Go structs is therefore part of the contract.
4. **A JSON Schema is published** in `config/schema/v1alpha1.json` and is strict (`additionalProperties: false`
   at every level), so consumers that are not written in Go can validate what they receive, and so that an
   unexpected key is refused and not ignored.
5. **The plan has an ID.** `plan_id` is the topology's name and eight hex digits of a hash of every node's content
   (never including `plan_id` or `hash` themselves, so it can be worked out again from finished configs).
   It changes exactly when some node's content does.

## Schema versioning policy

This applies D5 to `beaver.netctl/v1alpha1` and follows [docs/versioning.md](../versioning.md).

- **The schema string changes only for a breaking change** to the format. These are breaking: removing or
  renaming a key, changing a key's type or meaning, making an optional key required, narrowing what a
  value may be, and changing the canonical form (key order, sort order, how a list is written), because that
  changes every hash. These are not: adding an optional key that readers can ignore without harm and that
  an old agent can leave out, and widening what a value may be where old values keep their meaning.
- **Readers refuse what they do not know.** `config.Decode` refuses an unknown schema
  (`UNSUPPORTED_SCHEMA`) before looking at anything else, and an unknown key (`INVALID_VALUE`). An agent that
  meets a newer schema must refuse it and say so, never half-apply it.
- **`v1alpha1` may change in a minor release** while netctl is `v0.x`. Each change is listed in the changelog
  under Breaking Changes. The names go `v1alpha1`, `v1beta1`, `v1`: `v1beta1` only after an agent applies
  `v1alpha1` on real machines and a second kind of agent can read it; `v1` at the Phase 5 gate, when
  netctl becomes `v1.0.0`.
- **During a transition the control plane may write two schemas for a time,** and an agent says which it reads.
  netctl does not write two at once; it writes the one it is built for.
- **The schema, the Go types and the docs move together.** A test fails if the Go struct tags and the schema
  list different keys or different required keys, or if the published file is not what the package embeds.

## Consequences

- **Good:** a new kind of agent needs only this document and the schema. A reviewer can read a diff of two
  plans in Beaver's words, and `config.Diff` shows prefixes moving between peers when a link fails.
- **Good:** the agent has no policy of its own to get wrong, and its tests can feed it golden documents.
- **Cost:** the document duplicates some of what the agent could work out (the peers' prefixes), and every
  change to the generator is a change to a contract with versions deployed elsewhere. The hash and the
  strict schema are what make that survivable.
- **Cost:** field order and sort order are frozen, since they decide the bytes. A change to either is a breaking
  change and a new schema string, however small it looks.

## Decisions made while building it

These are recorded here because a later reader will ask.

- **Probes use the link's transit `/30`,** not the peers' overlay addresses, and the far end's probe address stays
  in the direct peer's `allowed_prefixes` after the traffic has failed over, so probes keep testing the link
  and its recovery can be seen. (From the end-to-end guide, which refines the plan.)
- **`needs_traversal` means neither end can be dialled:** the peer has no public endpoint and the node is not
  publicly reachable either, so two spokes behind NAT need it and a spoke and the hub do not. The hub's peers
  therefore carry no keepalive. A node that never said whether it is behind NAT is treated as behind.
- **A prefix that would be active through two peers is an error,** not something the generator picks a winner for.
- **Open inconsistency in the test plan:** the end-to-end guide says the hub's config is byte-identical across the
  healthy and failed-over plans, and also that `plan_id` is in every file and follows every node's content. Both
  cannot hold. This ADR follows the plan and build guide; the hub's content is identical across the two plans
  (tested), so the end-to-end check should compare the hub's file with `plan_id` and `hash` masked.

## Release note

`v0.7.0` was cut when `config` was built (V56 to V60), before this record and before the remaining Era 8 checks.
The tag is not moved. This ADR changes no code.
