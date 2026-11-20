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
// # Computing the routes
//
// [Compute] builds the table of every machine in one call. It needs a [Graph],
// which says which machines exist, whether each is a hub or a spoke, and which
// links join them, and an [Input], which says who holds which block of
// addresses. [Collect] reads that second part from the address plan (an
// ipam.IPAM): the site block and overlay address each machine announces, and
// the /30 of each direct link, which only its two ends hold.
//
// For each machine, Compute adds:
//
//   - a connected route for every block the machine itself holds;
//   - an active computed route for every block another machine announces, along
//     the cheapest path to that machine;
//   - one inactive backup route for the same block, along the cheapest path
//     that avoids the link the primary leaves on, if there is one;
//   - an active static route for every accepted override.
//
// When a link fails, the backup is the route that takes over, and it is already
// in the table. This package does not decide when a link has failed; it computes
// the tables for a graph in which the link is down, and the failover code
// compares the two.
//
// # Shortest paths
//
// [SPF] finds the cheapest path from one machine to every other, over the links
// that are up. One rule makes the network hub-and-spoke instead of an
// accidental mesh: a spoke never forwards traffic for others. A path may end at
// a spoke, but never passes through one, so two spokes with a direct link
// between them still reach a third through the hub, not through each other.
//
// Equal-cost paths are settled the same way on every run: fewer hops first, then
// the lexically smaller next hop, then the smaller link, then the smaller list
// of nodes. The same network always gives the same tables, whatever order its
// machines and links are listed in.
//
// # Nothing is dropped silently
//
// A machine nobody can reach gets no route. The pair is listed in
// [Result.Unreachable], with the blocks that have no route and a reason that
// tells "no path at all" from "the only path crosses a spoke". An override that
// is not accepted is listed in [Result.Rejected] with an explanation. Input
// that is broken, such as one block announced by two machines, is refused with
// a coded error and no tables.
//
// # Static overrides
//
// An [Override] sends traffic for one prefix to one neighbour, whatever the
// shortest-path calculation says. It is accepted only if the next hop is a real
// neighbour, and an accepted one is always flagged: its route has Source
// [SourceStatic]. If an override covers the same prefix as a computed route, it
// takes over and the computed route stays in the table, inactive, so there is
// never more than one active route for a prefix. An override does not follow
// link health: if its link goes down it stays active, and it is for the
// validator to report that.
//
// # What a Graph is
//
// [Graph] is three methods. It is defined here, and not borrowed from the
// topology package, so that routing can be tested with a ten-line fake and so
// that this package does not depend on how a network is described. Edges
// returns every edge out of a machine, with [Edge.Down] set on those that
// cannot carry traffic, because an override must be able to tell "not a
// neighbour" from "a neighbour over a link that is down". The planner adapts
// topology.Graph to this interface.
//
// # Scope
//
// This package defines routes, tables, lookup and their computation. It does
// no I/O and imports only the types, cidr and ipam packages from this module.
package route
