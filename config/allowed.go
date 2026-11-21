package config

import (
	"cmp"
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// AllowedPrefixes applies rule G3 to one node: it returns, for each next-hop node,
// the prefixes that peer is allowed to carry. A peer's list is its probe address
// (only a direct link has one) and every prefix whose active route on this node
// has that peer as its next hop.
//
// WireGuard keeps a prefix with exactly one peer, and sends traffic for it to
// that peer, so only active routes count: a backup never puts a prefix in a list,
// and each prefix is in one peer's list at a time. When the direct link fails
// the site and overlay prefixes of the far node leave the direct peer's list and
// join the hub's, together, in the next config. The probe address does not move,
// so probes keep crossing the direct link after its traffic has gone to the hub,
// and its recovery can be seen.
//
// The lists are masked and sorted. A prefix that is active through two different
// peers is a conflict WireGuard would settle silently by keeping the last; it is
// returned as a second value for the caller to report, in sorted order, naming the
// prefix and the two peers. A nil table has no routes.
//
// Example:
//
//	with the direct link healthy, builder-1's hub peer gets 10.42.0.0/20 and
//	10.42.240.1/32, and its dgx-spark peer gets 10.42.32.0/20, 10.42.240.3/32 and
//	the probe address 10.42.252.2/32.
func AllowedPrefixes(table *route.Table, probeRemote map[types.NodeID]netip.Addr) (map[types.NodeID][]netip.Prefix, []Conflict) {
	out := map[types.NodeID][]netip.Prefix{}
	owner := map[netip.Prefix]types.NodeID{}
	var conflicts []Conflict
	claim := func(peer types.NodeID, prefix netip.Prefix) {
		masked, _ := cidr.Canonical(prefix)
		if first, held := owner[masked]; held && first != peer {
			conflicts = append(conflicts, Conflict{Prefix: masked, Peers: [2]types.NodeID{min(first, peer), max(first, peer)}})
			return
		}
		owner[masked] = peer
		out[peer] = append(out[peer], masked)
	}
	// Claims are made in a fixed order, probes by peer and then routes by
	// route.Compare, so which peer is "first" in a conflict never depends on
	// map order or on the order the table happens to be in.
	probePeers := make([]types.NodeID, 0, len(probeRemote))
	for peer := range probeRemote {
		probePeers = append(probePeers, peer)
	}
	slices.Sort(probePeers)
	for _, peer := range probePeers {
		addr := probeRemote[peer]
		claim(peer, netip.PrefixFrom(addr, addr.BitLen()))
	}
	if table != nil {
		routes := slices.Clone(table.Routes)
		for i := range routes {
			routes[i].Prefix, _ = cidr.Canonical(routes[i].Prefix)
		}
		slices.SortFunc(routes, route.Compare)
		for _, r := range routes {
			if r.Active && r.NextHop != "" {
				claim(r.NextHop, r.Prefix)
			}
		}
	}
	for peer := range out {
		out[peer] = maskAll(out[peer])
	}
	sortConflicts(conflicts)
	return out, slices.Compact(conflicts)
}

// Conflict is a prefix that two peers of one node would both be allowed to carry.
//
// Example:
//
//	Conflict{Prefix: 10.42.32.0/20, Peers: {"aws-hub", "dgx-spark"}}.
type Conflict struct {
	// Prefix is the prefix in two lists.
	//
	// Example:
	//
	//	10.42.32.0/20.
	Prefix netip.Prefix
	// Peers are the two peers, in order.
	//
	// Example:
	//
	//	{"aws-hub", "dgx-spark"}.
	Peers [2]types.NodeID
}

// sortConflicts puts conflicts in a fixed order, by prefix and then by peers.
func sortConflicts(conflicts []Conflict) {
	slices.SortFunc(conflicts, func(a, b Conflict) int {
		return cmp.Or(cidr.Compare(a.Prefix, b.Prefix), cmp.Compare(a.Peers[0], b.Peers[0]), cmp.Compare(a.Peers[1], b.Peers[1]))
	})
}
