package sim

import (
	"net/netip"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// Net is the network under test: every node's routing table, and three
// questions the simulator cannot answer from the tables alone. They are
// functions, not concrete types, so that the simulator depends on neither the
// topology nor the health packages; the planner wires them up, and NewNet does
// it for a route.Graph.
type Net struct {
	// Tables holds each node's routing table. A node with no table drops
	// everything it is given.
	Tables route.Tables
	// Local says whether addr is one of node's own addresses, so a packet for it
	// is delivered. A nil Local means no address is local to any node.
	Local func(node types.NodeID, addr netip.Addr) bool
	// Adjacent says whether link really joins from to to, whether or not the
	// link is up. It tells a route to a node that is not a neighbour (a mistake
	// in the tables) from a route over a neighbour whose link is down (a failover
	// that did not happen). A nil Adjacent means nothing is adjacent, so every
	// forward is WrongNeighbour, which makes a missing wire easy to see.
	Adjacent func(from, to types.NodeID, link types.LinkID) bool
	// Up says whether a link can carry packets right now. A nil Up means every
	// link is up.
	Up func(link types.LinkID) bool
}

// NewNet builds a Net from a graph and the prefixes each node holds. Delivery is
// to any address inside a prefix the node holds (owned or local); adjacency is
// every edge of the graph, up or down; a link is up if its edges are not marked
// down.
//
// Example: with the three-site graph and its prefixes, 10.42.240.3 is local to
// dgx-spark, builder-1 and dgx-spark are adjacent over builder-1~dgx-spark, and
// that link is up unless the graph marks it down.
func NewNet(graph route.Graph, tables route.Tables, owned, local route.Owned) Net {
	held := map[types.NodeID][]netip.Prefix{}
	for _, source := range []route.Owned{owned, local} {
		for node, prefixes := range source {
			held[node] = append(held[node], prefixes...)
		}
	}
	adjacent := map[adjacency]bool{}
	up := map[types.LinkID]bool{}
	for _, node := range graph.Nodes() {
		for _, edge := range graph.Edges(node) {
			adjacent[adjacency{node, edge.To, edge.Link}] = true
			if !edge.Down {
				up[edge.Link] = true
			}
		}
	}
	return Net{
		Tables: tables,
		Local: func(node types.NodeID, addr netip.Addr) bool {
			for _, prefix := range held[node] {
				if prefix.Contains(addr) {
					return true
				}
			}
			return false
		},
		Adjacent: func(from, to types.NodeID, link types.LinkID) bool { return adjacent[adjacency{from, to, link}] },
		Up:       func(link types.LinkID) bool { return up[link] },
	}
}

// adjacency identifies one directed edge.
type adjacency struct {
	// from, to and link are the two nodes and the link between them.
	from, to types.NodeID
	link     types.LinkID
}

// Send forwards one packet from start, node by node, until it is delivered or
// something stops it, and records every step.
//
// At each node, in this order:
//  1. If the destination is the node's own, the packet is delivered.
//  2. If the hop limit is used up, the packet is dropped: TTL exceeded.
//  3. If the packet has been at this node before, it is in a loop. Forwarding
//     is deterministic and the destination is fixed, so a packet that returns
//     to a node will repeat the same journey for ever: this test is exact, and
//     it fires long before the hop limit does.
//  4. The node looks up the destination in its table. No route is a black hole.
//  5. The route's next hop must be a real neighbour over the route's link,
//     otherwise the tables and the graph disagree: wrong neighbour.
//  6. The link must be up, otherwise a failover did not happen: link down.
//  7. Otherwise the packet is forwarded.
//
// The hop limit is a second, independent safety net: the packet's TTL, or
// DefaultTTL if it is zero. A packet may be forwarded TTL times, and may be
// delivered at the end of the last one.
//
// The trace ends with the node where the journey stopped: a loop shows the
// repeated node as its last hop, and a failure shows the route that failed.
// Send never panics and always returns, whatever the tables hold.
//
// Example: builder-1 sending to 10.42.240.3 in the three-site network with the
// direct link up goes builder-1, then dgx-spark, delivered at the second hop.
func (n Net) Send(packet Packet, start types.NodeID) Trace {
	if packet.TTL == 0 {
		packet.TTL = DefaultTTL
	}
	trace := Trace{Packet: packet, Hops: []Hop{}}
	finish := func(node types.NodeID, found *route.Route, result HopResult) Trace {
		trace.Hops = append(trace.Hops, Hop{Node: node, Route: found, Result: result})
		trace.Final = result
		return trace
	}
	seen := map[types.NodeID]bool{}
	current := start
	for remaining := packet.TTL; ; remaining-- {
		if n.Local != nil && n.Local(current, packet.Dst) {
			return finish(current, nil, Delivered)
		}
		if remaining <= 0 {
			return finish(current, nil, TTLExceeded)
		}
		if seen[current] {
			return finish(current, nil, LoopDetected)
		}
		seen[current] = true
		found, ok := n.Tables[current].Lookup(packet.Dst)
		if !ok {
			return finish(current, nil, BlackHole)
		}
		hop := found
		switch {
		case n.Adjacent == nil || !n.Adjacent(current, hop.NextHop, hop.Via):
			return finish(current, &hop, WrongNeighbour)
		case n.Up != nil && !n.Up(hop.Via):
			return finish(current, &hop, LinkDown)
		}
		trace.Hops = append(trace.Hops, Hop{Node: current, Route: &hop, Result: Forwarded})
		current = hop.NextHop
	}
}
