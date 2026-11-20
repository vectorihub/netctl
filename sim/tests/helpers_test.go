package tests

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// prefixOf parses a prefix, panicking on bad text because test data is fixed.
var prefixOf = netip.MustParsePrefix

// addrOf parses an address, panicking on bad text because test data is fixed.
var addrOf = netip.MustParseAddr

// The overlay addresses of the three-site network.
var (
	hubAddr = addrOf("10.42.240.1")
	b1Addr  = addrOf("10.42.240.2")
	dgxAddr = addrOf("10.42.240.3")
)

// plan is a computed three-site plan: the graph it was computed from, what each
// node holds, and the tables the engine produced.
type plan struct {
	// graph is the network the tables were computed for.
	graph *fixture.Graph
	// owned and local are what each node holds.
	owned, local route.Owned
	// result is the engine's output.
	result route.Result
	// tables is result.Tables, kept for planting faults in.
	tables route.Tables
}

// computePlan computes the tables of the three-site network over graph.
func computePlan(t *testing.T, graph *fixture.Graph) plan {
	t.Helper()
	input := fixture.ThreeSiteInput()
	result, err := route.Compute(graph, input)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return plan{graph: graph, owned: input.Owned, local: input.Local, result: result, tables: result.Tables}
}

// net returns the simulator over the plan's own graph: the network the tables
// were computed for.
func (p plan) net() sim.Net { return sim.NewNet(p.graph, p.tables, p.owned, p.local) }

// netOver returns the simulator for the plan's tables running over a different
// network, for example one whose link has since gone down.
func (p plan) netOver(graph *fixture.Graph) sim.Net {
	return sim.NewNet(graph, p.tables, p.owned, p.local)
}

// overlayFlow builds the overlay flow from one node to another, using the
// three-site overlay addresses.
func overlayFlow(from, to types.NodeID) sim.Flow {
	addr := map[types.NodeID]netip.Addr{"aws-hub": hubAddr, "builder-1": b1Addr, "dgx-spark": dgxAddr}
	return sim.Flow{
		Label: string(from) + "→" + string(to) + " (overlay)", Family: sim.FamilyOverlay,
		Src: addr[from], Dst: addr[to], From: from, To: to,
	}
}

// dropRoutes removes every route, active or not, for the given prefixes from a
// node's table. It plants a missing route.
func dropRoutes(tables route.Tables, node types.NodeID, prefixes ...string) {
	table := tables[node]
	table.Routes = slices.DeleteFunc(table.Routes, func(r route.Route) bool {
		return slices.ContainsFunc(prefixes, func(p string) bool { return r.Prefix == prefixOf(p) })
	})
}

// retarget points every route of a node for prefix at another next hop over
// another link, active or not. It plants a wrong next hop, or a loop.
func retarget(tables route.Tables, node types.NodeID, prefix string, nextHop types.NodeID, via types.LinkID) {
	table := tables[node]
	for i := range table.Routes {
		if table.Routes[i].Prefix == prefixOf(prefix) {
			table.Routes[i].NextHop, table.Routes[i].Via = nextHop, via
		}
	}
}

// addRoute adds an active computed route to a node's table.
func addRoute(tables route.Tables, node types.NodeID, prefix string, nextHop types.NodeID) {
	tables[node].Routes = append(tables[node].Routes, route.Route{
		Prefix: prefixOf(prefix), NextHop: nextHop, Via: types.LinkIDOf(node, nextHop),
		Metric: 1, Source: route.SourceComputed, Active: true,
	})
}

// journey builds a trace through the nodes: each node but the last forwards the
// packet over the link joining it to the next, and the last ends the journey with
// final. It is for tests of judgement, which need traces of exact shapes.
func journey(final sim.HopResult, nodes ...types.NodeID) sim.Trace {
	trace := sim.Trace{Final: final, Hops: []sim.Hop{}}
	for i, node := range nodes {
		hop := sim.Hop{Node: node, Result: final}
		if i < len(nodes)-1 {
			hop.Result = sim.Forwarded
			hop.Route = &route.Route{NextHop: nodes[i+1], Via: types.LinkIDOf(node, nodes[i+1])}
		}
		trace.Hops = append(trace.Hops, hop)
	}
	return trace
}

// verdictOf runs one overlay flow and returns its result.
func verdictOf(n sim.Net, from, to types.NodeID) sim.FlowResult {
	return n.Run(overlayFlow(from, to), false)
}
