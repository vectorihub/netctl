// Package route holds the routing tables of a private network and answers the
// question every router asks: "a packet is headed to this address, which way
// does it go?"
//
// # Routes and tables
//
// A [Route] says: to reach this block of addresses, hand the packet to this
// neighbour, over this link, at this cost. It also records where the route
// came from (its [Source]), the full [Route.Path] the packet will take, and
// whether the route is in use right now ([Route.Active]).
//
// A [Table] is one machine's routes. Every machine forwards on its own, so a
// network has one table per machine, kept together in [Tables]. A table holds
// two views of the same list:
//
//   - the full list, which includes backup routes waiting in reserve (a router
//     calls this the RIB);
//   - the active routes, the ones that actually carry traffic ([Table.FIB]).
//
// # Order
//
// [Compare] is a total order on routes: by prefix, then metric, then next hop,
// then the remaining fields. [Table.Sort] uses it, so the same set of routes
// always sorts to the same sequence, whatever order it was built in.
//
// # Longest-prefix match
//
// [Table.Lookup] picks the route for a destination address. Several routes can
// cover one address, for example a route for the whole network, one for a
// site, and one for a single machine. The most specific route, the one with
// the longest prefix, wins. Equal prefixes are settled by the lower metric.
// Only active routes are considered, so a backup is never chosen while its
// primary is healthy. If nothing matches, Lookup reports false rather than
// guessing.
//
// The result never depends on the order of the routes in the table.
//
// # Scope
//
// This package defines routes, tables and lookup. It does no I/O and imports
// only the types and cidr packages from this module.
package route
