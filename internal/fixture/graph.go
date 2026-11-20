package fixture

import (
	"math/rand/v2"
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// Graph is a small route.Graph that tests build by hand. It is the "ten-line
// fake" the routing engine is meant to be testable with: nodes with roles, and
// links with costs.
type Graph struct {
	// order lists the nodes in the order they were added.
	order []types.NodeID
	// roles holds each node's role.
	roles map[types.NodeID]types.Role
	// edges holds each node's outgoing edges in the order they were added.
	edges map[types.NodeID][]route.Edge
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{roles: map[types.NodeID]types.Role{}, edges: map[types.NodeID][]route.Edge{}}
}

// Node adds a node with a role and returns the graph, so calls chain.
func (g *Graph) Node(id types.NodeID, role types.Role) *Graph {
	if _, seen := g.roles[id]; !seen {
		g.order = append(g.order, id)
	}
	g.roles[id] = role
	return g
}

// Hub adds a hub node and returns the graph.
func (g *Graph) Hub(ids ...types.NodeID) *Graph {
	for _, id := range ids {
		g.Node(id, types.RoleHub)
	}
	return g
}

// Spoke adds a spoke node and returns the graph.
func (g *Graph) Spoke(ids ...types.NodeID) *Graph {
	for _, id := range ids {
		g.Node(id, types.RoleSpoke)
	}
	return g
}

// Link joins two nodes with a link of the given cost, as two edges, and
// returns the link's ID.
func (g *Graph) Link(a, b types.NodeID, cost int) types.LinkID {
	id := types.LinkIDOf(a, b)
	g.edges[a] = append(g.edges[a], route.Edge{Link: id, To: b, Cost: cost})
	g.edges[b] = append(g.edges[b], route.Edge{Link: id, To: a, Cost: cost})
	return id
}

// Down marks both edges of a link as down.
func (g *Graph) Down(link types.LinkID) *Graph {
	for node, edges := range g.edges {
		for i := range edges {
			if edges[i].Link == link {
				g.edges[node][i].Down = true
			}
		}
	}
	return g
}

// Nodes returns the nodes in the order they were added.
func (g *Graph) Nodes() []types.NodeID { return slices.Clone(g.order) }

// Role returns a node's role.
func (g *Graph) Role(node types.NodeID) types.Role { return g.roles[node] }

// Edges returns every edge out of a node, in the order they were added.
func (g *Graph) Edges(node types.NodeID) []route.Edge { return slices.Clone(g.edges[node]) }

// Shuffled returns a copy with the nodes, and each node's edges, in a random
// order. A correct engine gives the same answer for all of them.
func (g *Graph) Shuffled(rng *rand.Rand) *Graph {
	out := NewGraph()
	out.order = slices.Clone(g.order)
	rng.Shuffle(len(out.order), func(i, j int) { out.order[i], out.order[j] = out.order[j], out.order[i] })
	for node, role := range g.roles {
		out.roles[node] = role
	}
	for node, edges := range g.edges {
		shuffled := slices.Clone(edges)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		out.edges[node] = shuffled
	}
	return out
}

// ThreeSite returns the reference network: aws-hub joined to builder-1 and
// dgx-spark by links of cost 20, and, when directUp is true, builder-1 joined
// to dgx-spark by a direct link of cost 10. When directUp is false the direct
// link does not exist at all.
func ThreeSite(directUp bool) *Graph {
	g := NewGraph().Hub("aws-hub").Spoke("builder-1", "dgx-spark")
	g.Link("aws-hub", "builder-1", 20)
	g.Link("aws-hub", "dgx-spark", 20)
	if directUp {
		g.Link("builder-1", "dgx-spark", 10)
	}
	return g
}

// ThreeSiteDirectDown returns the reference network with the direct link
// present but marked down, which is what a health failure looks like.
func ThreeSiteDirectDown() *Graph {
	return ThreeSite(true).Down(types.LinkIDOf("builder-1", "dgx-spark"))
}

// prefixOf parses a prefix, panicking on bad text because fixture data is fixed.
var prefixOf = netip.MustParsePrefix

// ThreeSiteOwned is what each node of the reference network advertises: its
// site block and its overlay address.
func ThreeSiteOwned() route.Owned {
	return route.Owned{
		"aws-hub":   {prefixOf("10.42.0.0/20"), prefixOf("10.42.240.1/32")},
		"builder-1": {prefixOf("10.42.16.0/20"), prefixOf("10.42.240.2/32")},
		"dgx-spark": {prefixOf("10.42.32.0/20"), prefixOf("10.42.240.3/32")},
	}
}

// ThreeSiteLocal is what is local to the ends of the direct link: its transit
// /30, held by both builder-1 and dgx-spark.
func ThreeSiteLocal() route.Owned {
	return route.Owned{
		"builder-1": {prefixOf("10.42.252.0/30")},
		"dgx-spark": {prefixOf("10.42.252.0/30")},
	}
}

// ThreeSiteInput returns the full routing input for the reference network.
func ThreeSiteInput() route.Input {
	return route.Input{Owned: ThreeSiteOwned(), Local: ThreeSiteLocal()}
}

// FromTopology adapts a topology.Graph to route.Graph. It is the adapter the
// planner will own; it lives here so tests can prove the two packages fit
// together without route importing topology.
func FromTopology(g *topology.Graph) route.Graph { return topologyGraph{g} }

// topologyGraph wraps a topology.Graph so that it satisfies route.Graph.
type topologyGraph struct {
	// g is the wrapped graph.
	g *topology.Graph
}

// Nodes returns the topology's node IDs.
func (t topologyGraph) Nodes() []types.NodeID { return t.g.Nodes() }

// Role returns a node's role.
func (t topologyGraph) Role(node types.NodeID) types.Role { return t.g.Role(node) }

// Edges returns every edge out of node, marking the unusable ones as down.
func (t topologyGraph) Edges(node types.NodeID) []route.Edge {
	var out []route.Edge
	for _, edge := range t.g.Neighbours(node) {
		out = append(out, route.Edge{Link: edge.Link, To: edge.To, Cost: edge.Cost, Down: !edge.Usable})
	}
	return out
}
