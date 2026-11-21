package route

import (
	"math"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// Edge is one direction of a link, seen from the node it leaves: a way to get
// to a neighbour.
//
// A link between A and B gives two edges, A to B and B to A, with the same
// Link ID.
type Edge struct {
	// Link is the link this edge belongs to.
	Link types.LinkID
	// To is the neighbour at the far end.
	To types.NodeID
	// Cost is what it costs to cross the link. It must not be negative.
	Cost int
	// Down says the link cannot carry traffic right now. Shortest-path search
	// skips a down edge, but the edge is still listed so that a static route
	// can tell "not a neighbour" (a mistake) from "a neighbour over a link
	// that is down" (a missed failover). The zero value is usable, so a
	// simple Graph that never sets it routes over every edge.
	Down bool
}

// Graph is what route needs to know about a network: its nodes, their roles
// and the links between them. It is deliberately small, so that routing can be
// tested with a ten-line fake and so that this package does not depend on the
// topology package. The planner adapts topology.Graph to it.
type Graph interface {
	// Nodes returns every node ID. Order does not matter; Compute sorts them.
	Nodes() []types.NodeID
	// Role returns a node's role. Only a hub forwards traffic between other
	// nodes; a node whose role is unknown never does.
	Role(node types.NodeID) types.Role
	// Edges returns every edge out of node, usable or not (see Edge.Down).
	// Order does not matter: the result never depends on it.
	Edges(node types.NodeID) []Edge
}

// Best is the best path from one source to one node.
type Best struct {
	// Cost is the total cost of the path.
	Cost int
	// Hops is the number of links on the path.
	Hops int
	// First is the neighbour of the source the path starts with. It is the
	// next hop.
	First types.NodeID
	// Link is the first link of the path, the one a packet leaves on.
	Link types.LinkID
	// Path lists every node on the path, from the source to the destination.
	Path []types.NodeID
}

// addCost adds an edge's cost to a path's, stopping at math.MaxInt instead of
// wrapping round to a negative number. Compute refuses graphs whose costs could
// get there (see MaxTotalCost); this keeps SPF, which returns no error, from
// ever producing a negative cost for a graph it was handed directly.
func addCost(path, edge int) int {
	if edge > math.MaxInt-path {
		return math.MaxInt
	}
	return path + edge
}

// none marks "no link to leave out" and "no predecessor".
const none int32 = -1

// arc is one usable edge in integer form.
type arc struct {
	// to is the index of the neighbour.
	to int32
	// link is the rank of the link ID among all link IDs, so that comparing
	// ranks is the same as comparing the IDs.
	link int32
	// cost is the edge's cost.
	cost int
}

// index is a Graph copied into integers, so the search compares numbers and not
// strings. Nodes are numbered in the order of their IDs and links in the order
// of theirs, so comparing the numbers gives the same answer as comparing the
// names. Only usable edges (not down, not negative) are kept: the search could
// never use the others.
type index struct {
	// nodes lists the node IDs, sorted and without repeats.
	nodes []types.NodeID
	// position maps a node ID to its number.
	position map[types.NodeID]int32
	// forwards says whether the node may pass traffic for others: only hubs.
	forwards []bool
	// arcs holds each node's usable edges.
	arcs [][]arc
	// links lists the link IDs by rank.
	links []types.LinkID
	// rank maps a link ID to its rank.
	rank map[types.LinkID]int32
}

// newIndex copies graph into integer form. Edges that lead to a node the graph
// does not list are ignored.
func newIndex(graph Graph) *index {
	ix := &index{position: map[types.NodeID]int32{}, rank: map[types.LinkID]int32{}}
	ix.nodes = slices.Clone(graph.Nodes())
	slices.Sort(ix.nodes)
	ix.nodes = slices.Compact(ix.nodes)
	for i, node := range ix.nodes {
		ix.position[node] = int32(i)
	}
	ix.forwards = make([]bool, len(ix.nodes))
	ix.arcs = make([][]arc, len(ix.nodes))
	var ids []types.LinkID
	for i, node := range ix.nodes {
		ix.forwards[i] = graph.Role(node) == types.RoleHub
		for _, edge := range graph.Edges(node) {
			if _, known := ix.position[edge.To]; !known || edge.Down || edge.Cost < 0 {
				continue
			}
			ids = append(ids, edge.Link)
		}
	}
	slices.Sort(ids)
	ix.links = slices.Compact(ids)
	for r, id := range ix.links {
		ix.rank[id] = int32(r)
	}
	for i, node := range ix.nodes {
		for _, edge := range graph.Edges(node) {
			to, known := ix.position[edge.To]
			if !known || edge.Down || edge.Cost < 0 {
				continue
			}
			ix.arcs[i] = append(ix.arcs[i], arc{to: to, link: ix.rank[edge.Link], cost: edge.Cost})
		}
	}
	return ix
}

// stage says how far the search has got with a node.
type stage uint8

const (
	// unseen means no path to the node has been found yet.
	unseen stage = iota
	// tentative means a path has been found but a better one may still come.
	tentative
	// settled means the best path is known and final.
	settled
)

// entry is a node waiting in the search queue, with the cost and hops it had
// when it was put there.
type entry struct {
	// cost and hops are the key the queue orders by.
	cost, hops int
	// node is the node's number.
	node int32
}

// before orders queue entries by cost, then hops, then node number.
func (e entry) before(other entry) bool {
	if e.cost != other.cost {
		return e.cost < other.cost
	}
	if e.hops != other.hops {
		return e.hops < other.hops
	}
	return e.node < other.node
}

// search holds the result of one shortest-path run, and the scratch space to
// make the next run cheap. Everything is indexed by node number.
type search struct {
	// ix is the graph being searched.
	ix *index
	// src is the node the run started from.
	src int32
	// cost, hops, first, link and pred describe each node's best path: its
	// total cost, number of links, first node after the source, first link and
	// the node just before it on the path.
	cost, hops        []int
	first, link, pred []int32
	// state says how far each node has got.
	state []stage
	// queue is the binary heap of waiting nodes.
	queue []entry
	// left and right are scratch space for comparing two paths.
	left, right []int32
}

// newSearch makes a search over ix, ready to run.
func newSearch(ix *index) *search {
	n := len(ix.nodes)
	return &search{
		ix:    ix,
		cost:  make([]int, n),
		hops:  make([]int, n),
		first: make([]int32, n),
		link:  make([]int32, n),
		pred:  make([]int32, n),
		state: make([]stage, n),
	}
}

// push adds an entry to the queue.
func (s *search) push(e entry) {
	s.queue = append(s.queue, e)
	child := len(s.queue) - 1
	for child > 0 {
		parent := (child - 1) / 2
		if !s.queue[child].before(s.queue[parent]) {
			break
		}
		s.queue[child], s.queue[parent] = s.queue[parent], s.queue[child]
		child = parent
	}
}

// pop removes and returns the best entry in the queue.
func (s *search) pop() entry {
	top := s.queue[0]
	last := len(s.queue) - 1
	s.queue[0] = s.queue[last]
	s.queue = s.queue[:last]
	parent := 0
	for {
		smallest, left, right := parent, 2*parent+1, 2*parent+2
		if left < last && s.queue[left].before(s.queue[smallest]) {
			smallest = left
		}
		if right < last && s.queue[right].before(s.queue[smallest]) {
			smallest = right
		}
		if smallest == parent {
			return top
		}
		s.queue[parent], s.queue[smallest] = s.queue[smallest], s.queue[parent]
		parent = smallest
	}
}

// pathInto writes the path from the source to node, in order, into buf and
// returns it.
func (s *search) pathInto(node int32, buf []int32) []int32 {
	buf = buf[:0]
	for at := node; at != none; at = s.pred[at] {
		buf = append(buf, at)
	}
	slices.Reverse(buf)
	return buf
}

// pathBeats reports whether the path to a is lexically smaller than the path to
// b. Both are settled nodes with the same number of hops, so the paths have the
// same length.
func (s *search) pathBeats(a, b int32) bool {
	s.left = s.pathInto(a, s.left)
	s.right = s.pathInto(b, s.right)
	return slices.Compare(s.left, s.right) < 0
}

// run finds the best path from src to every node it can reach. The link
// without, if it is not none, is left out. The result is in the search's
// arrays: a node is reachable if its state is settled.
//
// The order that decides between equal-cost paths is: cost, hops, first node,
// first link, then the list of nodes. It is applied when a second path to a
// node is found, and not in the queue, which is ordered by cost and hops
// alone. That is safe because every path into a node comes from a node with a
// strictly smaller cost and hops, which is settled before the node is.
func (s *search) run(src, without int32) {
	for i := range s.state {
		s.state[i] = unseen
	}
	s.queue = s.queue[:0]
	s.src = src
	s.cost[src], s.hops[src], s.pred[src] = 0, 0, none
	s.first[src], s.link[src] = none, none
	s.state[src] = tentative
	s.push(entry{node: src})
	for len(s.queue) > 0 {
		current := s.pop()
		node := current.node
		if s.state[node] == settled || current.cost != s.cost[node] || current.hops != s.hops[node] {
			continue
		}
		s.state[node] = settled
		if node != src && !s.ix.forwards[node] {
			continue
		}
		for _, edge := range s.ix.arcs[node] {
			if edge.link == without || s.state[edge.to] == settled {
				continue
			}
			cost, hops := addCost(s.cost[node], edge.cost), s.hops[node]+1
			first, link := s.first[node], s.link[node]
			if node == src {
				first, link = edge.to, edge.link
			}
			switch {
			case s.state[edge.to] == unseen || cost < s.cost[edge.to] || (cost == s.cost[edge.to] && hops < s.hops[edge.to]):
				s.cost[edge.to], s.hops[edge.to] = cost, hops
				s.first[edge.to], s.link[edge.to], s.pred[edge.to] = first, link, node
				s.state[edge.to] = tentative
				s.push(entry{cost: cost, hops: hops, node: edge.to})
			case cost == s.cost[edge.to] && hops == s.hops[edge.to] && s.tiePrefers(edge.to, node, first, link):
				s.first[edge.to], s.link[edge.to], s.pred[edge.to] = first, link, node
			}
		}
	}
}

// tiePrefers reports whether a path to target that goes through via, with the
// given first node and first link, beats the path now recorded for target, when
// the two cost the same and have the same number of hops: the smaller first
// node wins, then the smaller first link, then the lexically smaller path.
func (s *search) tiePrefers(target, via, first, link int32) bool {
	switch {
	case first != s.first[target]:
		return first < s.first[target]
	case link != s.link[target]:
		return link < s.link[target]
	case via == s.pred[target]:
		return false
	}
	return s.pathBeats(via, s.pred[target])
}

// best builds the Best for a settled node, with its own copy of the path.
func (s *search) best(node int32) Best {
	path := make([]types.NodeID, s.hops[node]+1)
	for at, i := node, len(path)-1; at != none; at, i = s.pred[at], i-1 {
		path[i] = s.ix.nodes[at]
	}
	return Best{
		Cost:  s.cost[node],
		Hops:  s.hops[node],
		First: s.ix.nodes[s.first[node]],
		Link:  s.ix.links[s.link[node]],
		Path:  path,
	}
}

// SPF finds the best path from src to every node it can reach, using
// Dijkstra's algorithm. The result is keyed by destination node and does not
// include src itself.
//
// Edges marked Down, and edges with a negative cost, are ignored, as are edges
// that lead to a node the graph does not list. If without is not empty, that
// link is ignored in both directions; Compute uses this to find a backup path
// that avoids the primary's first link.
//
// Two rules decide which paths exist at all:
//   - A spoke never forwards for others. A path may end at a spoke but never
//     pass through one, and the only spoke whose edges are used is src
//     itself. A hub may forward. A node whose role is unknown never does.
//     This is what keeps the network hub-and-spoke instead of an accidental
//     mesh.
//   - Every path is simple: it visits each node at most once.
//
// When several paths to a node cost the same, the choice is the same on every
// run: fewer hops, then the lexically smaller next hop, then the smaller
// first link, then the lexically smaller list of nodes (see Best).
//
// Example: with spokes a and c joined by a hub h, and a direct link between a
// and b but none between b and c, SPF from a reaches c through h and not
// through b.
func SPF(graph Graph, src types.NodeID, without types.LinkID) map[types.NodeID]Best {
	ix := newIndex(graph)
	start, known := ix.position[src]
	if !known {
		return map[types.NodeID]Best{}
	}
	skip, hasLink := ix.rank[without]
	if !hasLink {
		skip = none
	}
	s := newSearch(ix)
	s.run(start, skip)
	out := make(map[types.NodeID]Best, len(ix.nodes))
	for node := range ix.nodes {
		if int32(node) != start && s.state[node] == settled {
			out[ix.nodes[node]] = s.best(int32(node))
		}
	}
	return out
}
