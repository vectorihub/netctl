package plan

import (
	"slices"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// reconcileAddresses brings the address plan in line with the topology (steps 2 to 5):
// it releases what is gone and allocates what is new, in a fixed order, and reports every
// problem at once.
//
//   - It releases the allocations whose owner is no longer in the topology: nodes first,
//     then links, then sites, so that a site and the machines in it can go in one plan.
//   - It allocates every site a block, sorted by site ID.
//   - It allocates every node an overlay address, sorted by node ID.
//   - It allocates a transit /30 to every direct link whose health must be proven, sorted
//     by link ID, the lower host going to the smaller node ID.
//
// Everything is allocated in sorted order and never in the order the topology lists it, so
// reordering the file changes nothing on a fresh plan, and the registry's idempotency keeps
// each owner's block on later plans.
func reconcileAddresses(m *ipam.IPAM, t *topology.Topology) error {
	var errs types.List
	present := map[string]map[string]bool{ipam.KindNode: {}, ipam.KindLink: {}, ipam.KindSite: {}}
	for _, n := range t.Nodes {
		present[ipam.KindNode][string(n.ID)] = true
	}
	for _, l := range t.Links {
		present[ipam.KindLink][string(types.LinkIDOf(l.A, l.B))] = true
	}
	for _, s := range t.Sites {
		present[ipam.KindSite][string(s.ID)] = true
	}
	// The pool each site's block must come from today. A site whose block sits in another
	// pool (its override was added, removed or changed) gives that block back, so it does not
	// end up holding two.
	wantPool := map[string]ipam.PoolName{}
	for _, s := range t.Sites {
		wantPool[string(s.ID)] = SitesPool
		if s.Override != nil {
			wantPool[string(s.ID)] = ipam.PoolName(s.Override.Pool)
		}
	}
	for _, kind := range []string{ipam.KindNode, ipam.KindLink, ipam.KindSite} {
		for _, a := range m.Snapshot().Allocations {
			if a.Owner.Kind != kind || a.State != ipam.Allocated {
				continue
			}
			stale := !present[kind][a.Owner.ID] || (kind == ipam.KindSite && a.Pool != wantPool[a.Owner.ID])
			if stale {
				if err := m.Release(a.Prefix, a.Owner); err != nil {
					errs.Add(problem(err))
				}
			}
		}
	}
	for _, s := range sorted(t.Sites, func(s topology.Site) string { return string(s.ID) }) {
		pool, opts := SitesPool, []ipam.Option(nil)
		if s.Override != nil {
			// A site that asks for another pool on purpose, with a reason, takes its block there.
			pool, opts = ipam.PoolName(s.Override.Pool), []ipam.Option{ipam.WithOverride(s.Override.Reason)}
		}
		if _, err := m.Allocate(pool, ipam.Site(string(s.ID)), opts...); err != nil {
			errs.Add(problem(err))
		}
	}
	for _, n := range sorted(t.Nodes, func(n topology.Node) string { return string(n.ID) }) {
		if _, err := m.Allocate(OverlayPool, ipam.Node(string(n.ID))); err != nil {
			errs.Add(problem(err))
		}
	}
	for _, l := range sorted(t.Links, func(l topology.Link) string { return string(types.LinkIDOf(l.A, l.B)) }) {
		if probed(l) {
			if _, err := m.AllocateTransit(types.LinkIDOf(l.A, l.B)); err != nil {
				errs.Add(problem(err))
			}
		}
	}
	return errs.Err()
}

// sorted returns a copy of the items ordered by key. A topology that has been normalised
// is already in this order; this makes the allocation order a property of this function and
// not of the caller.
func sorted[T any](items []T, key func(T) string) []T {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b T) int {
		ka, kb := key(a), key(b)
		switch {
		case ka < kb:
			return -1
		case ka > kb:
			return 1
		}
		return 0
	})
	return out
}
