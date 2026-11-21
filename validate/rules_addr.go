package validate

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// The addressing rules: what the address plan holds, checked on the final plan and
// not only when each allocation was made.
func init() {
	register("V-ADDR-001", Error, "no two allocations overlap", noOverlap)
	register("V-ADDR-002", Error, "the accounting invariant holds", accountingHolds)
	register("V-ADDR-003", Error, "every node has exactly one overlay address", oneOverlayAddress)
	register("V-ADDR-004", Error, "every probed link has a transit /30 whose hosts are its two ends", transitHosts)
	register("V-ADDR-005", Error, "no allocation in a reserved pool without an override reason", reservedPoolOverride)
	register("V-ADDR-006", Warning, "a pool is over 80% used", poolNearlyFull)
	register("V-ADDR-007", Error, "no address in 0/8, 127/8, 169.254/16 or 224/4 is allocated", forbiddenRanges)
	register("V-ADDR-008", Warning, "the supernet overlaps a well-known cloud or home default", supernetCollides)
}

// hasIPAM reports whether the plan has an address plan at all: a plan with a
// zero-valued snapshot was given none, and has nothing to check.
func hasIPAM(p *Plan) bool {
	s := p.IPAM
	return s.Schema != "" || s.Supernet.IsValid() || len(s.Pools) > 0 || len(s.Allocations) > 0
}

// noOverlap reports every allocation that lies inside, or partly inside, another. It
// sweeps the allocations in order of address, so a plan of thousands is checked in one
// pass: prefixes nest or are apart, so an allocation that overlaps the widest block
// seen so far is inside it.
func noOverlap(p *Plan) []Finding {
	all := slices.Clone(p.IPAM.Allocations)
	slices.SortStableFunc(all, func(a, b ipam.Allocation) int { return cidr.Compare(a.Prefix, b.Prefix) })
	var out []Finding
	var wide *ipam.Allocation
	for i := range all {
		next := all[i]
		if wide != nil && cidr.Overlaps(wide.Prefix, next.Prefix) {
			out = append(out, Finding{Subject: next.Prefix.String(),
				Message: fmt.Sprintf("%s (%s) overlaps %s (%s)", next.Prefix, next.Owner, wide.Prefix, wide.Owner),
				Hint:    "release one of the two, or give one a different block"})
			if next.Prefix.Bits() < wide.Prefix.Bits() {
				wide = &all[i]
			}
			continue
		}
		wide = &all[i]
	}
	return out
}

// accountingHolds rebuilds the address plan from its snapshot and checks it loads and
// that its books balance: in the supernet and in every pool, what is allocated,
// reserved, quarantined and free add up to the total, and nothing the plan itself
// found wrong is left. A snapshot that cannot be loaded (an allocation outside the
// supernet or its pool, a bad state) is reported with the reason. An overlap is
// V-ADDR-001's to report, not this rule's.
func accountingHolds(p *Plan) []Finding {
	if !hasIPAM(p) {
		return nil
	}
	registry, err := ipam.New(p.IPAM, &types.FixedClock{T: time.Time{}}, 0)
	if err != nil {
		var list types.List
		if !errors.As(err, &list) {
			list = types.List{types.Errf(types.ErrInvalidValue, "address plan", "%v", err)}
		}
		// An allocation that overlaps another is V-ADDR-001's to report; everything else
		// the plan refuses to load, an overlap of two pools included, is reported here.
		var rest []string
		for _, e := range list {
			if e.Code == types.ErrOverlap && !strings.HasPrefix(e.Msg, "pool ") {
				continue
			}
			rest = append(rest, e.Error())
		}
		if len(rest) == 0 {
			return nil
		}
		return []Finding{{Subject: p.IPAM.Supernet.String(), Message: "the address plan does not load: " + strings.Join(rest, "; "),
			Hint: "the snapshot was edited by hand or is damaged; restore it from the last good one"}}
	}
	var out []Finding
	report := registry.Accounting()
	for _, problem := range report.Problems {
		out = append(out, Finding{Subject: report.Supernet.String(), Message: problem, Hint: "the snapshot is damaged; restore it from the last good one"})
	}
	if sum := report.Allocated + report.Reserved + report.Quarantined + report.Free; sum != report.Total {
		out = append(out, Finding{Subject: report.Supernet.String(), Message: fmt.Sprintf("allocated, reserved, quarantined and free add up to %d, not the total %d", sum, report.Total)})
	}
	for _, pool := range report.Pools {
		if sum := pool.Allocated + pool.Reserved + pool.Quarantined + pool.Free; sum != pool.Total {
			out = append(out, Finding{Subject: string(pool.Name), Message: fmt.Sprintf("pool %s: allocated, reserved, quarantined and free add up to %d, not the total %d", pool.Name, sum, pool.Total)})
		}
	}
	return out
}

// oneOverlayAddress checks each node of the topology holds exactly one allocated /32 of
// its own.
func oneOverlayAddress(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	count := map[string]int{}
	for _, a := range p.IPAM.Allocations {
		if a.Owner.Kind == ipam.KindNode && a.State == ipam.Allocated && a.Prefix.Bits() == 32 {
			count[a.Owner.ID]++
		}
	}
	var out []Finding
	for _, n := range p.Topology.Nodes {
		switch got := count[string(n.ID)]; {
		case got == 0:
			out = append(out, Finding{Subject: string(n.ID), Message: "it has no overlay address", Hint: "allocate one from the overlay pool"})
		case got > 1:
			out = append(out, Finding{Subject: string(n.ID), Message: fmt.Sprintf("it has %d overlay addresses, want one", got), Hint: "release the extra ones"})
		}
	}
	return out
}

// probed reports whether a link needs a transit /30: a direct link whose health must
// be proven. A link with no health mode takes its kind's default.
func probed(l topology.Link) bool {
	required := l.Health == topology.HealthRequired || (l.Health == topology.HealthUnset && l.Kind == types.LinkDirect)
	return l.Kind == types.LinkDirect && required
}

// transitHosts checks every probed link has one allocated /30 of its own, and that the
// probe addresses the ends' configs use are the two hosts of that /30: different, inside
// it, the lower to the lexically smaller node, and each end's remote is the other's local.
func transitHosts(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	blocks := map[string][]netip.Prefix{}
	for _, a := range p.IPAM.Allocations {
		if a.Owner.Kind == ipam.KindLink && a.State == ipam.Allocated {
			blocks[a.Owner.ID] = append(blocks[a.Owner.ID], a.Prefix)
		}
	}
	var out []Finding
	for _, l := range p.Topology.Links {
		if !probed(l) {
			continue
		}
		id := types.LinkIDOf(l.A, l.B)
		have := blocks[string(id)]
		if len(have) != 1 || have[0].Bits() != 30 {
			out = append(out, Finding{Subject: string(id), Message: fmt.Sprintf("it needs one transit /30 and has %d block(s)", len(have)), Hint: "allocate a /30 from the transit pool for the link"})
			continue
		}
		block := have[0]
		low := block.Addr().Next()
		want := map[types.NodeID]netip.Addr{min(l.A, l.B): low, max(l.A, l.B): low.Next()}
		for _, end := range []types.NodeID{l.A, l.B} {
			other := l.A
			if end == l.A {
				other = l.B
			}
			peer, ok := probeOf(p, end, other)
			if !ok {
				continue // a missing probe is V-LINK-003's to report
			}
			if peer.Local.Addr() != want[end] || peer.Remote != want[other] || !block.Contains(peer.Local.Addr()) || !block.Contains(peer.Remote) {
				out = append(out, Finding{Subject: string(id),
					Message: fmt.Sprintf("%s probes from %s to %s, but the hosts of %s are %s for %s and %s for %s", end, peer.Local.Addr(), peer.Remote, block, want[end], end, want[other], other),
					Hint:    "the lower host goes to the lexically smaller node; regenerate the configs"})
			}
		}
	}
	return out
}

// reservedPoolOverride checks nothing is allocated inside a reserved pool unless it was
// given an override reason. An override is recorded in the allocation's note as
// "override: <reason>".
func reservedPoolOverride(p *Plan) []Finding {
	reserved := map[ipam.PoolName]bool{}
	for _, pool := range p.IPAM.Pools {
		if pool.Reserved {
			reserved[pool.Name] = true
		}
	}
	var out []Finding
	for _, a := range p.IPAM.Allocations {
		if a.State != ipam.Allocated || !reserved[a.Pool] {
			continue
		}
		if reason, ok := strings.CutPrefix(a.Note, "override:"); !ok || strings.TrimSpace(strings.SplitN(reason, ";", 2)[0]) == "" {
			out = append(out, Finding{Subject: a.Prefix.String(), Message: fmt.Sprintf("%s (%s) is in the reserved pool %s with no override reason", a.Prefix, a.Owner, a.Pool),
				Hint: "allocate it with an override reason, or from another pool"})
		}
	}
	return out
}

// poolNearlyFull warns about a pool that is more than 80% used (allocated, reserved or
// quarantined), before it runs out.
func poolNearlyFull(p *Plan) []Finding {
	if !hasIPAM(p) {
		return nil
	}
	registry, err := ipam.New(p.IPAM, &types.FixedClock{T: time.Time{}}, 0)
	if err != nil {
		return nil // V-ADDR-002 reports a plan that does not load
	}
	var out []Finding
	for _, pool := range registry.Accounting().Pools {
		used := pool.Allocated + pool.Reserved + pool.Quarantined
		if pool.Total > 0 && used*100 > pool.Total*80 {
			out = append(out, Finding{Subject: string(pool.Name),
				Message: fmt.Sprintf("pool %s (%s) is %d%% used: %d of %d", pool.Name, pool.Prefix, used*100/pool.Total, used, pool.Total),
				Hint:    "plan for a larger pool, or a second one, before it runs out"})
		}
	}
	return out
}

// forbidden are the ranges nothing may be allocated in: "this network", loopback,
// link-local and multicast.
var forbidden = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("224.0.0.0/4"),
}

// forbiddenRanges reports every allocation that lies in, or contains, a forbidden range.
func forbiddenRanges(p *Plan) []Finding {
	var out []Finding
	for _, a := range p.IPAM.Allocations {
		for _, bad := range forbidden {
			if cidr.Overlaps(a.Prefix, bad) {
				out = append(out, Finding{Subject: a.Prefix.String(), Message: fmt.Sprintf("%s (%s) is in %s, which must never be allocated", a.Prefix, a.Owner, bad),
					Hint: "use private space (RFC 1918) outside the well-known defaults"})
			}
		}
	}
	return out
}

// commonDefaults are blocks that cloud providers and home routers hand out by default,
// so a supernet that overlaps one is likely to collide with a site's own network.
var commonDefaults = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/16"), netip.MustParsePrefix("10.1.0.0/24"), netip.MustParsePrefix("10.10.0.0/24"),
	netip.MustParsePrefix("172.17.0.0/16"), netip.MustParsePrefix("172.31.0.0/16"),
	netip.MustParsePrefix("192.168.0.0/24"), netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("192.168.100.0/24"),
}

// supernetCollides warns when the supernet overlaps a well-known default.
func supernetCollides(p *Plan) []Finding {
	var out []Finding
	if !p.IPAM.Supernet.IsValid() {
		return nil
	}
	for _, def := range commonDefaults {
		if cidr.Overlaps(p.IPAM.Supernet, def) {
			out = append(out, Finding{Subject: p.IPAM.Supernet.String(), Message: fmt.Sprintf("the supernet %s overlaps %s, which cloud providers and home routers commonly use", p.IPAM.Supernet, def),
				Hint: "pick a supernet away from the defaults, or expect a collision at a site that uses one"})
		}
	}
	return out
}
