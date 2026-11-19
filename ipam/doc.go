// Package ipam manages IP addresses for a private network.
//
// An [IPAM] is a registry over one supernet, such as 10.42.0.0/16, divided
// into named pools. Each pool hands out blocks of one size: a /20 for every
// site, a /32 for every machine, a /30 for every direct link. The registry
// guarantees that no two owners ever hold overlapping blocks, and it can prove
// where every address in the supernet is.
//
// # Creating a registry
//
// A registry is built from a [Snapshot] with [New]. The snapshot is plain data:
// the schema version, the supernet, the pools and any existing allocations.
// New checks every rule and reports every problem at once, so a registry that
// exists is always consistent. Time comes from a [types.Clock] you pass in, so
// results are reproducible in tests.
//
//	registry, err := ipam.New(ipam.Snapshot{
//		Schema:   ipam.Schema,
//		Supernet: netip.MustParsePrefix("10.42.0.0/16"),
//		Pools: []ipam.Pool{
//			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
//		},
//	}, clock, ipam.DefaultQuarantine)
//
// # Owners
//
// Every allocation belongs to an [Owner]: a site, a node, a link or a piece of
// shared infrastructure. Build one with [Site], [Node], [Link] or [Infra]. In
// text and JSON an owner is written "kind:id", for example "node:web-1".
//
// # Allocating addresses
//
//   - [IPAM.Allocate] gives an owner the lowest free block in a pool. It is
//     idempotent: asking again returns the same block.
//   - [IPAM.AllocateSpecific] claims an exact prefix, for hand-planned ranges.
//   - [IPAM.AllocateTransit] numbers a direct link between two nodes with a
//     /30 and returns the address of each end.
//   - [IPAM.Reserve] holds a prefix for an owner without marking it in use.
//
// Pools marked Reserved refuse allocation unless [WithOverride] gives a reason,
// which is recorded with the allocation.
//
// # Releasing addresses
//
// [IPAM.Release] does not free a block at once. The block is quarantined for
// the period given to New ([DefaultQuarantine] is 24 hours), so stale routes
// and caches that still point at the old owner can never reach a new one.
//
// # Looking up and accounting
//
// [IPAM.OwnerOf] answers who holds an address, [IPAM.Lookup] finds the
// allocation that contains a prefix, and [IPAM.ByOwner] lists what an owner
// holds. [IPAM.Accounting] returns a [Report] showing that allocated, reserved,
// quarantined and free space add up to exactly the whole supernet, overall
// and per pool.
//
// # Saving state
//
// [IPAM.Snapshot] returns the full state, [Marshal] writes it as byte-stable
// JSON, and [Unmarshal] reads it back. netctl never touches a file: you store
// the bytes wherever you like and pass them back to New next time.
//
// # Errors
//
// Errors carry a stable code from package types, such as OVERLAP,
// POOL_EXHAUSTED, RESERVED or NOT_FOUND. Match them with [types.HasCode].
package ipam
