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

// TestChangingASitesPoolMovesItsBlock checks that giving an existing site an override, and
// later taking it away, moves the site's block between pools instead of leaving it holding
// two allocated blocks.
func TestChangingASitesPoolMovesItsBlock(t *testing.T) {
	in := fresh(t)
	in.Topology.Sites = append(in.Topology.Sites, topology.Site{ID: "extra"})
	in.Topology.Nodes = append(in.Topology.Nodes, topology.Node{ID: "extra-1", Site: "extra", Role: types.RoleSpoke})
	in.Topology.Links = append(in.Topology.Links, topology.Link{Kind: types.LinkHubSpoke, A: hub, B: "extra-1"})
	in.Topology.Normalize()
	first := mustPlan(t, in)

	// held counts the allocated blocks the site holds, and reports the pool of the last.
	held := func(r ipam.Snapshot) (n int, pool ipam.PoolName) {
		for _, a := range r.Allocations {
			if a.Owner == ipam.Site("extra") && a.State == ipam.Allocated {
				n, pool = n+1, a.Pool
			}
		}
		return n, pool
	}
	if n, pool := held(first.IPAM); n != 1 || pool == "growth" {
		t.Fatalf("before the override the site holds %d blocks (last from %s), want 1 from the sites pool", n, pool)
	}

	moved := fresh(t)
	withOverride := *in.Topology // a copy, so the baseline keeps its sites
	moved.Topology, moved.IPAM = &withOverride, first.IPAM
	withOverride.Sites = append([]topology.Site(nil), in.Topology.Sites...)
	for i := range withOverride.Sites {
		if withOverride.Sites[i].ID == "extra" {
			withOverride.Sites[i].Override = &topology.SiteOverride{Pool: "growth", Reason: "approved expansion"}
		}
	}
	second := mustPlan(t, moved)
	if n, pool := held(second.IPAM); n != 1 || pool != "growth" {
		t.Fatalf("with the override the site holds %d blocks (last from %s), want 1 from growth", n, pool)
	}

	back := fresh(t)
	back.Topology, back.IPAM = in.Topology, second.IPAM
	third := mustPlan(t, back)
	if n, pool := held(third.IPAM); n != 1 || pool == "growth" {
		t.Fatalf("after the override is removed the site holds %d blocks (last from %s), want 1 from the sites pool", n, pool)
	}
}
