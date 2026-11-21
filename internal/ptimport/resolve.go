package ptimport

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/vectorihub/netctl/ipam"
)

// Resolver turns addresses into the nodes that own them, using the plan's own allocations:
// a node's overlay /32, and the two hosts of a link's transit /30 (the lower address to the
// lexically smaller node ID). Addresses the plan does not allocate come from the lab's
// mapping.
//
// Example:
//
//	node, ok := resolver.Node("10.42.0.2")
type Resolver struct {
	snapshot ipam.Snapshot
	mapping  Mapping
}

// NewResolver builds a resolver from a saved address plan and the lab's mapping.
//
// Example:
//
//	r := ptimport.NewResolver(result.IPAM, obs.Mapping)
func NewResolver(snapshot ipam.Snapshot, mapping Mapping) Resolver {
	return Resolver{snapshot: snapshot, mapping: mapping}
}

// Node returns the node that owns the address.
//
// Example:
//
//	node, ok := resolver.Node("10.42.0.2") // "aws-hub", true
func (r Resolver) Node(address string) (string, bool) {
	if node, ok := r.mapping.Hops[address]; ok {
		return node, true
	}
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return "", false
	}
	for _, a := range r.snapshot.Allocations {
		if !a.Prefix.Contains(addr) {
			continue
		}
		switch a.Owner.Kind {
		case ipam.KindNode:
			if a.Prefix.Bits() == 32 {
				return a.Owner.ID, true
			}
		case ipam.KindLink:
			ends := strings.Split(a.Owner.ID, "~")
			if len(ends) != 2 {
				continue
			}
			base := a.Prefix.Addr()
			switch addr {
			case base.Next():
				return ends[0], true
			case base.Next().Next():
				return ends[1], true
			}
		}
	}
	return "", false
}

// SiteNode returns the node whose site holds the address, for a host address such as
// 10.42.32.10. It reports the node by the site owner's ID through the mapping of site to
// node: the first node of the site that appears in the nodes map.
//
// Example:
//
//	node, ok := resolver.SiteNode("10.42.32.10", siteNodes)
func (r Resolver) SiteNode(address string, siteNodes map[string]string) (string, bool) {
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return "", false
	}
	for _, a := range r.snapshot.Allocations {
		if a.Owner.Kind == ipam.KindSite && a.Prefix.Contains(addr) {
			node, ok := siteNodes[a.Owner.ID]
			return node, ok
		}
	}
	return "", false
}

// Describe names an address for a failure message: the node that owns it, or "unmapped".
//
// Example:
//
//	label := resolver.Describe("10.42.0.2") // "aws-hub", or "unmapped"
func (r Resolver) Describe(address string) string {
	if node, ok := r.Node(address); ok {
		return fmt.Sprintf("%s (%s)", address, node)
	}
	return address + " (unmapped)"
}
