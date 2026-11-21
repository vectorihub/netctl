package validate

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/internal/jsonschema"
	"github.com/vectorihub/netctl/types"
)

// The configuration rules: whether the generated documents are well formed and say what
// the routes say, in the terms WireGuard needs.
func init() {
	register("V-CFG-001", Error, "every config is well formed: it has its hash and validates against the schema", wellFormed)
	register("V-CFG-002", Error, "every active route's next hop has a peer entry", nextHopHasAPeer)
	register("V-CFG-003", Error, "every active route's prefix is in its next hop peer's allowed_prefixes", prefixIsAllowed)
	register("V-CFG-004", Error, "no prefix is in two peers' allowed_prefixes on one node", prefixInTwoPeers)
	register("V-CFG-005", Error, "no secret material in any config", noSecrets)
	register("V-CFG-006", Error, "an allowed prefix has a route back to the node", returnPath)
}

// masked returns the block a prefix stands for (cidr.Canonical), the form every comparison
// and map key in these rules uses.
func masked(prefix netip.Prefix) netip.Prefix {
	out, _ := cidr.Canonical(prefix)
	return out
}

// wellFormed checks every node of the topology has a config, that the hash in each is the
// hash of its content, and that it validates against the published JSON Schema, reporting
// each problem the schema finds.
func wellFormed(p *Plan) []Finding {
	schema, err := jsonschema.New(config.SchemaJSON())
	if err != nil {
		return []Finding{{Subject: "schema", Message: "the schema does not load: " + err.Error()}}
	}
	var out []Finding
	if p.Topology != nil {
		for _, n := range p.Topology.Nodes {
			if p.configOf(n.ID) == nil {
				out = append(out, Finding{Subject: string(n.ID), Message: "it has no config", Hint: "every node of the plan needs one"})
			}
		}
	}
	for _, c := range p.Configs {
		if c == nil {
			continue
		}
		if err := c.Verify(); err != nil {
			out = append(out, Finding{Subject: string(c.Node), Message: "its hash does not match its content", Hint: "the config was changed after it was hashed; encode it again"})
		}
		data, err := c.Clone().Encode()
		if err != nil {
			out = append(out, Finding{Subject: string(c.Node), Message: "it cannot be written: " + err.Error()})
			continue
		}
		problems, err := schema.Validate(data)
		if err != nil {
			out = append(out, Finding{Subject: string(c.Node), Message: "it is not JSON: " + err.Error()})
		}
		for _, problem := range problems {
			out = append(out, Finding{Subject: string(c.Node), Message: "the schema refuses it: " + problem, Hint: "see config/schema/v1alpha1.json"})
		}
	}
	return out
}

// nextHopHasAPeer checks the next hop of every active route in a config is one of that
// config's peers.
func nextHopHasAPeer(p *Plan) []Finding {
	var out []Finding
	for _, c := range p.Configs {
		if c == nil {
			continue
		}
		for _, r := range c.Routes {
			if r.Active && !slices.ContainsFunc(c.Peers, func(peer config.Peer) bool { return peer.Node == r.Via }) {
				out = append(out, Finding{Subject: string(c.Node), Message: fmt.Sprintf("the active route for %s goes via %s, which is not one of its peers", r.Prefix, r.Via),
					Hint: "WireGuard can only send to a peer; add the link or change the route"})
			}
		}
	}
	return out
}

// prefixIsAllowed checks the prefix of every active route is in the allowed prefixes of
// the peer it goes through, since otherwise WireGuard drops the traffic it sends and the
// replies that come back.
func prefixIsAllowed(p *Plan) []Finding {
	var out []Finding
	for _, c := range p.Configs {
		if c == nil {
			continue
		}
		for _, r := range c.Routes {
			if !r.Active {
				continue
			}
			i := slices.IndexFunc(c.Peers, func(peer config.Peer) bool { return peer.Node == r.Via })
			if i >= 0 && !slices.ContainsFunc(c.Peers[i].AllowedPrefixes, func(a netip.Prefix) bool { return masked(a) == masked(r.Prefix) }) {
				out = append(out, Finding{Subject: string(c.Node), Message: fmt.Sprintf("the active route for %s goes via %s, but %s is not in that peer's allowed_prefixes", r.Prefix, r.Via, r.Prefix),
					Hint: "WireGuard would drop the traffic; check rule G3"})
			}
		}
	}
	return out
}

// prefixInTwoPeers reports a prefix in the allowed list of two peers of one node, which
// WireGuard settles silently by keeping only the last.
func prefixInTwoPeers(p *Plan) []Finding {
	var out []Finding
	for _, c := range p.Configs {
		if c == nil {
			continue
		}
		seen := map[netip.Prefix]types.NodeID{}
		for _, peer := range c.Peers {
			for _, prefix := range peer.AllowedPrefixes {
				prefix = masked(prefix) // 10.42.32.5/20 and 10.42.32.0/20 are one block
				if other, dup := seen[prefix]; dup && other != peer.Node {
					out = append(out, Finding{Subject: string(c.Node), Message: fmt.Sprintf("%s is allowed for both %s and %s", prefix, other, peer.Node),
						Hint: "a prefix must belong to exactly one peer; check rule G3"})
				}
				seen[prefix] = peer.Node
			}
		}
	}
	return out
}

// noSecrets scans every config as it would be written for anything shaped like key
// material or a token.
func noSecrets(p *Plan) []Finding {
	var out []Finding
	for _, c := range p.Configs {
		if c == nil {
			continue
		}
		data, err := c.Clone().Encode()
		if err != nil {
			continue // wellFormed reports a config that cannot be written
		}
		for _, f := range config.ScanSecrets(data) {
			out = append(out, Finding{Subject: string(c.Node), Message: fmt.Sprintf("%s: %s: %s", f.Path, f.Rule, f.Why), Hint: "private keys are made on the node and never leave it"})
		}
	}
	return out
}

// returnPath checks that if A allows a prefix for peer B, then B has an active route to
// A's overlay address, so what A sends to B can be answered. Otherwise the packet goes
// out and the reply is lost.
func returnPath(p *Plan) []Finding {
	var out []Finding
	for _, a := range p.Configs {
		if a == nil || !a.Overlay.Address.IsValid() {
			continue
		}
		back := a.Overlay.Address.Addr()
		for _, peer := range a.Peers {
			if len(peer.AllowedPrefixes) == 0 {
				continue
			}
			b := p.configOf(peer.Node)
			if b == nil {
				continue // a missing config is V-CFG-001's
			}
			if !slices.ContainsFunc(b.Routes, func(r config.Route) bool { return r.Active && r.Prefix.Contains(back) }) {
				out = append(out, Finding{Subject: string(a.Node), Message: fmt.Sprintf("%s allows %s for %s, but %s has no active route back to %s (%s)", a.Node, peer.AllowedPrefixes[0], peer.Node, peer.Node, a.Node, back),
					Hint: "replies would be lost; give the peer a route to the node's overlay address"})
			}
		}
	}
	return out
}
