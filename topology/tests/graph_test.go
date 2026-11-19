package tests

import (
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// P1-TOPO-030: two directed edges per link, 6 for three-site.
func TestBuildGraphThreeSite(t *testing.T) {
	graph := topology.BuildGraph(decode(t, "three-site.yaml"), nil)
	if graph.EdgeCount() != 6 {
		t.Fatalf("%d edges, want 6", graph.EdgeCount())
	}
	if got, want := graph.Nodes(), []types.NodeID{"aws-hub", "builder-1", "dgx-spark"}; !reflect.DeepEqual(got, want) {
		t.Errorf("nodes %v", got)
	}
	if graph.Role("aws-hub") != types.RoleHub || graph.Role("builder-1") != types.RoleSpoke || graph.Role("ghost") != types.RoleUnknown {
		t.Error("roles")
	}
	got := graph.Neighbours("builder-1")
	want := []topology.Edge{
		{From: "builder-1", To: "aws-hub", Link: "aws-hub~builder-1", Kind: types.LinkHubSpoke, Cost: 20, Usable: true},
		{From: "builder-1", To: "dgx-spark", Link: "builder-1~dgx-spark", Kind: types.LinkDirect, Cost: 10, Usable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("neighbours = %+v", got)
	}
	// Every edge has its reverse, on the same link.
	for _, from := range graph.Nodes() {
		for _, edge := range graph.Neighbours(from) {
			if !graph.Adjacent(edge.To, edge.From, edge.Link) {
				t.Errorf("%+v has no reverse edge", edge)
			}
		}
	}
}

// P1-TOPO-031: Neighbours is sorted by (To, Link) and the same whatever order
// the topology was written in.
func TestNeighboursSortedAndStableUnderShuffle(t *testing.T) {
	base := decode(t, "three-site.yaml")
	// Add a second link between a pair so the Link tie-break is used.
	base.Links = append(base.Links, topology.Link{Kind: types.LinkHubSpoke, A: "builder-1", B: "dgx-spark", Cost: 7})
	base.Normalize()
	want := topology.BuildGraph(base, nil)
	rng := rand.New(rand.NewPCG(1, 2))
	for range 100 {
		shuffled := *base
		shuffled.Nodes = append([]topology.Node(nil), base.Nodes...)
		shuffled.Links = append([]topology.Link(nil), base.Links...)
		rng.Shuffle(len(shuffled.Nodes), func(i, j int) { shuffled.Nodes[i], shuffled.Nodes[j] = shuffled.Nodes[j], shuffled.Nodes[i] })
		rng.Shuffle(len(shuffled.Links), func(i, j int) { shuffled.Links[i], shuffled.Links[j] = shuffled.Links[j], shuffled.Links[i] })
		graph := topology.BuildGraph(&shuffled, nil)
		for _, node := range want.Nodes() {
			if !reflect.DeepEqual(graph.Neighbours(node), want.Neighbours(node)) {
				t.Fatalf("%s: %+v vs %+v", node, graph.Neighbours(node), want.Neighbours(node))
			}
		}
	}
	for _, node := range want.Nodes() {
		edges := want.Neighbours(node)
		for i := 1; i < len(edges); i++ {
			if edges[i-1].To > edges[i].To || (edges[i-1].To == edges[i].To && edges[i-1].Link > edges[i].Link) {
				t.Errorf("%s not sorted: %+v", node, edges)
			}
		}
	}
}

// P1-TOPO-032: the link ID is the same whichever end is named first.
func TestGraphLinkIDIgnoresWhichEndIsFirst(t *testing.T) {
	forward := topology.BuildGraph(&topology.Topology{
		Nodes: []topology.Node{{ID: "builder-1", Role: types.RoleSpoke}, {ID: "dgx-spark", Role: types.RoleSpoke}},
		Links: []topology.Link{{Kind: types.LinkDirect, A: "builder-1", B: "dgx-spark", Cost: 10}},
	}, nil)
	backward := topology.BuildGraph(&topology.Topology{
		Nodes: []topology.Node{{ID: "dgx-spark", Role: types.RoleSpoke}, {ID: "builder-1", Role: types.RoleSpoke}},
		Links: []topology.Link{{Kind: types.LinkDirect, A: "dgx-spark", B: "builder-1", Cost: 10}},
	}, nil)
	for _, graph := range []*topology.Graph{forward, backward} {
		if e := graph.Neighbours("builder-1"); len(e) != 1 || e[0].Link != "builder-1~dgx-spark" {
			t.Errorf("%+v", e)
		}
	}
	if !reflect.DeepEqual(forward.Neighbours("dgx-spark"), backward.Neighbours("dgx-spark")) {
		t.Error("graphs differ")
	}
}

// Health: Edges drops edges over unusable links, Neighbours and Adjacent keep them.
func TestGraphHealthView(t *testing.T) {
	topo := decode(t, "three-site.yaml")
	down := func(id types.LinkID) bool { return id != "builder-1~dgx-spark" }
	graph := topology.BuildGraph(topo, down)
	if edges := graph.Edges("builder-1"); len(edges) != 1 || edges[0].To != "aws-hub" {
		t.Errorf("usable edges %+v", edges)
	}
	if all := graph.Neighbours("builder-1"); len(all) != 2 || all[1].Usable {
		t.Errorf("all edges %+v", all)
	}
	if !graph.Adjacent("builder-1", "dgx-spark", "builder-1~dgx-spark") {
		t.Error("Adjacent must ignore health")
	}
	if graph.Adjacent("builder-1", "dgx-spark", "aws-hub~builder-1") || graph.Adjacent("builder-1", "ghost", "x") {
		t.Error("Adjacent matched the wrong link or node")
	}
	if graph.EdgeCount() != 6 {
		t.Error("an unusable link still has its edges")
	}
}

// Snapshot, copies, dangling links, nil.
func TestGraphIsASnapshotAndSkipsBrokenLinks(t *testing.T) {
	topo := decode(t, "three-site.yaml")
	topo.Links = append(topo.Links,
		topology.Link{Kind: types.LinkDirect, A: "builder-1", B: "ghost", Cost: 1},
		topology.Link{Kind: types.LinkDirect, A: "builder-1", B: "builder-1", Cost: 1})
	graph := topology.BuildGraph(topo, nil)
	if graph.EdgeCount() != 6 {
		t.Errorf("%d edges; dangling and self links must be left out", graph.EdgeCount())
	}
	topo.Links = nil
	topo.Nodes = nil
	if graph.EdgeCount() != 6 || len(graph.Nodes()) != 3 {
		t.Error("graph changed with the topology")
	}
	graph.Nodes()[0] = "x"
	graph.Neighbours("builder-1")[0].Cost = 99
	if graph.Nodes()[0] != "aws-hub" || graph.Neighbours("builder-1")[0].Cost != 20 {
		t.Error("callers can change the graph")
	}
	empty := topology.BuildGraph(nil, nil)
	if len(empty.Nodes()) != 0 || empty.EdgeCount() != 0 || empty.Edges("a") != nil {
		t.Error("nil topology")
	}
}
