package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// TestASiteWithAnOverrideIsNumberedFromThatPool checks that a site that asks for the reserved
// growth pool, with a reason, gets its block there, that the reason is kept with the block,
// and that planning again leaves it where it is.
func TestASiteWithAnOverrideIsNumberedFromThatPool(t *testing.T) {
	in := fresh(t)
	in.Topology.Sites = append(in.Topology.Sites, topology.Site{ID: "extra", Override: &topology.SiteOverride{Pool: "growth", Reason: "approved expansion"}})
	in.Topology.Nodes = append(in.Topology.Nodes, topology.Node{ID: "extra-1", Site: "extra", Role: types.RoleSpoke})
	in.Topology.Links = append(in.Topology.Links, topology.Link{Kind: types.LinkHubSpoke, A: hub, B: "extra-1"})
	in.Topology.Normalize()
	first := mustPlan(t, in)

	var got ipam.Allocation
	for _, a := range first.IPAM.Allocations {
		if a.Owner == ipam.Site("extra") {
			got = a
		}
	}
	if got.Pool != "growth" || got.Prefix.String() != "10.42.128.0/20" || !strings.Contains(got.Note, "approved expansion") {
		t.Fatalf("the site's block is %+v, want 10.42.128.0/20 from growth with the reason", got)
	}

	again := fresh(t)
	again.Topology, again.IPAM = in.Topology, first.IPAM
	second := mustPlan(t, again)
	if len(second.Changes) != 0 {
		t.Errorf("planning again changed the address plan: %v", second.Changes)
	}
}

// TestWithoutTheOverrideTheGrowthPoolStaysClosed checks the other half: the same site without
// an override is numbered from the sites pool, and the growth pool is left alone.
func TestWithoutTheOverrideTheGrowthPoolStaysClosed(t *testing.T) {
	in := fresh(t)
	in.Topology.Sites = append(in.Topology.Sites, topology.Site{ID: "extra"})
	in.Topology.Nodes = append(in.Topology.Nodes, topology.Node{ID: "extra-1", Site: "extra", Role: types.RoleSpoke})
	in.Topology.Links = append(in.Topology.Links, topology.Link{Kind: types.LinkHubSpoke, A: hub, B: "extra-1"})
	in.Topology.Normalize()
	r := mustPlan(t, in)
	for _, a := range r.IPAM.Allocations {
		if a.Pool == "growth" {
			t.Errorf("%v came from the growth pool without an override", a)
		}
	}
}
