package e2e

import (
	"net/netip"

	"github.com/vectorihub/netctl/ipam"
)

// DiscussionOverlap returns the address plan of the original discussion: AWS holds the site
// block 10.42.0.0/20 and the three machines were also given 10.42.0.2, 10.42.0.3 and
// 10.42.0.4, which all lie inside it. The topology format cannot pin an address, so the
// conflict is built as saved state, which is how a hand-edited file would carry it. Loading
// it must be refused with one OVERLAP error per machine.
func (w *W) DiscussionOverlap() ipam.Snapshot {
	w.T.Helper()
	snap := w.EmptyPlan()
	at := w.Now
	snap.Allocations = []ipam.Allocation{
		{Prefix: netip.MustParsePrefix("10.42.0.0/20"), Pool: "sites", Owner: ipam.Site("aws"), State: ipam.Allocated, CreatedAt: at},
		{Prefix: netip.MustParsePrefix("10.42.0.2/32"), Pool: "sites", Owner: ipam.Node("aws-hub"), State: ipam.Allocated, CreatedAt: at},
		{Prefix: netip.MustParsePrefix("10.42.0.3/32"), Pool: "sites", Owner: ipam.Node("builder-1"), State: ipam.Allocated, CreatedAt: at},
		{Prefix: netip.MustParsePrefix("10.42.0.4/32"), Pool: "sites", Owner: ipam.Node("dgx-spark"), State: ipam.Allocated, CreatedAt: at},
	}
	return snap
}

// EmptyPlan returns the standard address plan with no allocations: the pools of the
// address-plan fixture and nothing handed out, the "empty.json" of the guide.
func (w *W) EmptyPlan() ipam.Snapshot {
	w.T.Helper()
	topo := w.Load(DefaultTopology)
	snap := ipam.Snapshot{Schema: ipam.Schema, Supernet: topo.Addressing.Supernet}
	for _, p := range topo.Addressing.Pools {
		snap.Pools = append(snap.Pools, ipam.Pool{Name: ipam.PoolName(p.Name), Prefix: p.Prefix, ChildBits: p.ChildBits, Reserved: p.Reserved, SkipFirst: p.SkipFirst})
	}
	return snap
}

// CorruptOverlap returns a copy of a saved address plan with a second allocation placed on
// top of the block of site builder: 10.42.16.0/21 for the owner site:extra, the way a hand
// edit would break it.
func (w *W) CorruptOverlap(snap ipam.Snapshot) ipam.Snapshot {
	out := snap
	out.Allocations = append([]ipam.Allocation(nil), snap.Allocations...)
	out.Allocations = append(out.Allocations, ipam.Allocation{
		Prefix: netip.MustParsePrefix("10.42.16.0/21"), Pool: "sites", Owner: ipam.Site("extra"), State: ipam.Allocated, CreatedAt: w.Now,
	})
	out.SortAllocations()
	return out
}
