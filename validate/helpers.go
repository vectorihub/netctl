package validate

import (
	"slices"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/types"
)

// configOf returns the plan's config for a node, or nil if there is none.
func (p *Plan) configOf(node types.NodeID) *config.NodeConfig {
	for _, c := range p.Configs {
		if c != nil && c.Node == node {
			return c
		}
	}
	return nil
}

// peerOf returns the peer of node's config that is the other node, and whether there
// is one.
func (p *Plan) peerOf(node, other types.NodeID) (config.Peer, bool) {
	c := p.configOf(node)
	if c == nil {
		return config.Peer{}, false
	}
	i := slices.IndexFunc(c.Peers, func(peer config.Peer) bool { return peer.Node == other })
	if i < 0 {
		return config.Peer{}, false
	}
	return c.Peers[i], true
}

// probeOf returns the probe node's config gives for its peer other, and whether the
// config, the peer and the probe all exist.
func probeOf(p *Plan, node, other types.NodeID) (config.Probe, bool) {
	peer, ok := p.peerOf(node, other)
	if !ok || peer.Probe == nil {
		return config.Probe{}, false
	}
	return *peer.Probe, true
}
