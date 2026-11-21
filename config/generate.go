package config

import (
	"cmp"
	"net/netip"
	"slices"
	"time"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// DefaultListenPort is the port a node listens on when Inputs does not say.
//
// Example:
//
//	config.DefaultListenPort is 51820.
const DefaultListenPort = 51820

// KeepaliveSeconds is the keepalive interval of a peer that needs one (rule G6).
//
// Example:
//
//	config.KeepaliveSeconds is 25.
const KeepaliveSeconds = 25

// Inputs is everything Generate needs: the topology, the address plan as plain
// data, and the routing tables. It holds no clock, no file and no health state:
// the tables already reflect which links are usable.
//
// Example:
//
//	inputs, err := config.FromPlan(topo, registry, result.Tables)
//	configs, err := config.Generate(inputs)
type Inputs struct {
	// Topology is the network: nodes, links and their health modes.
	//
	// Example:
	//
	//	fixture.ThreeSiteTopology().
	Topology *topology.Topology
	// Overlay holds each node's overlay address as a /32.
	//
	// Example:
	//
	//	{"builder-1": 10.42.240.2/32}.
	Overlay map[types.NodeID]netip.Prefix
	// Sites holds each site's address block.
	//
	// Example:
	//
	//	{"builder": 10.42.16.0/20}.
	Sites map[types.SiteID]netip.Prefix
	// Transit holds the /30 of each link that is probed, which is every direct
	// link whose health is required.
	//
	// Example:
	//
	//	{"builder-1~dgx-spark": 10.42.252.0/30}.
	Transit map[types.LinkID]netip.Prefix
	// Tables are the routing tables of every node, active and backup.
	//
	// Example:
	//
	//	result.Tables from route.Compute, or failover.Evaluate's Tables.
	Tables route.Tables
	// Health holds the thresholds written into every config. The zero value
	// means health.DefaultConfig. The times have to be whole seconds.
	//
	// Example:
	//
	//	health.DefaultConfig().
	Health health.Config
	// ListenPort is the port every node listens on. Zero means DefaultListenPort.
	//
	// Example:
	//
	//	51820.
	ListenPort int
}

// held returns the prefix of the given length the registry holds for the owner.
func held(registry *ipam.IPAM, owner ipam.Owner, bits int) (netip.Prefix, bool) {
	for _, allocation := range registry.ByOwner(owner) {
		if allocation.State == ipam.Allocated && allocation.Prefix.Bits() == bits {
			return allocation.Prefix, true
		}
	}
	return netip.Prefix{}, false
}

// probed reports whether a link is probed: a direct link whose health must be
// proven. A link that has not been normalised and has no health mode is treated
// as its kind's default, which for a direct link is required.
func probed(l topology.Link) bool {
	required := l.Health == topology.HealthRequired || (l.Health == topology.HealthUnset && l.Kind == types.LinkDirect)
	return l.Kind == types.LinkDirect && required
}

// FromPlan collects Generate's inputs from a topology and the address plan it was
// planned with: each node's overlay /32, each advertised site's block, and the
// /30 of each probed link. It reports every address the plan lacks at once
// (NOT_FOUND), and a nil topology or registry as INVALID_VALUE.
//
// Example:
//
//	inputs, err := config.FromPlan(topo, registry, result.Tables)
func FromPlan(t *topology.Topology, registry *ipam.IPAM, tables route.Tables) (Inputs, error) {
	if t == nil || registry == nil {
		return Inputs{}, types.Errf(types.ErrInvalidValue, "FromPlan", "it needs a topology and an address plan, got nil")
	}
	in := Inputs{
		Topology: t, Tables: tables,
		Overlay: map[types.NodeID]netip.Prefix{}, Sites: map[types.SiteID]netip.Prefix{}, Transit: map[types.LinkID]netip.Prefix{},
	}
	var problems types.List
	for _, node := range t.Nodes {
		if prefix, ok := held(registry, ipam.Node(string(node.ID)), 32); ok {
			in.Overlay[node.ID] = prefix
		} else {
			problems.Add(types.Errf(types.ErrNotFound, "node "+string(node.ID), "the address plan holds no overlay /32 for it"))
		}
		for _, advert := range node.Advertises {
			if !advert.Site {
				continue
			}
			if _, done := in.Sites[node.Site]; done {
				continue
			}
			found := false
			for _, allocation := range registry.ByOwner(ipam.Site(string(node.Site))) {
				if allocation.State == ipam.Allocated {
					in.Sites[node.Site], found = allocation.Prefix, true
					break
				}
			}
			if !found {
				problems.Add(types.Errf(types.ErrNotFound, "site "+string(node.Site), "%s advertises it, but the address plan holds no block for it", node.ID))
			}
		}
	}
	for _, l := range t.Links {
		if !probed(l) {
			continue
		}
		id := types.LinkIDOf(l.A, l.B)
		if prefix, ok := held(registry, ipam.Link(string(id)), 30); ok {
			in.Transit[id] = prefix
		} else {
			problems.Add(types.Errf(types.ErrNotFound, "link "+string(id), "it is probed, but the address plan holds no transit /30 for it"))
		}
	}
	if err := problems.Err(); err != nil {
		return Inputs{}, err
	}
	return in, nil
}

// natted reports whether a node cannot be dialled from outside. A node that never
// said is assumed to be behind NAT, which is the safe assumption.
func natted(n topology.Node) bool { return n.NAT != topology.NATNone }

// seconds converts a duration to whole seconds, and reports false if it is not a
// whole number of them.
func seconds(d time.Duration) (int, bool) { return int(d / time.Second), d%time.Second == 0 }

// generator holds what Generate works out once and uses for every node.
type generator struct {
	// in is the inputs, with defaults filled in.
	in Inputs
	// nodes are the topology's nodes by ID.
	nodes map[types.NodeID]topology.Node
	// links are the topology's links, sorted by ID.
	links []topology.Link
	// cfg is the health thresholds in force.
	cfg health.Config
	// problems collects every problem found, so they are reported together.
	problems types.List
}

// hosts returns the two addresses of a probed link's /30: the lower to the lexically
// smaller node, as the address plan hands them out.
func hosts(prefix netip.Prefix, a, b types.NodeID) (map[types.NodeID]netip.Addr, bool) {
	masked, _ := cidr.Canonical(prefix)
	if !masked.IsValid() || masked.Bits() != 30 || !masked.Addr().Is4() {
		return nil, false
	}
	low := masked.Addr().Next()
	return map[types.NodeID]netip.Addr{min(a, b): low, max(a, b): low.Next()}, true
}

// Generate builds the config of every node from one plan, in one pass (rule G1),
// sorted by node ID, each with its hash and the plan ID filled in.
//
// The rules it applies:
//
//   - G2: a peer for every link the node is an end of, and for no other.
//   - G3: allowed prefixes are the peer's probe address and every prefix whose
//     active route on the node has the peer as next hop. See AllowedPrefixes.
//   - G4: the routes of the node's table are copied, active and backup. Connected
//     routes are the node's own addresses and are left out.
//   - G5: forwarding is true for a hub and for no other node.
//   - G6: keepalive is 25 seconds on a peer where the node is behind NAT or the
//     peer needs traversal, and 0 otherwise.
//   - G7: endpoints are listed only for a peer that is publicly reachable. A peer
//     behind NAT has none. needs_traversal is set when the peer has no endpoint
//     and the node cannot be dialled either, so neither end can open the path alone:
//     two spokes behind NAT. A hub never needs it: the spoke dials the hub.
//   - G8 and G9: every list is sorted and the hash is the SHA-256 of the canonical
//     JSON. See NodeConfig.Encode.
//   - G10: the plan ID is the topology's name and eight hex digits derived from
//     every node's content. See PlanID.
//
// Every problem is found before it returns, and reported at once: a missing
// overlay address, site block or transit /30 (NOT_FOUND); a missing routing table
// or a route whose next hop is not a peer (NOT_FOUND, INVALID_CONFIG); a prefix
// active through two peers, which WireGuard cannot do (INVALID_CONFIG); a bad
// health setting (INVALID_CONFIG); two links to the same neighbour or a link with
// an unknown end (INVALID_TOPOLOGY).
//
// Example:
//
//	configs, err := config.Generate(inputs)
//	for _, c := range configs { data, _ := c.Encode(); save(c.Node, data) }
func Generate(in Inputs) ([]*NodeConfig, error) {
	if in.Topology == nil {
		return nil, types.Errf(types.ErrInvalidTopology, "Generate", "there is no topology")
	}
	g := &generator{in: in, nodes: map[types.NodeID]topology.Node{}, cfg: in.Health}
	if g.cfg == (health.Config{}) {
		g.cfg = health.DefaultConfig()
	}
	if err := g.cfg.Validate(); err != nil {
		return nil, err
	}
	for name, d := range map[string]time.Duration{"probe_interval": g.cfg.ProbeInterval, "hold_down": g.cfg.HoldDown} {
		if _, whole := seconds(d); !whole {
			g.problems.Add(types.Errf(types.ErrInvalidConfig, "health "+name, "it is %v, which is not a whole number of seconds", d))
		}
	}
	if g.in.ListenPort == 0 {
		g.in.ListenPort = DefaultListenPort
	}
	if g.in.ListenPort < 1 || g.in.ListenPort > 65535 {
		g.problems.Add(types.Errf(types.ErrInvalidConfig, "listen port", "it is %d, want 1 to 65535", g.in.ListenPort))
	}
	for _, n := range in.Topology.Nodes {
		g.nodes[n.ID] = n
	}
	g.links = slices.Clone(in.Topology.Links)
	slices.SortFunc(g.links, func(a, b topology.Link) int {
		return cmp.Or(cmp.Compare(types.LinkIDOf(a.A, a.B), types.LinkIDOf(b.A, b.B)), cmp.Compare(a.Kind, b.Kind))
	})

	ids := make([]types.NodeID, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]*NodeConfig, 0, len(ids))
	for _, id := range ids {
		out = append(out, g.nodeConfig(g.nodes[id]))
	}
	if err := g.problems.Err(); err != nil {
		return nil, err
	}
	for _, c := range out {
		if _, err := c.Encode(); err != nil {
			return nil, err
		}
	}
	id, err := PlanID(in.Topology.Name, out)
	if err != nil {
		return nil, err
	}
	for _, c := range out {
		c.PlanID = id
		if _, err := c.Encode(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// nodeConfig works out one node's config, recording any problem it meets. Its
// hash and plan ID are not set; Generate does that once every node is done.
func (g *generator) nodeConfig(n topology.Node) *NodeConfig {
	subject := "node " + string(n.ID)
	c := &NodeConfig{
		Schema: Schema, Node: n.ID, Role: n.Role.String(), Forwarding: n.Role == types.RoleHub, // G5
		Overlay: Overlay{ListenPort: g.in.ListenPort},
	}
	if addr, ok := g.in.Overlay[n.ID]; ok {
		c.Overlay.Address = addr
	} else {
		g.problems.Add(types.Errf(types.ErrNotFound, subject, "there is no overlay address for it"))
	}
	if addr := c.Overlay.Address; addr.IsValid() {
		c.Advertise = append(c.Advertise, addr)
	}
	for _, advert := range n.Advertises {
		switch {
		case advert.Site:
			block, ok := g.in.Sites[n.Site]
			if !ok {
				g.problems.Add(types.Errf(types.ErrNotFound, subject, "it advertises its site, but there is no block for site %q", n.Site))
				continue
			}
			c.Advertise = append(c.Advertise, block)
			site := block
			c.SitePrefix = &site
		case advert.Prefix.IsValid():
			c.Advertise = append(c.Advertise, advert.Prefix)
		}
	}

	probes := map[types.NodeID]netip.Addr{}
	seen := map[types.NodeID]bool{}
	for _, l := range g.links {
		var peerID types.NodeID
		switch n.ID {
		case l.A:
			peerID = l.B
		case l.B:
			peerID = l.A
		default:
			continue
		}
		id := types.LinkIDOf(l.A, l.B)
		peer, known := g.nodes[peerID]
		switch {
		case !known:
			g.problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(id), "%s is not a node of the topology", peerID))
			continue
		case peerID == n.ID:
			g.problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(id), "it joins %s to itself", n.ID))
			continue
		case seen[peerID]:
			g.problems.Add(types.Errf(types.ErrInvalidTopology, subject, "it has more than one link to %s, so it would have two peers with the same name", peerID))
			continue
		}
		seen[peerID] = true
		p := Peer{Node: peerID, Link: id, Role: peerRole(l, peer), PublicKeyRef: "node:" + string(peerID), Endpoints: []netip.AddrPort{}}
		if peer.NAT == topology.NATNone { // G7
			p.Endpoints = slices.Clone(peer.Endpoints)
		}
		p.NeedsTraversal = len(p.Endpoints) == 0 && natted(n)
		if natted(n) || p.NeedsTraversal { // G6
			p.Keepalive = KeepaliveSeconds
		}
		if probed(l) {
			g.addProbe(c, &p, n.ID, id, l, probes)
		}
		c.Peers = append(c.Peers, p)
	}

	table := g.in.Tables[n.ID]
	if table == nil {
		g.problems.Add(types.Errf(types.ErrNotFound, subject, "there is no routing table for it"))
	}
	allowed, conflicts := AllowedPrefixes(table, probes) // G3
	for _, conflict := range conflicts {
		g.problems.Add(types.Errf(types.ErrInvalidConfig, subject, "%s would be allowed through both %s and %s, and WireGuard keeps a prefix with one peer", conflict.Prefix, conflict.Peers[0], conflict.Peers[1]))
	}
	for i := range c.Peers {
		c.Peers[i].AllowedPrefixes = allowed[c.Peers[i].Node]
	}
	if table != nil {
		for _, r := range table.Routes { // G4
			if r.NextHop == "" {
				continue
			}
			if !seen[r.NextHop] && r.Active {
				g.problems.Add(types.Errf(types.ErrInvalidConfig, subject, "the active route for %s goes via %s, which is not one of its peers", r.Prefix, r.NextHop))
			}
			c.Routes = append(c.Routes, Route{Prefix: r.Prefix, Via: r.NextHop, Metric: r.Metric, Active: r.Active, Backup: r.Source == route.SourceBackup})
		}
	}

	interval, _ := seconds(g.cfg.ProbeInterval)
	hold, _ := seconds(g.cfg.HoldDown)
	for i := range c.Health.Probes {
		c.Health.Probes[i].Interval = interval
	}
	c.Health.FailThreshold, c.Health.RecoverThreshold, c.Health.HoldDownSeconds = g.cfg.FailThreshold, g.cfg.RecoverThreshold, hold
	return c
}

// addProbe gives a direct link's peer its probe addresses from the link's /30, and
// adds the probe to the node's health section. A missing or unusable /30 is a
// problem.
func (g *generator) addProbe(c *NodeConfig, p *Peer, self types.NodeID, id types.LinkID, l topology.Link, probes map[types.NodeID]netip.Addr) {
	prefix, ok := g.in.Transit[id]
	if !ok {
		g.problems.Add(types.Errf(types.ErrNotFound, "link "+string(id), "it is probed, but there is no transit /30 for it"))
		return
	}
	ends, ok := hosts(prefix, l.A, l.B)
	if !ok {
		g.problems.Add(types.Errf(types.ErrInvalidConfig, "link "+string(id), "its transit block %s is not an IPv4 /30", prefix))
		return
	}
	local, remote := ends[self], ends[p.Node]
	p.Probe = &Probe{Local: netip.PrefixFrom(local, 30), Remote: remote}
	probes[p.Node] = remote
	c.Health.Probes = append(c.Health.Probes, ProbeTarget{Link: id, Target: remote})
}

// peerRole says what kind of peer the other end of a link is.
func peerRole(l topology.Link, peer topology.Node) PeerRole {
	switch {
	case l.Kind == types.LinkDirect:
		return PeerDirect
	case peer.Role == types.RoleHub:
		return PeerHub
	}
	return PeerSpoke
}

// ByNode returns the configs keyed by node, for a caller that looks them up.
//
// Example:
//
//	config.ByNode(configs)["builder-1"] is builder-1's config.
func ByNode(configs []*NodeConfig) map[types.NodeID]*NodeConfig {
	out := make(map[types.NodeID]*NodeConfig, len(configs))
	for _, c := range configs {
		out[c.Node] = c
	}
	return out
}
