package route

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// Announcer says what one node announces to the overlay. The planner builds
// one from each node of the topology.
type Announcer struct {
	// Node is the machine.
	Node types.NodeID
	// Site is the site the machine lives in, whose address block the machine
	// announces when AdvertiseSite is set.
	Site types.SiteID
	// AdvertiseSite means the node announces its site's whole block (the
	// topology's "advertises: [site]").
	AdvertiseSite bool
	// Prefixes are extra blocks the node announces (the topology's
	// "advertises: [10.9.0.0/24]").
	Prefixes []netip.Prefix
}

// Collect gathers the destinations of a network from its address plan: who
// holds which prefix.
//
// For every announcer it gathers:
//   - the site block, if AdvertiseSite is set, from the allocations the
//     registry holds for the site;
//   - the overlay address, from the allocations the registry holds for the
//     node itself (normally one /32);
//   - the extra Prefixes it lists.
//
// For every link in links it finds the transit block the registry holds for
// the link and makes it local to both ends: each end gets a connected route
// to it, and it is never advertised further. A link with no transit block has
// nothing to connect and is skipped.
//
// Only allocated blocks count. A block that is merely reserved, or in
// quarantine after a release, is not a destination.
//
// The result feeds Input.Owned and Input.Local. Each list is sorted and has no
// repeats. Collect returns an error, and no lists, if the registry is nil, if
// a node announces its site but the registry holds no block for that site
// (NOT_FOUND), if a prefix is not an IPv4 prefix (INVALID_PREFIX), or if a
// link ID is not two node IDs joined by '~' (INVALID_TOPOLOGY). Every problem
// is reported at once.
//
// Example: with site aws holding 10.42.0.0/20, node aws-hub holding
// 10.42.240.1/32, and a direct link builder-1~dgx-spark holding 10.42.252.0/30,
// Collect gives aws-hub the owned prefixes 10.42.0.0/20 and 10.42.240.1/32 (if
// it advertises its site), and gives both builder-1 and dgx-spark the local
// prefix 10.42.252.0/30.
func Collect(registry *ipam.IPAM, announcers []Announcer, links []types.LinkID) (owned, local Owned, err error) {
	if registry == nil {
		return nil, nil, types.Errf(types.ErrInvalidValue, "Collect", "it needs an IPAM registry, got nil")
	}
	var problems types.List
	owned, local = Owned{}, Owned{}
	for _, announcer := range announcers {
		if announcer.AdvertiseSite {
			blocks := allocated(registry, ipam.Site(string(announcer.Site)))
			if len(blocks) == 0 {
				problems.Add(types.Errf(types.ErrNotFound, "node "+string(announcer.Node),
					"it advertises its site, but the address plan holds no block for site %q", announcer.Site))
			}
			owned[announcer.Node] = append(owned[announcer.Node], blocks...)
		}
		owned[announcer.Node] = append(owned[announcer.Node], allocated(registry, ipam.Node(string(announcer.Node)))...)
		for _, prefix := range announcer.Prefixes {
			if !prefix.IsValid() || !prefix.Addr().Is4() {
				problems.Add(types.Errf(types.ErrInvalidPrefix, "node "+string(announcer.Node), "%s is not a valid IPv4 prefix", prefix))
				continue
			}
			canonical, _ := cidr.Canonical(prefix)
			owned[announcer.Node] = append(owned[announcer.Node], canonical)
		}
	}
	for _, link := range links {
		first, second, found := strings.Cut(string(link), "~")
		if !found || !types.ValidID(first) || !types.ValidID(second) || first == second {
			problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(link), "a link is two different valid node IDs joined by '~'"))
			continue
		}
		for _, block := range allocated(registry, ipam.Link(string(link))) {
			local[types.NodeID(first)] = append(local[types.NodeID(first)], block)
			local[types.NodeID(second)] = append(local[types.NodeID(second)], block)
		}
	}
	if err := problems.Err(); err != nil {
		return nil, nil, err
	}
	tidy(owned)
	tidy(local)
	return owned, local, nil
}

// allocated returns the prefixes the registry holds for owner in the allocated
// state, in address order.
func allocated(registry *ipam.IPAM, owner ipam.Owner) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, held := range registry.ByOwner(owner) {
		if held.State == ipam.Allocated {
			prefixes = append(prefixes, held.Prefix)
		}
	}
	return prefixes
}

// tidy sorts every list in owned and removes repeats, and drops nodes whose
// list is empty.
func tidy(owned Owned) {
	for node, prefixes := range owned {
		slices.SortFunc(prefixes, cidr.Compare)
		prefixes = slices.Compact(prefixes)
		if len(prefixes) == 0 {
			delete(owned, node)
			continue
		}
		owned[node] = prefixes
	}
}
