package topology

import (
	"cmp"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// The default cost of each kind of link, used when a link gives none. A direct
// link is cheaper than the hub path it shortcuts: two hub-spoke hops cost 40
// together, one direct link costs 10.
const (
	// DefaultHubSpokeCost is the cost of a hub-spoke link that gives none.
	DefaultHubSpokeCost = 20
	// DefaultDirectCost is the cost of a direct link that gives none.
	DefaultDirectCost = 10
)

// defaultCost returns the default cost for a link kind, or 0 for an unknown kind.
func defaultCost(kind types.LinkKind) int {
	switch kind {
	case types.LinkHubSpoke:
		return DefaultHubSpokeCost
	case types.LinkDirect:
		return DefaultDirectCost
	}
	return 0
}

// defaultHealth returns how a link's health is known when it does not say: a
// direct link needs its own health signal, because nothing else vouches for it;
// a hub-spoke link is taken to be up, because the hub path is the fallback.
func defaultHealth(kind types.LinkKind) HealthMode {
	switch kind {
	case types.LinkHubSpoke:
		return HealthAssumed
	case types.LinkDirect:
		return HealthRequired
	}
	return HealthUnset
}

// Normalize puts a topology into its one canonical form, so nothing downstream
// has to guess and the same network always looks the same. Decode calls it; call
// it yourself on a Topology built in code.
//
// It does four things:
//   - Fills in what was left out: a link's cost (20 for hub-spoke, 10 for direct)
//     and health (assumed for hub-spoke, required for direct). A node's NAT mode
//     needs no filling: its zero value already is NATUnknown.
//   - Stores every link with A before B and sets its ID from the two ends, so a
//     link is the same whichever end the author wrote first. Any ID that was
//     there is replaced, because the ID is derived, never chosen.
//   - Sorts sites, nodes and links by ID. Links with the same ID (a pair joined
//     twice) are ordered by kind, cost and health, so the order never depends on
//     how the input was arranged.
//   - Leaves intents exactly as they are: their order matters for policy.
//
// Values that were given are never changed, and nothing is checked: a link to a
// node that does not exist, or two nodes with one ID, are rules T2 to T5.
// Normalize is idempotent, sorts the slices in place and does nothing to a nil
// Topology.
//
// Example: a link written as "b: aws-hub, a: builder-1, kind: hub-spoke" becomes
// A "aws-hub", B "builder-1", ID "aws-hub~builder-1", Cost 20, Health assumed.
func (t *Topology) Normalize() {
	if t == nil {
		return
	}
	for i := range t.Links {
		link := &t.Links[i]
		if link.Cost == 0 {
			link.Cost = defaultCost(link.Kind)
		}
		if link.Health == HealthUnset {
			link.Health = defaultHealth(link.Kind)
		}
		if link.B < link.A {
			link.A, link.B = link.B, link.A
		}
		link.ID = types.LinkIDOf(link.A, link.B)
	}

	slices.SortStableFunc(t.Sites, func(a, b Site) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortStableFunc(t.Nodes, func(a, b Node) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortStableFunc(t.Links, func(a, b Link) int {
		return cmp.Or(
			cmp.Compare(a.ID, b.ID),
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Cost, b.Cost),
			cmp.Compare(a.Health, b.Health),
		)
	})
}
