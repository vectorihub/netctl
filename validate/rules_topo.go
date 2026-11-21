package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// The topology rules: whether the network described can carry anything, and where it
// is fragile.
func init() {
	register("V-TOPO-001", Error, "the loader rules T1 to T11 all pass", loaderRules)
	register("V-TOPO-002", Error, "the healthy links connect every node to a hub", connectedToAHub)
	register("V-TOPO-003", Warning, "a spoke has only one path to the rest of the network", singleUplink)
	register("V-TOPO-004", Warning, "a direct link joins two nodes behind NAT", natToNat)
	register("V-TOPO-020", Warning, "there is more than one hub", moreThanOneHub)
}

// hubs returns the IDs of the topology's hubs, sorted.
func hubs(t *topology.Topology) []types.NodeID {
	var out []types.NodeID
	for _, n := range t.Nodes {
		if n.Role == types.RoleHub {
			out = append(out, n.ID)
		}
	}
	slices.Sort(out)
	return out
}

// loaderRules runs the topology's own checks (T1 to T11) again on the final plan and
// reports every error. Its warnings are not repeated: V-TOPO-004 and the others cover
// what matters.
func loaderRules(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	var out []Finding
	for _, f := range p.Topology.Check().Errors() {
		out = append(out, Finding{Subject: f.Subject, Message: fmt.Sprintf("%s: %s", f.Rule, f.Msg), Hint: "fix the topology file"})
	}
	return out
}

// connectedToAHub walks the graph from every hub over the links that are up and reports
// each node it does not reach. The walk goes on from a hub and not from a spoke, because
// a spoke never forwards for others: a spoke whose only way to a hub is through another
// spoke is as cut off as one with no way at all. A plan with no graph or no hub has
// nothing to walk.
func connectedToAHub(p *Plan) []Finding {
	if p.Graph == nil || p.Topology == nil {
		return nil
	}
	start := hubs(p.Topology)
	if len(start) == 0 {
		return nil
	}
	reached := map[types.NodeID]bool{}
	queue := slices.Clone(start)
	for _, h := range start {
		reached[h] = true
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, e := range p.Graph.Edges(node) {
			if !e.Down && !reached[e.To] {
				reached[e.To] = true
				if p.Graph.Role(e.To) == types.RoleHub {
					queue = append(queue, e.To)
				}
			}
		}
	}
	var out []Finding
	for _, id := range p.Graph.Nodes() {
		if !reached[id] {
			out = append(out, Finding{Subject: string(id), Message: "no chain of healthy links joins it to a hub",
				Hint: "bring a link up, or add a link that reaches a hub"})
		}
	}
	return out
}

// singleUplink warns about a spoke that has exactly one link, whose loss cuts it off.
func singleUplink(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	degree := map[types.NodeID]int{}
	for _, l := range p.Topology.Links {
		degree[l.A]++
		degree[l.B]++
	}
	var out []Finding
	for _, n := range p.Topology.Nodes {
		if n.Role == types.RoleSpoke && degree[n.ID] == 1 {
			out = append(out, Finding{Subject: string(n.ID), Message: "it has only one link, so losing it cuts the node off",
				Hint: "add a second link, or suppress this warning with the reason"})
		}
	}
	return out
}

// natToNat warns about a direct link between two nodes that cannot be dialled from
// outside: neither can start the connection, so the two must open the path together,
// and if they cannot the hub is the only way between them. A node that never said is
// counted as behind NAT.
func natToNat(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	nat := map[types.NodeID]bool{}
	for _, n := range p.Topology.Nodes {
		nat[n.ID] = n.NAT != topology.NATNone
	}
	var out []Finding
	for _, l := range p.Topology.Links {
		if l.Kind == types.LinkDirect && nat[l.A] && nat[l.B] {
			out = append(out, Finding{Subject: string(types.LinkIDOf(l.A, l.B)),
				Message: fmt.Sprintf("%s and %s are both behind NAT: the link needs hole punching, and the hub is the only fallback", l.A, l.B),
				Hint:    "expect traffic over the hub if the two cannot reach each other directly"})
		}
	}
	return out
}

// moreThanOneHub warns when there are several hubs, since routing is not tuned for it
// before Phase 5.
func moreThanOneHub(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	if h := hubs(p.Topology); len(h) > 1 {
		names := make([]string, len(h))
		for i, n := range h {
			names[i] = string(n)
		}
		return []Finding{{Subject: "hubs", Message: fmt.Sprintf("there are %d hubs (%s); routing is not tuned for several", len(h), strings.Join(names, ", ")),
			Hint: "multi-hub is not optimised until Phase 5"}}
	}
	return nil
}
