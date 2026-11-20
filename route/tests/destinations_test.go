package tests

import (
	"net/netip"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// planClock is the fixed clock the address-plan tests use.
var planClock = &types.FixedClock{T: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}

// newPlan builds the reference address plan, the pools of
// testdata/topologies/address-plan.yaml, with nothing allocated yet.
func newPlan(t *testing.T) *ipam.IPAM {
	t.Helper()
	registry, err := ipam.New(ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: prefixOf("10.42.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: prefixOf("10.42.0.0/17"), ChildBits: 20},
			{Name: "growth", Prefix: prefixOf("10.42.128.0/18"), ChildBits: 20, Reserved: true},
			{Name: "overlay", Prefix: prefixOf("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
			{Name: "infra", Prefix: prefixOf("10.42.248.0/22"), Reserved: true},
			{Name: "transit", Prefix: prefixOf("10.42.252.0/22"), ChildBits: 30},
		},
	}, planClock, ipam.DefaultQuarantine)
	if err != nil {
		t.Fatalf("building the address plan: %v", err)
	}
	return registry
}

// threeSitePlan allocates the three-site network in the reference plan: a /20
// for each site, an overlay /32 for each node, and a /30 for the direct link.
func threeSitePlan(t *testing.T) *ipam.IPAM {
	t.Helper()
	registry := newPlan(t)
	for _, site := range []string{"aws", "builder", "dgx"} {
		if _, err := registry.Allocate("sites", ipam.Site(site)); err != nil {
			t.Fatalf("allocating site %s: %v", site, err)
		}
	}
	for _, node := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		if _, err := registry.Allocate("overlay", ipam.Node(node)); err != nil {
			t.Fatalf("allocating node %s: %v", node, err)
		}
	}
	if _, err := registry.AllocateTransit("builder-1~dgx-spark"); err != nil {
		t.Fatalf("allocating the transit block: %v", err)
	}
	return registry
}

// threeSiteAnnouncers is what each node of the reference network announces.
// The hub announces its site too, as the engineering plan's tables assume.
func threeSiteAnnouncers() []route.Announcer {
	return []route.Announcer{
		{Node: "aws-hub", Site: "aws", AdvertiseSite: true},
		{Node: "builder-1", Site: "builder", AdvertiseSite: true},
		{Node: "dgx-spark", Site: "dgx", AdvertiseSite: true},
	}
}

// threeSiteLinks is the three links of the reference network.
func threeSiteLinks() []types.LinkID {
	return []types.LinkID{"aws-hub~builder-1", "aws-hub~dgx-spark", "builder-1~dgx-spark"}
}

// TestCollectReadsTheAddressPlan checks that Collect turns the address plan
// into exactly the input the fixture spells out by hand.
func TestCollectReadsTheAddressPlan(t *testing.T) {
	owned, local, err := route.Collect(threeSitePlan(t), threeSiteAnnouncers(), threeSiteLinks())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !reflect.DeepEqual(owned, fixture.ThreeSiteOwned()) {
		t.Errorf("owned\n got: %v\nwant: %v", owned, fixture.ThreeSiteOwned())
	}
	if !reflect.DeepEqual(local, fixture.ThreeSiteLocal()) {
		t.Errorf("local\n got: %v\nwant: %v", local, fixture.ThreeSiteLocal())
	}
}

// TestCollectedInputComputesTheReferenceTables checks the whole path from the
// address plan to the tables: the result equals the one computed from the
// hand-written input.
func TestCollectedInputComputesTheReferenceTables(t *testing.T) {
	owned, local, err := route.Collect(threeSitePlan(t), threeSiteAnnouncers(), threeSiteLinks())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := mustCompute(t, fixture.ThreeSite(true), route.Input{Owned: owned, Local: local})
	want := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	if !reflect.DeepEqual(got, want) {
		t.Error("tables from the address plan differ from the reference tables")
	}
}

// TestCollectSiteBlockOnlyWhenAdvertised checks that a node announces its site
// block only if it says so; its own overlay address is announced regardless.
func TestCollectSiteBlockOnlyWhenAdvertised(t *testing.T) {
	announcers := threeSiteAnnouncers()
	announcers[0].AdvertiseSite = false
	owned, _, err := route.Collect(threeSitePlan(t), announcers, nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := []netip.Prefix{prefixOf("10.42.240.1/32")}; !reflect.DeepEqual(owned["aws-hub"], want) {
		t.Errorf("aws-hub announces %v, want only its overlay address", owned["aws-hub"])
	}
}

// TestCollectExtraPrefixes checks the extra prefixes a node lists: they are
// announced in canonical form, and listing one twice announces it once.
func TestCollectExtraPrefixes(t *testing.T) {
	announcers := []route.Announcer{{Node: "builder-1", Prefixes: []netip.Prefix{prefixOf("10.9.0.5/24"), prefixOf("10.9.0.0/24"), prefixOf("10.8.0.0/24")}}}
	owned, _, err := route.Collect(newPlan(t), announcers, nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := []netip.Prefix{prefixOf("10.8.0.0/24"), prefixOf("10.9.0.0/24")}; !reflect.DeepEqual(owned["builder-1"], want) {
		t.Errorf("builder-1 announces %v, want %v", owned["builder-1"], want)
	}
}

// TestCollectIgnoresBlocksNotInUse checks that only allocated blocks are
// destinations: a node whose overlay address was released, so it is in
// quarantine, announces nothing, and a reserved block is not announced.
func TestCollectIgnoresBlocksNotInUse(t *testing.T) {
	registry := threeSitePlan(t)
	if err := registry.Release(prefixOf("10.42.240.2/32"), ipam.Node("builder-1")); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := registry.Reserve(prefixOf("10.42.240.9/32"), ipam.Node("dgx-spark"), "held for later"); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	owned, _, err := route.Collect(registry, []route.Announcer{{Node: "builder-1"}, {Node: "dgx-spark"}}, nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(owned["builder-1"]) != 0 {
		t.Errorf("builder-1 announces %v after releasing its address", owned["builder-1"])
	}
	if want := []netip.Prefix{prefixOf("10.42.240.3/32")}; !reflect.DeepEqual(owned["dgx-spark"], want) {
		t.Errorf("dgx-spark announces %v, want only its allocated address", owned["dgx-spark"])
	}
}

// TestCollectLocalIsOnBothEnds checks that a link's transit block is local to
// both of its ends, and that a link with no transit block is skipped.
func TestCollectLocalIsOnBothEnds(t *testing.T) {
	_, local, err := route.Collect(threeSitePlan(t), nil, threeSiteLinks())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := route.Owned{"builder-1": {prefixOf("10.42.252.0/30")}, "dgx-spark": {prefixOf("10.42.252.0/30")}}
	if !reflect.DeepEqual(local, want) {
		t.Errorf("local = %v, want %v", local, want)
	}
}

// TestCollectRefusesBrokenInput checks each way Collect refuses: a nil
// registry, a node that advertises a site with no block, a bad prefix and a
// bad link ID. Every problem comes back at once, with no lists.
func TestCollectRefusesBrokenInput(t *testing.T) {
	if _, _, err := route.Collect(nil, nil, nil); err == nil {
		t.Error("Collect accepted a nil registry")
	}
	owned, local, err := route.Collect(threeSitePlan(t),
		[]route.Announcer{
			{Node: "x", Site: "nowhere", AdvertiseSite: true},
			{Node: "y", Prefixes: []netip.Prefix{prefixOf("fd00::/64")}},
		},
		[]types.LinkID{"no-tilde", "a~a", "A~b"})
	if !types.HasCode(err, types.ErrNotFound) || !types.HasCode(err, types.ErrInvalidPrefix) || !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("error = %v, want NOT_FOUND, INVALID_PREFIX and INVALID_TOPOLOGY together", err)
	}
	if owned != nil || local != nil {
		t.Error("Collect returned lists alongside an error")
	}
}

// TestTwoNodesAdvertisingOneSiteIsRefusedByCompute checks the hand-off between
// Collect and Compute: two nodes at one site that both advertise it give two
// owners for one block, which Compute refuses.
func TestTwoNodesAdvertisingOneSiteIsRefusedByCompute(t *testing.T) {
	g := fixture.NewGraph().Hub("hub").Spoke("a", "b")
	g.Link("hub", "a", 20)
	g.Link("hub", "b", 20)
	owned, local, err := route.Collect(threeSitePlan(t), []route.Announcer{
		{Node: "a", Site: "builder", AdvertiseSite: true},
		{Node: "b", Site: "builder", AdvertiseSite: true},
	}, nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := route.Compute(g, route.Input{Owned: owned, Local: local}); !types.HasCode(err, types.ErrOverlap) {
		t.Errorf("Compute error = %v, want OVERLAP", err)
	}
}

// loadThreeSite reads the reference topology file and returns it. The hub's
// "advertises" line is left out of the file, but the engineering plan's tables
// treat the hub's block as advertised, so the announcers set it.
func loadThreeSite(t *testing.T) *topology.Topology {
	t.Helper()
	data, err := os.ReadFile("../../testdata/topologies/three-site.yaml")
	if err != nil {
		t.Fatalf("reading the reference topology: %v", err)
	}
	loaded, _, err := topology.Load("three-site.yaml", data, 1<<20)
	if err != nil {
		t.Fatalf("loading the reference topology: %v", err)
	}
	return loaded
}

// TestRoutesComputedFromARealTopology checks that route works with the
// topology package without importing it: the reference topology file is loaded,
// its graph is adapted to route.Graph, and the tables equal the hand-built
// ones, with the direct link healthy and with it down.
func TestRoutesComputedFromARealTopology(t *testing.T) {
	loaded := loadThreeSite(t)
	direct := types.LinkIDOf("builder-1", "dgx-spark")
	for name, c := range map[string]struct {
		usable func(types.LinkID) bool
		by     *fixture.Graph
	}{
		"healthy":     {nil, fixture.ThreeSite(true)},
		"direct down": {func(link types.LinkID) bool { return link != direct }, fixture.ThreeSiteDirectDown()},
	} {
		adapted := fixture.FromTopology(topology.BuildGraph(loaded, c.usable))
		got := mustCompute(t, adapted, fixture.ThreeSiteInput())
		want := mustCompute(t, c.by, fixture.ThreeSiteInput())
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: tables from the topology file differ from the hand-built ones", name)
		}
	}
	graph := topology.BuildGraph(loaded, nil)
	if got := fixture.FromTopology(graph).Edges("builder-1"); len(got) != 2 {
		t.Errorf("builder-1 has %d edges through the adapter, want 2", len(got))
	}
}
