package config

import (
	"cmp"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// PrefixMove is a prefix that left one peer's allowed list and joined another's:
// what failover does, and what failback undoes.
//
// Example:
//
//	PrefixMove{Prefix: 10.42.32.0/20, From: "dgx-spark", To: "aws-hub"}.
type PrefixMove struct {
	// Prefix is the prefix that moved.
	//
	// Example:
	//
	//	10.42.32.0/20.
	Prefix netip.Prefix `json:"prefix"`
	// From is the peer that carried it before.
	//
	// Example:
	//
	//	"dgx-spark".
	From types.NodeID `json:"from"`
	// To is the peer that carries it now.
	//
	// Example:
	//
	//	"aws-hub".
	To types.NodeID `json:"to"`
}

// PeerChange is what changed about a peer that is in both configs, apart from
// prefixes that moved to or from another peer.
//
// Example:
//
//	PeerChange{Node: "aws-hub", Added: [10.42.50.0/24]} says one more prefix is
//	routed through the hub.
type PeerChange struct {
	// Node is the peer.
	//
	// Example:
	//
	//	"aws-hub".
	Node types.NodeID `json:"node"`
	// Added lists allowed prefixes the peer gained that did not come from
	// another peer, sorted.
	//
	// Example:
	//
	//	[10.42.50.0/24].
	Added []netip.Prefix `json:"added,omitempty"`
	// Removed lists allowed prefixes the peer lost that did not go to another
	// peer, sorted.
	//
	// Example:
	//
	//	[10.42.60.0/24].
	Removed []netip.Prefix `json:"removed,omitempty"`
	// Fields names the other fields of the peer that changed, in the order the
	// config writes them.
	//
	// Example:
	//
	//	["endpoints", "keepalive_seconds"].
	Fields []string `json:"fields,omitempty"`
}

// RouteChange is a route that is in both configs, with a different state.
//
// Example:
//
//	RouteChange{Old: the backup via aws-hub, inactive, New: the same route, active}.
type RouteChange struct {
	// Old is the route as it was.
	//
	// Example:
	//
	//	10.42.32.0/20 via aws-hub, metric 40, inactive, backup.
	Old Route `json:"old"`
	// New is the route as it is.
	//
	// Example:
	//
	//	10.42.32.0/20 via aws-hub, metric 40, active.
	New Route `json:"new"`
}

// NodeDiff is what changed between two configs of one node. The plan ID and the
// hash are not compared: they follow from the rest.
//
// Example:
//
//	between builder-1's healthy config and its failed-over one, Moves lists the two
//	prefixes that went from dgx-spark to aws-hub, and RoutesChanged the two backups
//	that became active.
type NodeDiff struct {
	// Node is the node.
	//
	// Example:
	//
	//	"builder-1".
	Node types.NodeID `json:"node"`
	// PeersAdded lists peers that are only in the new config.
	//
	// Example:
	//
	//	a node linked to a new site.
	PeersAdded []Peer `json:"peers_added,omitempty"`
	// PeersRemoved lists peers that are only in the old config.
	//
	// Example:
	//
	//	a node whose link was taken out.
	PeersRemoved []Peer `json:"peers_removed,omitempty"`
	// PeersChanged lists peers in both configs that changed.
	//
	// Example:
	//
	//	a peer whose endpoints changed.
	PeersChanged []PeerChange `json:"peers_changed,omitempty"`
	// Moves lists allowed prefixes that went from one peer to another.
	//
	// Example:
	//
	//	10.42.32.0/20 from dgx-spark to aws-hub.
	Moves []PrefixMove `json:"moves,omitempty"`
	// RoutesAdded lists routes only in the new config.
	//
	// Example:
	//
	//	a route to a new site.
	RoutesAdded []Route `json:"routes_added,omitempty"`
	// RoutesRemoved lists routes only in the old config.
	//
	// Example:
	//
	//	the direct route, when the direct link is down.
	RoutesRemoved []Route `json:"routes_removed,omitempty"`
	// RoutesChanged lists routes in both configs, identified by prefix and next
	// hop, whose metric, active flag or backup flag changed.
	//
	// Example:
	//
	//	a backup that became active.
	RoutesChanged []RouteChange `json:"routes_changed,omitempty"`
	// Other names the top-level fields that changed: role, forwarding, overlay,
	// site_prefix, advertise, health or policy.
	//
	// Example:
	//
	//	["advertise"].
	Other []string `json:"other,omitempty"`
}

// Empty reports whether nothing changed.
//
// Example:
//
//	the diff of a config with itself is Empty.
func (d NodeDiff) Empty() bool {
	return len(d.PeersAdded)+len(d.PeersRemoved)+len(d.PeersChanged)+len(d.Moves)+
		len(d.RoutesAdded)+len(d.RoutesRemoved)+len(d.RoutesChanged)+len(d.Other) == 0
}

// routeKey says which route is which across two configs.
type routeKey struct {
	// prefix and via are the route's destination and next hop.
	prefix netip.Prefix
	via    types.NodeID
}

// prefixSet returns the prefixes as a set.
func prefixSet(prefixes []netip.Prefix) map[netip.Prefix]bool {
	set := make(map[netip.Prefix]bool, len(prefixes))
	for _, p := range prefixes {
		set[p] = true
	}
	return set
}

// minus returns the prefixes of a that are not in b, in a's order.
func minus(a []netip.Prefix, b map[netip.Prefix]bool) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range a {
		if !b[p] {
			out = append(out, p)
		}
	}
	return out
}

// peerFields names the fields of a peer, other than its allowed prefixes, that differ.
func peerFields(a, b Peer) []string {
	var out []string
	check := func(name string, same bool) {
		if !same {
			out = append(out, name)
		}
	}
	check("link", a.Link == b.Link)
	check("role", a.Role == b.Role)
	check("public_key_ref", a.PublicKeyRef == b.PublicKeyRef)
	check("endpoints", slices.Equal(a.Endpoints, b.Endpoints))
	check("needs_traversal", a.NeedsTraversal == b.NeedsTraversal)
	check("keepalive_seconds", a.Keepalive == b.Keepalive)
	check("probe", reflect.DeepEqual(a.Probe, b.Probe))
	return out
}

// Diff says what changed between the old and new config of a node. Both are
// compared in canonical form and neither is changed. Peers are matched by node,
// routes by prefix and next hop. A prefix that left one peer's allowed list and
// joined another's is one Move and not two unrelated changes, which is how a
// failover reads: the far node's prefixes moved from the direct peer to the hub.
//
// Diff of configs for different nodes compares them as they are; callers match
// nodes first (see DiffPlans).
//
// Example:
//
//	d := config.Diff(healthy, failedOver)
//	for _, m := range d.Moves { fmt.Println(m.Prefix, m.From, "->", m.To) }
func Diff(old, updated *NodeConfig) NodeDiff {
	a, b := old.Clone(), updated.Clone()
	a.Canonicalise()
	b.Canonicalise()
	d := NodeDiff{Node: updated.Node}

	oldPeers, newPeers := map[types.NodeID]Peer{}, map[types.NodeID]Peer{}
	for _, p := range a.Peers {
		oldPeers[p.Node] = p
	}
	for _, p := range b.Peers {
		newPeers[p.Node] = p
	}
	type gone struct {
		peer   types.NodeID
		prefix netip.Prefix
	}
	var lost, gained []gone
	changed := map[types.NodeID]*PeerChange{}
	for _, p := range a.Peers {
		now, still := newPeers[p.Node]
		if !still {
			d.PeersRemoved = append(d.PeersRemoved, p)
			continue
		}
		change := &PeerChange{Node: p.Node, Fields: peerFields(p, now)}
		for _, prefix := range minus(p.AllowedPrefixes, prefixSet(now.AllowedPrefixes)) {
			lost = append(lost, gone{p.Node, prefix})
		}
		for _, prefix := range minus(now.AllowedPrefixes, prefixSet(p.AllowedPrefixes)) {
			gained = append(gained, gone{p.Node, prefix})
		}
		changed[p.Node] = change
	}
	for _, p := range b.Peers {
		if _, had := oldPeers[p.Node]; !had {
			d.PeersAdded = append(d.PeersAdded, p)
		}
	}
	used := map[int]bool{}
	for _, l := range lost {
		moved := false
		for i, g := range gained {
			if !used[i] && g.prefix == l.prefix && g.peer != l.peer {
				used[i], moved = true, true
				d.Moves = append(d.Moves, PrefixMove{Prefix: l.prefix, From: l.peer, To: g.peer})
				break
			}
		}
		if !moved {
			changed[l.peer].Removed = append(changed[l.peer].Removed, l.prefix)
		}
	}
	for i, g := range gained {
		if !used[i] {
			changed[g.peer].Added = append(changed[g.peer].Added, g.prefix)
		}
	}
	for _, p := range b.Peers {
		if change, ok := changed[p.Node]; ok && (len(change.Added)+len(change.Removed)+len(change.Fields) > 0) {
			d.PeersChanged = append(d.PeersChanged, *change)
		}
	}
	slices.SortFunc(d.Moves, func(x, y PrefixMove) int {
		return cmp.Or(cidr.Compare(x.Prefix, y.Prefix), cmp.Compare(x.From, y.From), cmp.Compare(x.To, y.To))
	})

	oldRoutes, newRoutes := map[routeKey]Route{}, map[routeKey]Route{}
	for _, r := range a.Routes {
		oldRoutes[routeKey{r.Prefix, r.Via}] = r
	}
	for _, r := range b.Routes {
		newRoutes[routeKey{r.Prefix, r.Via}] = r
	}
	for _, r := range a.Routes {
		now, still := newRoutes[routeKey{r.Prefix, r.Via}]
		switch {
		case !still:
			d.RoutesRemoved = append(d.RoutesRemoved, r)
		case now != r:
			d.RoutesChanged = append(d.RoutesChanged, RouteChange{Old: r, New: now})
		}
	}
	for _, r := range b.Routes {
		if _, had := oldRoutes[routeKey{r.Prefix, r.Via}]; !had {
			d.RoutesAdded = append(d.RoutesAdded, r)
		}
	}

	for _, field := range []struct {
		name string
		same bool
	}{
		{"role", a.Role == b.Role},
		{"forwarding", a.Forwarding == b.Forwarding},
		{"overlay", a.Overlay == b.Overlay},
		{"site_prefix", reflect.DeepEqual(a.SitePrefix, b.SitePrefix)},
		{"advertise", slices.Equal(a.Advertise, b.Advertise)},
		{"health", reflect.DeepEqual(a.Health, b.Health)},
		{"policy", reflect.DeepEqual(a.Policy, b.Policy)},
	} {
		if !field.same {
			d.Other = append(d.Other, field.name)
		}
	}
	return d
}

// Text writes the diff for people: the node, then one line for each change. It
// writes nothing for an empty diff.
//
// Example:
//
//	builder-1
//	  moved 10.42.32.0/20 from dgx-spark to aws-hub
//	  route 10.42.32.0/20 via aws-hub: now active
func (d NodeDiff) Text() string {
	if d.Empty() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", d.Node)
	for _, p := range d.PeersAdded {
		fmt.Fprintf(&b, "  peer %s added (link %s)\n", p.Node, p.Link)
	}
	for _, p := range d.PeersRemoved {
		fmt.Fprintf(&b, "  peer %s removed (link %s)\n", p.Node, p.Link)
	}
	for _, m := range d.Moves {
		fmt.Fprintf(&b, "  moved %s from %s to %s\n", m.Prefix, m.From, m.To)
	}
	for _, c := range d.PeersChanged {
		for _, p := range c.Added {
			fmt.Fprintf(&b, "  peer %s: %s allowed\n", c.Node, p)
		}
		for _, p := range c.Removed {
			fmt.Fprintf(&b, "  peer %s: %s no longer allowed\n", c.Node, p)
		}
		if len(c.Fields) > 0 {
			fmt.Fprintf(&b, "  peer %s: changed %s\n", c.Node, strings.Join(c.Fields, ", "))
		}
	}
	for _, r := range d.RoutesRemoved {
		fmt.Fprintf(&b, "  route %s via %s removed\n", r.Prefix, r.Via)
	}
	for _, r := range d.RoutesAdded {
		fmt.Fprintf(&b, "  route %s via %s added (%s)\n", r.Prefix, r.Via, routeState(r))
	}
	for _, c := range d.RoutesChanged {
		fmt.Fprintf(&b, "  route %s via %s: %s, metric %d, now %s, metric %d\n", c.New.Prefix, c.New.Via, routeState(c.Old), c.Old.Metric, routeState(c.New), c.New.Metric)
	}
	for _, name := range d.Other {
		fmt.Fprintf(&b, "  %s changed\n", name)
	}
	return b.String()
}

// routeState describes a route's use: active, or an inactive backup.
func routeState(r Route) string {
	switch {
	case r.Active:
		return "active"
	case r.Backup:
		return "backup"
	}
	return "inactive"
}

// PlanDiff is what changed between two plans: nodes that appeared or went, and the
// diff of each node that is in both and changed.
//
// Example:
//
//	between the healthy plan and the failed-over one, Nodes holds builder-1 and
//	dgx-spark, and aws-hub is not listed.
type PlanDiff struct {
	// NodesAdded lists nodes only in the new plan, sorted.
	//
	// Example:
	//
	//	["mac-studio"].
	NodesAdded []types.NodeID `json:"nodes_added,omitempty"`
	// NodesRemoved lists nodes only in the old plan, sorted.
	//
	// Example:
	//
	//	[].
	NodesRemoved []types.NodeID `json:"nodes_removed,omitempty"`
	// Nodes lists the diff of each node in both plans whose config changed,
	// sorted by node.
	//
	// Example:
	//
	//	builder-1 and dgx-spark after a failover.
	Nodes []NodeDiff `json:"nodes,omitempty"`
}

// Empty reports whether the plans are the same.
//
// Example:
//
//	the diff of a plan with itself is Empty.
func (d PlanDiff) Empty() bool { return len(d.NodesAdded)+len(d.NodesRemoved)+len(d.Nodes) == 0 }

// DiffPlans compares two plans node by node. Neither is changed.
//
// Example:
//
//	d := config.DiffPlans(healthyConfigs, failedOverConfigs)
func DiffPlans(old, updated []*NodeConfig) PlanDiff {
	before, after := ByNode(old), ByNode(updated)
	var d PlanDiff
	nodes := map[types.NodeID]bool{}
	for n := range before {
		nodes[n] = true
	}
	for n := range after {
		nodes[n] = true
	}
	ordered := make([]types.NodeID, 0, len(nodes))
	for n := range nodes {
		ordered = append(ordered, n)
	}
	slices.Sort(ordered)
	for _, n := range ordered {
		o, hadOld := before[n]
		u, hasNew := after[n]
		switch {
		case !hadOld:
			d.NodesAdded = append(d.NodesAdded, n)
		case !hasNew:
			d.NodesRemoved = append(d.NodesRemoved, n)
		default:
			if nd := Diff(o, u); !nd.Empty() {
				d.Nodes = append(d.Nodes, nd)
			}
		}
	}
	return d
}

// Text writes the plan diff for people, node by node.
//
// Example:
//
//	a plan diff with one changed node writes that node's NodeDiff.Text.
func (d PlanDiff) Text() string {
	var b strings.Builder
	for _, n := range d.NodesAdded {
		fmt.Fprintf(&b, "%s added\n", n)
	}
	for _, n := range d.NodesRemoved {
		fmt.Fprintf(&b, "%s removed\n", n)
	}
	for _, nd := range d.Nodes {
		b.WriteString(nd.Text())
	}
	return b.String()
}
