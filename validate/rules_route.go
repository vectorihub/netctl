package validate

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// The routing rules: whether the tables the engine made, together with any static
// overrides, are ones traffic can follow.
func init() {
	register("V-ROUTE-001", Error, "at most one active route per prefix per node", oneActiveRoute)
	register("V-ROUTE-002", Error, "every active route's next hop is a neighbour over a healthy link", nextHopIsReachable)
	register("V-ROUTE-003", Error, "no active path visits a node twice", noRepeatedNode)
	register("V-ROUTE-004", Error, "no spoke forwards traffic between two other nodes", noSpokeTransit)
	register("V-ROUTE-005", Warning, "a static override is present", staticOverrideShown)
	register("V-ROUTE-006", Warning, "a more-specific route shadows another with a different next hop", moreSpecificShadows)
}

// active returns the node's active routes with a next hop, prefixes masked, in table order.
func active(table *route.Table) []route.Route {
	var out []route.Route
	if table == nil {
		return out
	}
	for _, r := range table.Routes {
		if r.Active && r.NextHop != "" {
			r.Prefix, _ = cidr.Canonical(r.Prefix)
			out = append(out, r)
		}
	}
	return out
}

// oneActiveRoute reports a prefix that two active routes of one node both cover, since
// the node could not say which to use. Connected routes count: a prefix a node owns and
// also routes elsewhere is a conflict.
func oneActiveRoute(p *Plan) []Finding {
	var out []Finding
	for _, node := range p.Tables.Nodes() {
		count := map[netip.Prefix]int{}
		for _, r := range p.Tables[node].Routes {
			if r.Active {
				masked, _ := cidr.Canonical(r.Prefix)
				count[masked]++
			}
		}
		prefixes := make([]netip.Prefix, 0, len(count))
		for prefix, n := range count {
			if n > 1 {
				prefixes = append(prefixes, prefix)
			}
		}
		slices.SortFunc(prefixes, cidr.Compare)
		for _, prefix := range prefixes {
			out = append(out, Finding{Subject: string(node), Message: fmt.Sprintf("%d active routes for %s", count[prefix], prefix),
				Hint: "a node must have one way to reach a prefix at a time; check static overrides"})
		}
	}
	return out
}

// nextHopIsReachable checks each active route's next hop is one of the node's
// neighbours, over the link the route names, and that link is up in the graph.
func nextHopIsReachable(p *Plan) []Finding {
	if p.Graph == nil {
		return nil
	}
	var out []Finding
	for _, node := range p.Tables.Nodes() {
		edges := p.Graph.Edges(node)
		for _, r := range active(p.Tables[node]) {
			i := slices.IndexFunc(edges, func(e route.Edge) bool {
				return e.To == r.NextHop && (r.Via == "" || e.Link == r.Via)
			})
			switch {
			case i < 0:
				out = append(out, Finding{Subject: string(node), Message: fmt.Sprintf("the active route for %s goes via %s, which is not a neighbour over %s", r.Prefix, r.NextHop, via(r)),
					Hint: "the route points at a node it cannot reach"})
			case edges[i].Down:
				out = append(out, Finding{Subject: string(node), Message: fmt.Sprintf("the active route for %s goes via %s over %s, which is down", r.Prefix, r.NextHop, edges[i].Link),
					Hint: "failover has not happened: recompute the routes without the link"})
			}
		}
	}
	return out
}

// via names the link of a route for a message.
func via(r route.Route) string {
	if r.Via == "" {
		return "any link"
	}
	return "the link " + string(r.Via)
}

// noRepeatedNode checks no active path goes through the same node twice, which would be
// a loop.
func noRepeatedNode(p *Plan) []Finding {
	var out []Finding
	for _, node := range p.Tables.Nodes() {
		for _, r := range active(p.Tables[node]) {
			seen := map[types.NodeID]bool{}
			for _, hop := range r.Path {
				if seen[hop] {
					out = append(out, Finding{Subject: string(node), Message: fmt.Sprintf("the active path for %s visits %s twice: %v", r.Prefix, hop, r.Path),
						Hint: "a path that returns to a node is a loop"})
					break
				}
				seen[hop] = true
			}
		}
	}
	return out
}

// holds reports whether the node owns or has locally a prefix that contains the route's.
func holds(p *Plan, node types.NodeID, prefix netip.Prefix) bool {
	for _, set := range []route.Owned{p.Input.Owned, p.Input.Local} {
		for _, own := range set[node] {
			if cidr.Contains(own, prefix) {
				return true
			}
		}
	}
	return false
}

// noSpokeTransit reports a spoke that would forward between two other nodes: one in the
// middle of an active path, or the next hop of a route for a prefix it does not hold
// (which is how a static override can make one).
func noSpokeTransit(p *Plan) []Finding {
	role := func(n types.NodeID) types.Role {
		if p.Graph != nil {
			return p.Graph.Role(n)
		}
		if p.Topology != nil {
			for _, node := range p.Topology.Nodes {
				if node.ID == n {
					return node.Role
				}
			}
		}
		return types.RoleUnknown
	}
	type key struct {
		spoke  types.NodeID
		reason string
	}
	var out []Finding
	seen := map[key]bool{}
	report := func(spoke types.NodeID, node types.NodeID, r route.Route, why string) {
		k := key{spoke, fmt.Sprintf("%s %s", node, r.Prefix)}
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, Finding{Subject: string(spoke), Message: fmt.Sprintf("%s would forward for %s: %s", spoke, node, why),
			Hint: "only a hub forwards between other nodes; route through the hub"})
	}
	for _, node := range p.Tables.Nodes() {
		for _, r := range active(p.Tables[node]) {
			if len(r.Path) > 2 {
				for _, hop := range r.Path[1 : len(r.Path)-1] {
					if role(hop) == types.RoleSpoke {
						report(hop, node, r, fmt.Sprintf("the active path for %s is %v", r.Prefix, r.Path))
					}
				}
			}
			if len(r.Path) == 0 && role(r.NextHop) == types.RoleSpoke && !holds(p, r.NextHop, r.Prefix) {
				report(r.NextHop, node, r, fmt.Sprintf("the active route for %s goes via it, and it does not hold that prefix", r.Prefix))
			}
		}
	}
	return out
}

// staticOverrideShown always shows every static override, since it overrides what the
// engine would have done and so deserves a second look.
func staticOverrideShown(p *Plan) []Finding {
	var out []Finding
	for _, o := range p.Input.Overrides {
		out = append(out, Finding{Subject: string(o.Node), Message: fmt.Sprintf("a static override sends %s via %s", o.Prefix, o.NextHop),
			Hint: "overrides are not recomputed when the network changes; remove it once it is not needed"})
	}
	return out
}

// moreSpecificShadows warns when, on one node, an active route for a more specific
// prefix has a different next hop from an active route for a prefix that contains it:
// traffic for the inner prefix will not go where the outer route says.
func moreSpecificShadows(p *Plan) []Finding {
	var out []Finding
	for _, node := range p.Tables.Nodes() {
		routes := active(p.Tables[node])
		for _, inner := range routes {
			for _, outer := range routes {
				if outer.Prefix.Bits() < inner.Prefix.Bits() && cidr.Contains(outer.Prefix, inner.Prefix) && outer.NextHop != inner.NextHop {
					out = append(out, Finding{Subject: string(node),
						Message: fmt.Sprintf("%s via %s is more specific than %s via %s, so it shadows it", inner.Prefix, inner.NextHop, outer.Prefix, outer.NextHop),
						Hint:    "check it is meant: the longer prefix wins"})
				}
			}
		}
	}
	return out
}
