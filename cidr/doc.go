// Package cidr does prefix arithmetic on net/netip. It adds what net/netip does
// not provide, and uses netip types throughout (see ADR 0002). It imports only
// the types package from this module.
//
// Only IPv4 is supported today. The API uses netip types, so IPv6 support can
// be added without changing it.
//
// # Canonical prefixes
//
// A prefix can be written with host bits set, so 10.42.16.5/20 and
// 10.42.16.0/20 are the same block but different values. Call Canonical once,
// at the boundary, and pass the result on. After that every prefix in netctl has
// zero host bits, so two spellings of one block cannot both exist and slip past
// an overlap check.
//
// # Asking questions
//
//   - Contains: is one block entirely inside another?
//   - Overlaps: do two blocks share any address?
//   - Size: how many addresses does a block hold?
//   - Compare: a total order (by address, then shorter prefix first). Every
//     sorted list of prefixes in netctl uses it, so output never depends on
//     input order.
//
// # Splitting
//
// Children yields the pieces of a block one at a time, in address order, and
// never builds the whole list. A /21 of /32s costs nothing until it is read,
// and a caller that stops early pays only for what it read. Split returns the
// pieces as a slice for small cases and refuses more than about a million.
//
// # Accounting for space
//
// Subtract returns what is left of a block after removing the pieces already
// used, as the fewest prefixes, sorted. IPAM uses it to prove that allocated
// plus free space equals the whole supernet. Usable lists the addresses of a
// small block that a machine can actually be given: it skips the network and
// broadcast addresses of /24 to /30 blocks, and uses every address of a /31 or
// /32.
//
// # Invalid input
//
// Functions do not panic on an empty or IPv6 prefix. Queries answer safely:
// Size returns 0, Contains and Overlaps return false, and Subtract returns nil.
// Children, Split and Usable return an INVALID_PREFIX error from the types
// package, matched by code with types.HasCode.
package cidr
