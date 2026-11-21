package validate

import (
	"fmt"
	"slices"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// The link rules: whether the two ends of each link, in their configs, tell the same
// story.
func init() {
	register("V-LINK-001", Error, "every peer has a matching peer on the other node", oneSidedPeers)
	register("V-LINK-002", Error, "both ends of a link agree on its kind and transit addresses", endsAgree)
	register("V-LINK-003", Error, "a direct link whose health is required is probed at both ends", probedAtBothEnds)
	register("V-LINK-004", Warning, "a direct link costs more than the hub path", directCostsMore)
}

// oneSidedPeers checks that if a node lists a peer, that peer's config lists the node
// back, on the same link. A peer whose own config is missing counts as not listing it.
func oneSidedPeers(p *Plan) []Finding {
	var out []Finding
	for _, c := range p.Configs {
		if c == nil {
			continue
		}
		for _, peer := range c.Peers {
			back, ok := p.peerOf(peer.Node, c.Node)
			switch {
			case p.configOf(peer.Node) == nil:
				out = append(out, Finding{Subject: string(peer.Link), Message: fmt.Sprintf("%s lists %s as a peer, but %s has no config", c.Node, peer.Node, peer.Node),
					Hint: "every node of the plan needs a config"})
			case !ok:
				out = append(out, Finding{Subject: string(peer.Link), Message: fmt.Sprintf("%s lists %s as a peer, but %s does not list %s", c.Node, peer.Node, peer.Node, c.Node),
					Hint: "a link has two ends; regenerate the configs"})
			case back.Link != peer.Link:
				out = append(out, Finding{Subject: string(peer.Link), Message: fmt.Sprintf("%s calls its link to %s %s, and %s calls it %s", c.Node, peer.Node, peer.Link, peer.Node, back.Link),
					Hint: "regenerate the configs"})
			}
		}
	}
	return out
}

// linkEnds returns the topology's links, keyed by ID.
func linkEnds(t *topology.Topology) map[types.LinkID]topology.Link {
	out := map[types.LinkID]topology.Link{}
	if t != nil {
		for _, l := range t.Links {
			out[types.LinkIDOf(l.A, l.B)] = l
		}
	}
	return out
}

// endsAgree checks, for each link both of whose ends have a config, that the two agree
// on what kind of link it is (and with the topology), and that they use the same
// transit addresses the other way round: each end's local address is the other's remote.
func endsAgree(p *Plan) []Finding {
	links := linkEnds(p.Topology)
	var out []Finding
	seen := map[types.LinkID]bool{}
	for _, c := range p.Configs {
		if c == nil {
			continue
		}
		for _, a := range c.Peers {
			if seen[a.Link] {
				continue
			}
			b, ok := p.peerOf(a.Node, c.Node)
			if !ok {
				continue // a one-sided link is V-LINK-001's
			}
			seen[a.Link] = true
			if (a.Role == config.PeerDirect) != (b.Role == config.PeerDirect) {
				out = append(out, Finding{Subject: string(a.Link), Message: fmt.Sprintf("%s sees the link to %s as %s and %s sees it as %s", c.Node, a.Node, a.Role, a.Node, b.Role),
					Hint: "both ends must agree on the kind of link"})
			}
			if l, known := links[a.Link]; known && (l.Kind == types.LinkDirect) != (a.Role == config.PeerDirect) {
				out = append(out, Finding{Subject: string(a.Link), Message: fmt.Sprintf("the topology says the link is %s, but %s's config says %s", l.Kind, c.Node, a.Role)})
			}
			if (a.Probe == nil) != (b.Probe == nil) {
				continue // a missing probe is V-LINK-003's
			}
			if a.Probe != nil && (a.Probe.Remote != b.Probe.Local.Addr() || b.Probe.Remote != a.Probe.Local.Addr()) {
				out = append(out, Finding{Subject: string(a.Link),
					Message: fmt.Sprintf("%s probes %s from %s to %s, and %s probes from %s to %s: the addresses do not mirror", c.Node, a.Node, a.Probe.Local.Addr(), a.Probe.Remote, a.Node, b.Probe.Local.Addr(), b.Probe.Remote),
					Hint:    "both ends must use the two hosts of the link's transit /30, each other's way round"})
			}
		}
	}
	return out
}

// probedAtBothEnds checks every direct link whose health is required has, at each end,
// a probe on the peer and a matching target in the health section. A node whose config
// is missing is not repeated here.
func probedAtBothEnds(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	var out []Finding
	for _, l := range p.Topology.Links {
		if !probed(l) {
			continue
		}
		id := types.LinkIDOf(l.A, l.B)
		for _, end := range []types.NodeID{l.A, l.B} {
			other := l.A
			if end == l.A {
				other = l.B
			}
			c := p.configOf(end)
			if c == nil {
				continue
			}
			peer, ok := p.peerOf(end, other)
			switch {
			case !ok || peer.Probe == nil:
				out = append(out, Finding{Subject: string(id), Message: fmt.Sprintf("the link's health is required, but %s's config has no probe for it", end),
					Hint: "give the link a transit /30 so both ends can probe it"})
			case !slices.ContainsFunc(c.Health.Probes, func(t config.ProbeTarget) bool { return t.Link == id && t.Target == peer.Probe.Remote }):
				out = append(out, Finding{Subject: string(id), Message: fmt.Sprintf("%s's config has a probe address for the link but nothing to probe it with in its health section", end),
					Hint: "regenerate the configs"})
			}
		}
	}
	return out
}

// directCostsMore warns about a direct link that costs more than going round by a hub,
// since the routes would never use it: the cheapest way over the two hub-spoke links of
// a hub that joins both ends.
func directCostsMore(p *Plan) []Finding {
	if p.Topology == nil {
		return nil
	}
	cost := func(l topology.Link) int {
		if l.Cost > 0 {
			return l.Cost
		}
		if l.Kind == types.LinkDirect {
			return topology.DefaultDirectCost
		}
		return topology.DefaultHubSpokeCost
	}
	hubLink := map[[2]types.NodeID]int{}
	for _, l := range p.Topology.Links {
		if l.Kind == types.LinkHubSpoke {
			hubLink[[2]types.NodeID{l.A, l.B}], hubLink[[2]types.NodeID{l.B, l.A}] = cost(l), cost(l)
		}
	}
	var out []Finding
	for _, l := range p.Topology.Links {
		if l.Kind != types.LinkDirect {
			continue
		}
		best := -1
		for _, h := range hubs(p.Topology) {
			a, okA := hubLink[[2]types.NodeID{h, l.A}]
			b, okB := hubLink[[2]types.NodeID{h, l.B}]
			if okA && okB && (best < 0 || a+b < best) {
				best = a + b
			}
		}
		if best >= 0 && cost(l) > best {
			out = append(out, Finding{Subject: string(types.LinkIDOf(l.A, l.B)),
				Message: fmt.Sprintf("the direct link costs %d, more than the %d of the path by way of a hub, so no route would use it", cost(l), best),
				Hint:    "lower its cost, or remove the link"})
		}
	}
	return out
}
