package plan

import (
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// graph adapts a topology graph to the routing engine's interface. route cannot import
// topology, so the planner, which knows both, joins them: an edge the topology graph marks
// unusable is an edge the engine sees as down.
type graph struct {
	// g is the topology graph, with the unusable links marked.
	g *topology.Graph
}

// adapt wraps a topology graph so the routing engine can use it.
func adapt(g *topology.Graph) route.Graph { return graph{g} }

// Nodes returns the topology's node IDs.
//
// Example:
//
//	the nodes of the adapted graph are the topology's.
func (a graph) Nodes() []types.NodeID { return a.g.Nodes() }

// Role returns a node's role.
//
// Example:
//
//	the role of aws-hub in the adapted graph is hub.
func (a graph) Role(node types.NodeID) types.Role { return a.g.Role(node) }

// Edges returns every edge out of node, marking the unusable ones down.
//
// Example:
//
//	with the direct link unusable, builder-1's edge to dgx-spark is Down.
func (a graph) Edges(node types.NodeID) []route.Edge {
	var out []route.Edge
	for _, e := range a.g.Neighbours(node) {
		out = append(out, route.Edge{Link: e.Link, To: e.To, Cost: e.Cost, Down: !e.Usable})
	}
	return out
}
