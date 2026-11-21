package sim

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// Options tune flow derivation.
type Options struct {
	// AllowAsymmetric lists pairs of nodes whose flows may take different paths
	// there and back without failing the gate. A pair applies in both directions.
	// The topology format has no field for this yet, so the caller supplies it.
	AllowAsymmetric [][2]types.NodeID
}

// negativeCandidates are the addresses tried, in order, for the negative flows.
// The first one outside the supernet is used. They are well-known addresses that
// are never part of a private overlay.
var negativeCandidates = []netip.Addr{
	netip.MustParseAddr("192.168.1.1"),
	netip.MustParseAddr("172.31.255.1"),
	netip.MustParseAddr("198.51.100.1"),
	netip.MustParseAddr("203.0.113.1"),
	netip.MustParseAddr("8.8.8.8"),
}

// held returns the lowest allocated prefix of the given length that the
// registry holds for owner.
func held(registry *ipam.IPAM, owner ipam.Owner, bits int) (netip.Prefix, bool) {
	for _, allocation := range registry.ByOwner(owner) {
		if allocation.State == ipam.Allocated && allocation.Prefix.Bits() == bits {
			return allocation.Prefix, true
		}
	}
	return netip.Prefix{}, false
}

// Flows derives everything worth simulating for a network, not only what the
// user asked for, in five families:
//
//  1. Intent flows. Every allow intent is expanded to pairs of nodes. Each pair
//     is already an overlay flow, so it is merged into that flow and marked
//     Intent; the flow is delivered both ways like every other.
//  2. Deny flows. One flow for each deny intent, from the first node its "from"
//     selector picks to the first node its "to" selector picks. Phase 1 does not
//     enforce policy, so it is reported as reachable, with a note, and Phase 3
//     inherits the list of what the firewall must block.
//  3. Overlay flows. Every node's overlay address to every other node's. Overlay
//     addresses are mutually reachable unless a deny intent says otherwise.
//  4. Site flows. The first usable address of each advertised site block (the
//     only address of a /32 block) to the same of every other advertised site,
//     injected at the node that advertises the site.
//  5. Negative flows. One per node, from its overlay address to an address
//     outside the supernet. These must not be delivered anywhere.
//
// The result is sorted by label, so the order never depends on how the topology
// was written. For the three-site network it is 6 overlay, 6 site, 1 deny and 3
// negative flows, 16 in all.
//
// Flows returns an error, and no flows, rather than quietly testing less than
// was asked: a node with no overlay /32 in the address plan (NOT_FOUND), an
// advertised site with no block (NOT_FOUND), an intent whose selector picks
// no node (INVALID_TOPOLOGY), or a supernet so large that no candidate address
// lies outside it for the negative flows (INVALID_VALUE). Every problem is reported at once.
//
// Example: for a topology with nodes a, b and c, each advertising its site, and
// one deny intent, Flows returns 6 overlay, 6 site, 1 deny and 3 negative flows.
func Flows(t *topology.Topology, registry *ipam.IPAM, opts Options) ([]Flow, error) {
	if t == nil || registry == nil {
		return nil, types.Errf(types.ErrInvalidValue, "Flows", "it needs a topology and an address plan, got nil")
	}
	var problems types.List
	nodes := make([]topology.Node, len(t.Nodes))
	copy(nodes, t.Nodes)
	slices.SortFunc(nodes, func(a, b topology.Node) int { return strings.Compare(string(a.ID), string(b.ID)) })

	overlay := map[types.NodeID]netip.Addr{}
	for _, node := range nodes {
		if prefix, ok := held(registry, ipam.Node(string(node.ID)), 32); ok {
			overlay[node.ID] = prefix.Addr()
		} else {
			problems.Add(types.Errf(types.ErrNotFound, "node "+string(node.ID), "the address plan holds no overlay /32 for it"))
		}
	}

	type advertised struct {
		site types.SiteID
		node types.NodeID
		addr netip.Addr
	}
	var sites []advertised
	seenSite := map[types.SiteID]bool{}
	for _, node := range nodes {
		for _, advert := range node.Advertises {
			if !advert.Site || seenSite[node.Site] {
				continue
			}
			seenSite[node.Site] = true
			var block netip.Prefix
			ok := false
			for _, allocation := range registry.ByOwner(ipam.Site(string(node.Site))) {
				if allocation.State == ipam.Allocated {
					block, ok = allocation.Prefix, true
					break
				}
			}
			if !ok {
				problems.Add(types.Errf(types.ErrNotFound, "site "+string(node.Site), "%s advertises it, but the address plan holds no block for it", node.ID))
				continue
			}
			// The first usable address, or the only one of a /32: the test
			// address must stay inside the site's own block.
			addr := block.Addr()
			if block.Bits() < 32 {
				addr = addr.Next()
			}
			sites = append(sites, advertised{site: node.Site, node: node.ID, addr: addr})
		}
	}
	slices.SortFunc(sites, func(a, b advertised) int { return strings.Compare(string(a.site), string(b.site)) })

	allowed := map[[2]types.NodeID]bool{}
	for _, pair := range opts.AllowAsymmetric {
		allowed[pair] = true
		allowed[[2]types.NodeID{pair[1], pair[0]}] = true
	}

	var flows []Flow
	byPair := map[[2]types.NodeID]int{}
	for _, from := range nodes {
		for _, to := range nodes {
			if from.ID == to.ID || !overlay[from.ID].IsValid() || !overlay[to.ID].IsValid() {
				continue
			}
			byPair[[2]types.NodeID{from.ID, to.ID}] = len(flows)
			flows = append(flows, Flow{
				Label:  fmt.Sprintf("%s→%s (overlay)", from.ID, to.ID),
				Family: FamilyOverlay, Src: overlay[from.ID], Dst: overlay[to.ID], From: from.ID, To: to.ID,
				AllowAsymmetric: allowed[[2]types.NodeID{from.ID, to.ID}],
			})
		}
	}
	for _, from := range sites {
		for _, to := range sites {
			if from.site == to.site {
				continue
			}
			flows = append(flows, Flow{
				Label:  fmt.Sprintf("%s→%s (site)", from.site, to.site),
				Family: FamilySite, Src: from.addr, Dst: to.addr, From: from.node, To: to.node,
				AllowAsymmetric: allowed[[2]types.NodeID{from.node, to.node}],
			})
		}
	}

	denies := 0
	for i, intent := range t.Intents {
		fromNodes, toNodes := intent.From.Resolve(t), intent.To.Resolve(t)
		subject := fmt.Sprintf("intent %d", i+1)
		if len(fromNodes) == 0 || len(toNodes) == 0 {
			problems.Add(types.Errf(types.ErrInvalidTopology, subject, "%s → %s picks no node", intent.From, intent.To))
			continue
		}
		switch intent.Action {
		case topology.ActionAllow:
			for _, from := range fromNodes {
				for _, to := range toNodes {
					if at, ok := byPair[[2]types.NodeID{from, to}]; ok {
						flows[at].Intent = true
					}
				}
			}
		case topology.ActionDeny:
			denies++
			from, to := fromNodes[0], toNodes[0]
			if !overlay[from].IsValid() || !overlay[to].IsValid() {
				continue
			}
			label := fmt.Sprintf("%s→%s (deny #%d: %s→%s", from, to, denies, intent.From, intent.To)
			if len(intent.Ports) > 0 {
				ports := make([]string, len(intent.Ports))
				for p, port := range intent.Ports {
					ports[p] = fmt.Sprint(port)
				}
				label += " ports " + strings.Join(ports, ",")
			}
			flows = append(flows, Flow{
				Label: label + ")", Family: FamilyDeny, Src: overlay[from], Dst: overlay[to], From: from, To: to,
				Ports: slices.Clone(intent.Ports),
				Note:  "reachable: policy is not enforced yet, so a firewall must block this in Phase 3",
			})
		}
	}

	supernet := registry.Snapshot().Supernet
	outside := netip.Addr{}
	for _, candidate := range negativeCandidates {
		if !supernet.Contains(candidate) {
			outside = candidate
			break
		}
	}
	if !outside.IsValid() {
		problems.Add(types.Errf(types.ErrInvalidValue, "Flows",
			"the supernet %s holds every candidate address for the negative flows, so none can test an address outside it", supernet))
	}
	for _, from := range nodes {
		if !outside.IsValid() || !overlay[from.ID].IsValid() {
			continue
		}
		flows = append(flows, Flow{
			Label:  fmt.Sprintf("%s→%s (negative)", from.ID, outside),
			Family: FamilyNegative, Src: overlay[from.ID], Dst: outside, From: from.ID,
			Note: "outside the supernet: must not be delivered anywhere",
		})
	}

	if err := problems.Err(); err != nil {
		return nil, err
	}
	slices.SortStableFunc(flows, func(a, b Flow) int { return strings.Compare(a.Label, b.Label) })
	return flows, nil
}
