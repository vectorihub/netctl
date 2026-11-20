package sim_test

// This file holds the runnable examples shown on the package documentation. They
// use only the exported API. The routing tables are written out by hand so that
// each example shows the simulator on its own.

import (
	"fmt"
	"net/netip"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// network is a small route.Graph: builder-1 and dgx-spark are spokes of aws-hub,
// and also joined directly when direct is true.
type network struct {
	// direct says whether the direct link exists.
	direct bool
}

// Nodes returns the three nodes.
func (network) Nodes() []types.NodeID { return []types.NodeID{"aws-hub", "builder-1", "dgx-spark"} }

// Role returns hub for aws-hub and spoke for the others.
func (network) Role(node types.NodeID) types.Role {
	if node == "aws-hub" {
		return types.RoleHub
	}
	return types.RoleSpoke
}

// Edges returns the links out of a node.
func (n network) Edges(node types.NodeID) []route.Edge {
	var edges []route.Edge
	link := func(a, b types.NodeID, cost int) {
		if node == a {
			edges = append(edges, route.Edge{Link: types.LinkIDOf(a, b), To: b, Cost: cost})
		}
		if node == b {
			edges = append(edges, route.Edge{Link: types.LinkIDOf(a, b), To: a, Cost: cost})
		}
	}
	link("aws-hub", "builder-1", 20)
	link("aws-hub", "dgx-spark", 20)
	if n.direct {
		link("builder-1", "dgx-spark", 10)
	}
	return edges
}

// owned is what each node holds: its overlay address.
func owned() route.Owned {
	return route.Owned{
		"aws-hub":   {netip.MustParsePrefix("10.42.240.1/32")},
		"builder-1": {netip.MustParsePrefix("10.42.240.2/32")},
		"dgx-spark": {netip.MustParsePrefix("10.42.240.3/32")},
	}
}

// build computes the tables for a network and returns the simulator over them.
func build(direct bool) sim.Net {
	g := network{direct: direct}
	result, _ := route.Compute(g, route.Input{Owned: owned()})
	return sim.NewNet(g, result.Tables, owned(), nil)
}

// printTrace writes a trace as its nodes and how it ended.
func printTrace(trace sim.Trace) {
	for i, hop := range trace.Hops {
		if i > 0 {
			fmt.Print(" > ")
		}
		fmt.Print(hop.Node)
	}
	fmt.Println(":", trace.Final)
}

// ExampleNet_Send shows a packet crossing the network. With the direct link it
// goes straight from builder-1 to dgx-spark; without it, through the hub.
func ExampleNet_Send() {
	packet := sim.Packet{Src: netip.MustParseAddr("10.42.240.2"), Dst: netip.MustParseAddr("10.42.240.3")}
	printTrace(build(true).Send(packet, "builder-1"))
	printTrace(build(false).Send(packet, "builder-1"))
	// Output:
	// builder-1 > dgx-spark: DELIVERED
	// builder-1 > aws-hub > dgx-spark: DELIVERED
}

// ExampleNet_Send_loop shows a planted fault. The hub is made to send
// dgx-spark's address back to builder-1, which sends it to the hub: the packet
// is caught going round, at the second visit to builder-1, long before its hop
// limit runs out.
func ExampleNet_Send_loop() {
	g := network{}
	result, _ := route.Compute(g, route.Input{Owned: owned()})
	for i, r := range result.Tables["aws-hub"].Routes {
		if r.Prefix == netip.MustParsePrefix("10.42.240.3/32") {
			result.Tables["aws-hub"].Routes[i].NextHop = "builder-1"
			result.Tables["aws-hub"].Routes[i].Via = "aws-hub~builder-1"
		}
	}
	net := sim.NewNet(g, result.Tables, owned(), nil)
	trace := net.Send(sim.Packet{Dst: netip.MustParseAddr("10.42.240.3"), TTL: 64}, "builder-1")
	printTrace(trace)
	// Output:
	// builder-1 > aws-hub > builder-1: LOOP
}

// ExampleNet_Run shows a whole flow: the packet, its reply and the verdict. The
// reply takes the same path reversed, so the flow is OK.
func ExampleNet_Run() {
	flow := sim.Flow{
		Label: "builder-1→dgx-spark", Family: sim.FamilyOverlay,
		Src: netip.MustParseAddr("10.42.240.2"), Dst: netip.MustParseAddr("10.42.240.3"),
		From: "builder-1", To: "dgx-spark",
	}
	result := build(false).Run(flow, false)
	printTrace(result.Forward)
	printTrace(*result.Return)
	fmt.Println("symmetric:", result.Symmetric, "verdict:", result.Verdict)
	// Output:
	// builder-1 > aws-hub > dgx-spark: DELIVERED
	// dgx-spark > aws-hub > builder-1: DELIVERED
	// symmetric: true verdict: OK
}

// ExampleNet_Run_noReturn shows a missing return path. The hub has lost its
// route to builder-1, so the packet reaches dgx-spark but the reply is dropped at
// the hub.
func ExampleNet_Run_noReturn() {
	g := network{}
	result, _ := route.Compute(g, route.Input{Owned: owned()})
	table := result.Tables["aws-hub"]
	for i := len(table.Routes) - 1; i >= 0; i-- {
		if table.Routes[i].Prefix == netip.MustParsePrefix("10.42.240.2/32") {
			table.Routes = append(table.Routes[:i], table.Routes[i+1:]...)
		}
	}
	net := sim.NewNet(g, result.Tables, owned(), nil)
	flow := sim.Flow{
		Label: "builder-1→dgx-spark", Family: sim.FamilyOverlay,
		Src: netip.MustParseAddr("10.42.240.2"), Dst: netip.MustParseAddr("10.42.240.3"),
		From: "builder-1", To: "dgx-spark",
	}
	got := net.Run(flow, false)
	printTrace(*got.Return)
	fmt.Println(got.Verdict, "fails the gate:", got.Verdict.FailsGate())
	fmt.Println(got.Problems[0].Detail)
	// Output:
	// dgx-spark > aws-hub: BLACK_HOLE
	// NO_RETURN fails the gate: true
	// aws-hub has no route for 10.42.240.2
}

// ExampleJudge shows how a forward and a return trace become a verdict.
func ExampleJudge() {
	net := build(false)
	packet := sim.Packet{Src: netip.MustParseAddr("10.42.240.2"), Dst: netip.MustParseAddr("10.42.240.3")}
	forward := net.Send(packet, "builder-1")
	back := net.Send(sim.Packet{Src: packet.Dst, Dst: packet.Src}, "dgx-spark")

	fmt.Println(sim.Judge(forward, back, sim.Case{}))
	fmt.Println(sim.Symmetric(forward, back))
	// Output:
	// OK
	// true
}

// ExampleFlows shows the flows derived for a network. The topology and address
// plan are built in code here; in practice the topology comes from topology.Load.
func ExampleFlows() {
	t := &topology.Topology{
		Schema: topology.Schema,
		Sites:  []topology.Site{{ID: "aws"}, {ID: "builder"}},
		Nodes: []topology.Node{
			{ID: "aws-hub", Site: "aws", Role: types.RoleHub, Advertises: []topology.Advertisement{{Site: true}}},
			{ID: "builder-1", Site: "builder", Role: types.RoleSpoke, Advertises: []topology.Advertisement{{Site: true}}},
		},
		Links: []topology.Link{{Kind: types.LinkHubSpoke, A: "aws-hub", B: "builder-1"}},
	}
	t.Normalize()
	registry, _ := ipam.New(ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: netip.MustParsePrefix("10.42.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
			{Name: "overlay", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
		},
	}, &types.FixedClock{}, ipam.DefaultQuarantine)
	registry.Allocate("sites", ipam.Site("aws"))
	registry.Allocate("sites", ipam.Site("builder"))
	registry.Allocate("overlay", ipam.Node("aws-hub"))
	registry.Allocate("overlay", ipam.Node("builder-1"))

	flows, _ := sim.Flows(t, registry, sim.Options{})
	for _, flow := range flows {
		fmt.Println(flow.Label)
	}
	// Output:
	// aws-hub→192.168.1.1 (negative)
	// aws-hub→builder-1 (overlay)
	// aws→builder (site)
	// builder-1→192.168.1.1 (negative)
	// builder-1→aws-hub (overlay)
	// builder→aws (site)
}

// ExampleNewReport shows gathering results into a report: sorted by label,
// counted, and passed only if no flow fails the gate.
func ExampleNewReport() {
	net := build(false)
	var results []sim.FlowResult
	for _, pair := range [][2]types.NodeID{{"dgx-spark", "builder-1"}, {"builder-1", "dgx-spark"}} {
		addr := map[types.NodeID]string{"builder-1": "10.42.240.2", "dgx-spark": "10.42.240.3"}
		results = append(results, net.Run(sim.Flow{
			Label: string(pair[0]) + "→" + string(pair[1]), Family: sim.FamilyOverlay,
			Src: netip.MustParseAddr(addr[pair[0]]), Dst: netip.MustParseAddr(addr[pair[1]]), From: pair[0], To: pair[1],
		}, false))
	}
	report := sim.NewReport(results)
	fmt.Println(report.Flows[0].Flow.Label, "then", report.Flows[1].Flow.Label)
	fmt.Println("flows:", report.Summary.Total, "OK:", report.Summary.Count(sim.VerdictOK), "passed:", report.Passed)
	// Output:
	// builder-1→dgx-spark then dgx-spark→builder-1
	// flows: 2 OK: 2 passed: true
}
