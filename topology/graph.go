package topology

import (
	"cmp"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// Edge is one direction of a link: a way to get from one node to a neighbour.
// A link between A and B gives two edges, A to B and B to A, with the same Link
// ID, so later versions can give the two directions different costs or health.
type Edge struct {
	// From and To are the nodes at each end of this direction.
	From, To types.NodeID
	// Link is the link this edge belongs to.
	Link types.LinkID
	// Kind is the link's kind.
	Kind types.LinkKind
	// Cost is the link's cost.
	Cost int
	// Usable says whether the link was usable when the graph was built.
	Usable bool
}

// Graph is the adjacency view of a topology, for walking paths between nodes. It is a
// snapshot: it holds copies, so changing the Topology afterwards does not change
// it, and it is safe to read from many goroutines. Rebuild it when health changes.
type Graph struct {
	nodes []types.NodeID
	roles map[types.NodeID]types.Role
	adj   map[types.NodeID][]Edge
	edges int
}

// BuildGraph builds the graph of t. usable says whether a link may carry traffic
// right now; nil means every link may. It is called once per link while building.
//
// Every link gives two directed edges. A link that joins a node to itself or to a
// node that does not exist is left out, so build the graph from a topology that
// passed Check. Each node's edges are sorted by (To, Link), so the order never
// depends on how the topology was written. A nil Topology gives an empty graph.
//
// Example: the three-site topology gives 3 nodes and 6 edges.
func BuildGraph(t *Topology, usable func(types.LinkID) bool) *Graph {
	graph := &Graph{roles: map[types.NodeID]types.Role{}, adj: map[types.NodeID][]Edge{}}
	if t == nil {
		return graph
	}
	for _, node := range t.Nodes {
		if _, seen := graph.roles[node.ID]; !seen {
			graph.nodes = append(graph.nodes, node.ID)
		}
		graph.roles[node.ID] = node.Role
	}
	slices.Sort(graph.nodes)

	for _, link := range t.Links {
		_, hasA := graph.roles[link.A]
		_, hasB := graph.roles[link.B]
		if !hasA || !hasB || link.A == link.B {
			continue
		}
		id := types.LinkIDOf(link.A, link.B)
		ok := usable == nil || usable(id)
		graph.adj[link.A] = append(graph.adj[link.A], Edge{From: link.A, To: link.B, Link: id, Kind: link.Kind, Cost: link.Cost, Usable: ok})
		graph.adj[link.B] = append(graph.adj[link.B], Edge{From: link.B, To: link.A, Link: id, Kind: link.Kind, Cost: link.Cost, Usable: ok})
		graph.edges += 2
	}
	for _, edges := range graph.adj {
		slices.SortStableFunc(edges, func(a, b Edge) int {
			return cmp.Or(cmp.Compare(a.To, b.To), cmp.Compare(a.Link, b.Link), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Cost, b.Cost))
		})
	}
	return graph
}

// Nodes returns every node ID in byte order. The caller owns the slice.
func (g *Graph) Nodes() []types.NodeID { return slices.Clone(g.nodes) }

// Role returns a node's role, or RoleUnknown for a node that is not in the graph.
func (g *Graph) Role(node types.NodeID) types.Role { return g.roles[node] }

// EdgeCount returns the number of directed edges, usable or not.
func (g *Graph) EdgeCount() int { return g.edges }

// Neighbours returns every edge out of node, usable or not, sorted by (To, Link).
// The caller owns the slice. A node with no links, or not in the graph, has none.
func (g *Graph) Neighbours(node types.NodeID) []Edge { return slices.Clone(g.adj[node]) }

// Edges returns only the usable edges out of node, in the same order as
// Neighbours. This is what shortest-path search walks.
func (g *Graph) Edges(node types.NodeID) []Edge {
	var out []Edge
	for _, edge := range g.adj[node] {
		if edge.Usable {
			out = append(out, edge)
		}
	}
	return out
}

// Adjacent reports whether link joins from to to, whether or not the link is
// usable. It deliberately ignores health: the simulator must tell "the route
// points at a node that is not a neighbour" (a bug) from "it points at a
// neighbour over a down link" (a missed failover).
func (g *Graph) Adjacent(from, to types.NodeID, link types.LinkID) bool {
	return slices.ContainsFunc(g.adj[from], func(edge Edge) bool { return edge.To == to && edge.Link == link })
}
