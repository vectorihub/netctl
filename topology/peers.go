package topology

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// ImportPeers turns each node's Peers list into links, then clears the lists.
// It closes the one-sided-link hole at the boundary (AC6): a link is one object
// with two ends, but some sources (a Packet Tracer export, an older format) say
// "I am connected to X" from each side separately, and the two sides can
// disagree.
//
// A pair becomes one link only when both nodes list each other. The kind comes
// from the roles: hub to spoke is hub-spoke, spoke to spoke is direct. Cost and
// health are left unset, so Normalize gives the defaults. The new links are
// appended in sorted order; call Normalize afterwards (Decode does).
//
// Every problem is returned together as a types.List of INVALID_TOPOLOGY errors
// whose Subject is the link ID ("builder-1~dgx-spark"), and nothing is changed
// when there is any. The problems are:
//   - one-sided link: A lists B, B does not list A;
//   - a peer that is not a node, a node that lists itself, or a peer listed twice;
//   - hub to hub, which has no link kind in v1alpha1;
//   - a node with no role, since the kind cannot be derived;
//   - peers given together with links: use one form or the other, so that no
//     pair can be described twice.
//
// A Topology with no Peers is left alone and returns nil.
//
// Example: builder-1 lists dgx-spark but dgx-spark does not list builder-1 gives
// "INVALID_TOPOLOGY: builder-1~dgx-spark: one-sided link: builder-1 lists
// dgx-spark as a peer, dgx-spark does not list builder-1".
func (t *Topology) ImportPeers() error {
	if t == nil {
		return nil
	}
	anyPeers := slices.ContainsFunc(t.Nodes, func(node Node) bool { return len(node.Peers) > 0 })
	if !anyPeers {
		return nil
	}
	var problems types.List
	if len(t.Links) > 0 {
		problems.Add(types.Errf(types.ErrInvalidTopology, "links", "nodes list peers and the topology also has links; use one form, so no pair is described twice"))
	}

	byID := make(map[types.NodeID]Node, len(t.Nodes))
	for _, node := range t.Nodes {
		byID[node.ID] = node
	}
	type pair = [2]types.NodeID
	listed := map[pair]bool{}
	for _, node := range t.Nodes {
		for _, peer := range node.Peers {
			subject := string(types.LinkIDOf(node.ID, peer))
			switch {
			case peer == node.ID:
				problems.Add(types.Errf(types.ErrInvalidTopology, subject, "%s lists itself as a peer", node.ID))
			case !hasNode(byID, peer):
				problems.Add(types.Errf(types.ErrInvalidTopology, subject, "%s lists %s as a peer, but there is no such node", node.ID, peer))
			case listed[pair{node.ID, peer}]:
				problems.Add(types.Errf(types.ErrInvalidTopology, subject, "%s lists %s as a peer more than once", node.ID, peer))
			default:
				listed[pair{node.ID, peer}] = true
			}
		}
	}

	var links []Link
	for _, from := range sortedPairs(listed) {
		a, b := from[0], from[1]
		subject := string(types.LinkIDOf(a, b))
		if !listed[pair{b, a}] {
			problems.Add(types.Errf(types.ErrInvalidTopology, subject, "one-sided link: %s lists %s as a peer, %s does not list %s", a, b, b, a))
			continue
		}
		if b < a { // a mutual pair becomes one link, made from its smaller end
			continue
		}
		kind, why := peerKind(byID[a], byID[b])
		if why != "" {
			problems.Add(types.Errf(types.ErrInvalidTopology, subject, "%s", why))
			continue
		}
		links = append(links, Link{Kind: kind, A: a, B: b})
	}
	if err := problems.Err(); err != nil {
		return err
	}

	t.Links = append(t.Links, links...)
	for i := range t.Nodes {
		t.Nodes[i].Peers = nil
	}
	return nil
}

// hasNode reports whether id is a key of nodes. It exists to read clearly where
// the node itself is not needed.
func hasNode(nodes map[types.NodeID]Node, id types.NodeID) bool {
	_, ok := nodes[id]
	return ok
}

// sortedPairs returns the listed pairs in byte order, so problems and links come
// out the same way whatever order the nodes were written in.
func sortedPairs(listed map[[2]types.NodeID]bool) [][2]types.NodeID {
	pairs := make([][2]types.NodeID, 0, len(listed))
	for pair := range listed {
		pairs = append(pairs, pair)
	}
	slices.SortFunc(pairs, func(x, y [2]types.NodeID) int {
		if x[0] != y[0] {
			return cmp.Compare(x[0], y[0])
		}
		return cmp.Compare(x[1], y[1])
	})
	return pairs
}

// peerKind derives a link's kind from its two ends' roles, or says why it cannot.
func peerKind(a, b Node) (types.LinkKind, string) {
	for _, node := range []Node{a, b} {
		if node.Role != types.RoleHub && node.Role != types.RoleSpoke {
			return types.LinkUnknown, fmt.Sprintf("cannot tell the link kind: %s has no role", node.ID)
		}
	}
	switch {
	case a.Role == types.RoleHub && b.Role == types.RoleHub:
		return types.LinkUnknown, fmt.Sprintf("%s and %s are both hubs; v1alpha1 has no hub-to-hub link", a.ID, b.ID)
	case a.Role == types.RoleSpoke && b.Role == types.RoleSpoke:
		return types.LinkDirect, ""
	}
	return types.LinkHubSpoke, ""
}
