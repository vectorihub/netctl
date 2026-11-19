package tests

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// P1-TOPO-020: Builder lists DGX Spark, DGX Spark does not list Builder.
func TestOneSidedPeerIsRefused(t *testing.T) {
	_, err := topology.Decode("adjacency-one-sided.yaml", fixture(t, "adjacency-one-sided.yaml"), 0)
	list := problems(t, err)
	if len(list) != 1 {
		t.Fatalf("%d problems, want exactly the one-sided pair (the agreeing links are not reported):\n%v", len(list), err)
	}
	problem := list[0]
	if problem.Code != types.ErrInvalidTopology || problem.Subject != "builder-1~dgx-spark" ||
		!strings.Contains(problem.Msg, "builder-1 lists dgx-spark as a peer, dgx-spark does not list builder-1") ||
		!strings.Contains(problem.Msg, "adjacency-one-sided.yaml") {
		t.Errorf("%v", problem)
	}
}

// P1-TOPO-021: both sides agree, one link per pair, and the model is exactly
// the one three-site.yaml describes with links.
func TestAgreeingPeersMakeTheSameTopologyAsLinks(t *testing.T) {
	fromPeers := decode(t, "adjacency.yaml")
	fromLinks := decode(t, "three-site.yaml")
	if len(fromPeers.Links) != 3 {
		t.Fatalf("%d links, want one per pair (3): %+v", len(fromPeers.Links), fromPeers.Links)
	}
	if !reflect.DeepEqual(fromPeers, fromLinks) {
		t.Errorf("peers form differs from links form:\n%+v\n%+v", fromPeers, fromLinks)
	}
	for _, node := range fromPeers.Nodes {
		if node.Peers != nil {
			t.Errorf("%s still has peers %v", node.ID, node.Peers)
		}
	}
	if _, report, err := topology.Load("adjacency.yaml", fixture(t, "adjacency.yaml"), 0); err != nil || len(report.Warnings()) != 1 {
		t.Errorf("Load: %v %v", err, report.Findings)
	}
}

// Repairing the one-sided file (the e2e follow-on step) gives the links form.
func TestAddingTheMissingPeerRepairsTheFile(t *testing.T) {
	text := strings.Replace(string(fixture(t, "adjacency-one-sided.yaml")), "peers: [aws-hub]            # missing builder-1", "peers: [aws-hub, builder-1]", 1)
	got, err := topology.Decode("fixed.yaml", []byte(text), 0)
	if err != nil || !reflect.DeepEqual(got, decode(t, "three-site.yaml")) {
		t.Errorf("%v", err)
	}
}

// node builds a Node with the given ID, site and role, listing peers.
func node(id, site string, role types.Role, peers ...types.NodeID) topology.Node {
	return topology.Node{ID: types.NodeID(id), Site: types.SiteID(site), Role: role, Peers: peers}
}

// TestPeerKindsComeFromRoles checks that hub to spoke becomes hub-spoke and spoke to spoke becomes
// direct, with defaults filled in.
func TestPeerKindsComeFromRoles(t *testing.T) {
	topo := &topology.Topology{Nodes: []topology.Node{
		node("h", "a", types.RoleHub, "s1", "s2"),
		node("s1", "a", types.RoleSpoke, "h", "s2"),
		node("s2", "a", types.RoleSpoke, "h", "s1"),
	}}
	if err := topo.ImportPeers(); err != nil {
		t.Fatal(err)
	}
	topo.Normalize()
	want := []topology.Link{
		{ID: "h~s1", Kind: types.LinkHubSpoke, A: "h", B: "s1", Cost: 20, Health: topology.HealthAssumed},
		{ID: "h~s2", Kind: types.LinkHubSpoke, A: "h", B: "s2", Cost: 20, Health: topology.HealthAssumed},
		{ID: "s1~s2", Kind: types.LinkDirect, A: "s1", B: "s2", Cost: 10, Health: topology.HealthRequired},
	}
	if !reflect.DeepEqual(topo.Links, want) {
		t.Errorf("%+v", topo.Links)
	}
}

// Every problem at once, nothing changed when there is one, and each kind of
// problem is caught.
func TestImportPeersProblems(t *testing.T) {
	topo := &topology.Topology{Nodes: []topology.Node{
		node("h1", "a", types.RoleHub, "h2", "s1", "s1"),   // hub to hub, and s1 twice
		node("h2", "a", types.RoleHub, "h1"),               // hub to hub (reported once)
		node("s1", "a", types.RoleSpoke, "h1", "s1", "zz"), // itself, and a node that does not exist
		node("s2", "a", types.RoleUnknown, "h1"),           // no role; h1 does not list it
	}}
	err := topo.ImportPeers()
	list := problems(t, err)
	want := map[string]string{
		"h1~h2": "both hubs",
		"s1~s1": "lists itself",
		"s1~zz": "no such node",
	}
	got := map[string]string{}
	for _, problem := range list {
		got[problem.Subject] += problem.Msg + "|"
	}
	for subject, fragment := range want {
		if !strings.Contains(got[subject], fragment) {
			t.Errorf("%s: want %q in %q", subject, fragment, got[subject])
		}
	}
	if !strings.Contains(got["h1~s1"], "more than once") || !strings.Contains(got["h1~s2"], "one-sided") {
		t.Errorf("%v", got)
	}
	if len(topo.Links) != 0 || len(topo.Nodes[0].Peers) == 0 {
		t.Error("a failed import must change nothing")
	}
	// Check also refuses a Topology whose peers were never imported.
	if report := topo.Check(); !report.Has(topology.RuleLinkEnds) {
		t.Errorf("%v", report.Findings)
	}
}

// TestNoRoleIsReportedWhenPairAgrees checks that a pair that agrees but has a node with no role is
// refused, since the kind cannot be derived.
func TestNoRoleIsReportedWhenPairAgrees(t *testing.T) {
	topo := &topology.Topology{Nodes: []topology.Node{
		node("h", "a", types.RoleHub, "x"), node("x", "a", types.RoleUnknown, "h"),
	}}
	list := problems(t, topo.ImportPeers())
	if len(list) != 1 || !strings.Contains(list[0].Msg, "x has no role") {
		t.Errorf("%v", list)
	}
}

// TestPeersAndLinksTogetherAreRefused checks that using both forms in one topology is an error.
func TestPeersAndLinksTogetherAreRefused(t *testing.T) {
	topo := &topology.Topology{
		Nodes: []topology.Node{node("h", "a", types.RoleHub, "s"), node("s", "a", types.RoleSpoke, "h")},
		Links: []topology.Link{{Kind: types.LinkHubSpoke, A: "h", B: "s"}},
	}
	if err := topo.ImportPeers(); !types.HasCode(err, types.ErrInvalidTopology) || !strings.Contains(err.Error(), "one form") {
		t.Errorf("%v", err)
	}
}

// TestNoPeersIsANoOpAndNilIsSafe checks that importing with no peers changes nothing and a nil
// topology is safe.
func TestNoPeersIsANoOpAndNilIsSafe(t *testing.T) {
	topo := decode(t, "three-site.yaml")
	before := *topo
	if err := topo.ImportPeers(); err != nil || !reflect.DeepEqual(before, *topo) {
		t.Error("changed a topology with no peers")
	}
	var none *topology.Topology
	if none.ImportPeers() != nil {
		t.Error("nil topology")
	}
}

// Input order must not change the result.
func TestImportPeersIgnoresInputOrder(t *testing.T) {
	build := func(order []int) *topology.Topology {
		all := []topology.Node{
			node("h", "a", types.RoleHub, "s2", "s1"),
			node("s1", "a", types.RoleSpoke, "s2", "h"),
			node("s2", "a", types.RoleSpoke, "h", "s1"),
		}
		topo := &topology.Topology{}
		for _, i := range order {
			topo.Nodes = append(topo.Nodes, all[i])
		}
		if err := topo.ImportPeers(); err != nil {
			t.Fatal(err)
		}
		topo.Normalize()
		return topo
	}
	first := build([]int{0, 1, 2})
	for _, order := range [][]int{{2, 1, 0}, {1, 0, 2}, {2, 0, 1}} {
		if got := build(order); !reflect.DeepEqual(got.Links, first.Links) {
			t.Errorf("order %v: %+v", order, got.Links)
		}
	}
}
