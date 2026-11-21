package tests

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// TestReportOrderIsTotalForSharedLabels gives NewReport two different results
// with one label, in both orders, and checks the report lists them the same way.
func TestReportOrderIsTotalForSharedLabels(t *testing.T) {
	a := sim.FlowResult{Flow: sim.Flow{Label: "same", Family: sim.FamilyOverlay, From: "a"}, Verdict: sim.VerdictOK}
	b := sim.FlowResult{Flow: sim.Flow{Label: "same", Family: sim.FamilyOverlay, From: "b"}, Verdict: sim.VerdictBlackHole}
	first := sim.NewReport([]sim.FlowResult{a, b})
	second := sim.NewReport([]sim.FlowResult{b, a})
	if !reflect.DeepEqual(first.Flows, second.Flows) {
		t.Errorf("the order depends on the input: %v then %v", first.Flows, second.Flows)
	}
}

// TestHostOverrideInsideAHeldBlockIsFollowed checks a static override for one
// address of builder-1's own block, 10.42.16.7/32, over the direct link that is
// down, is followed and caught as LINK_DOWN: the simulator does not deliver the
// packet at builder-1 just because the block is builder-1's. An address of the
// block with no override is still delivered.
func TestHostOverrideInsideAHeldBlockIsFollowed(t *testing.T) {
	g := fixture.ThreeSiteDirectDown()
	input := fixture.ThreeSiteInput()
	input.Overrides = []route.Override{{Node: "builder-1", Prefix: prefixOf("10.42.16.7/32"), NextHop: "dgx-spark", Link: "builder-1~dgx-spark"}}
	result, err := route.Compute(g, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("the override was rejected: %+v", result.Rejected)
	}
	net := sim.NewNet(g, result.Tables, input.Owned, input.Local)
	if trace := net.Send(sim.Packet{Dst: addrOf("10.42.16.7")}, "builder-1"); trace.Final != sim.LinkDown {
		t.Errorf("the overridden address ended %s at %v, want LINK_DOWN at builder-1", trace.Final, nodesText(trace))
	}
	if trace := net.Send(sim.Packet{Dst: addrOf("10.42.16.8")}, "builder-1"); trace.Final != sim.Delivered || len(trace.Hops) != 1 {
		t.Errorf("an address of the block with no override ended %s, want delivered at once", trace.Final)
	}
}

// planWith returns an address plan with the given supernet, one site pool whose
// blocks are childBits long, and an overlay pool of /32s, with each site and node
// of the three-site network allocated.
func planWith(t *testing.T, supernet string, childBits int) *ipam.IPAM {
	t.Helper()
	registry, err := ipam.New(ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: netip.MustParsePrefix(supernet),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/24"), ChildBits: childBits},
			{Name: "overlay", Prefix: netip.MustParsePrefix("10.42.240.0/24"), ChildBits: 32, SkipFirst: 1},
		},
	}, fixture.PlanClock, ipam.DefaultQuarantine)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"aws", "builder", "dgx"} {
		if _, err := registry.Allocate("sites", ipam.Site(site)); err != nil {
			t.Fatal(err)
		}
	}
	for _, node := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		if _, err := registry.Allocate("overlay", ipam.Node(node)); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}

// TestSiteFlowAddressStaysInsideA32Block checks a site whose block is a single
// /32 is tested at that address, not at the next one, which belongs to someone
// else.
func TestSiteFlowAddressStaysInsideA32Block(t *testing.T) {
	registry := planWith(t, "10.42.0.0/16", 32)
	flows, err := sim.Flows(fixture.ThreeSiteTopology(), registry, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[types.NodeID]netip.Prefix{}
	for node, site := range map[types.NodeID]string{"aws-hub": "aws", "builder-1": "builder", "dgx-spark": "dgx"} {
		blocks[node] = registry.ByOwner(ipam.Site(site))[0].Prefix
	}
	sites := 0
	for _, f := range flows {
		if f.Family != sim.FamilySite {
			continue
		}
		sites++
		if !blocks[f.From].Contains(f.Src) || !blocks[f.To].Contains(f.Dst) {
			t.Errorf("%s tests %s → %s, outside the blocks %s and %s", f.Label, f.Src, f.Dst, blocks[f.From], blocks[f.To])
		}
	}
	if sites != 6 {
		t.Errorf("%d site flows, want 6", sites)
	}
}

// TestNoOutsideAddressIsRefused checks a supernet of 0.0.0.0/0, which leaves no
// address outside it, is refused with INVALID_VALUE instead of producing negative
// flows to an invalid address that would pass as UNREACHABLE_EXPECTED.
func TestNoOutsideAddressIsRefused(t *testing.T) {
	flows, err := sim.Flows(fixture.ThreeSiteTopology(), planWith(t, "0.0.0.0/0", 28), sim.Options{})
	if !types.HasCode(err, types.ErrInvalidValue) || flows != nil {
		t.Errorf("Flows with supernet 0.0.0.0/0 = %d flows, %v; want INVALID_VALUE and none", len(flows), err)
	}
}
