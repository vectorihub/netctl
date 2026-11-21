package topogen

import (
	"fmt"
	"math/rand/v2"
	"net/netip"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// Spec says what network to generate.
type Spec struct {
	// Nodes is the total number of nodes, hubs included.
	Nodes int
	// Hubs is how many of them are hubs (at least 1).
	Hubs int
	// Direct is how many direct links between spokes to add, health required.
	Direct int
	// NATRatio is the share of spokes behind NAT, from 0 to 1.
	NATRatio float64
	// Seed makes the choice of links and NAT reproducible.
	Seed uint64
}

// addressing is the address plan every generated topology carries.
func addressing() topology.Addressing {
	p := netip.MustParsePrefix
	return topology.Addressing{Supernet: p("10.0.0.0/8"), Pools: []topology.AddressPool{
		{Name: "sites", Prefix: p("10.0.0.0/12"), ChildBits: 20},
		{Name: "growth", Prefix: p("10.16.0.0/12"), ChildBits: 20, Reserved: true},
		{Name: "overlay", Prefix: p("10.240.0.0/16"), ChildBits: 32, SkipFirst: 1},
		{Name: "transit", Prefix: p("10.252.0.0/16"), ChildBits: 30},
	}}
}

// Generate builds a topology from a spec: hubs with public endpoints, spokes spread sixteen to
// a site, every spoke linked to the first hub (and to the others when there are several), and
// Direct random direct links between distinct spokes. It is normalised and ready to plan.
//
// Example:
//
//	topo := topogen.Generate(topogen.Spec{Nodes: 100, Hubs: 1, Direct: 30, NATRatio: 0.6, Seed: 1})
func Generate(s Spec) *topology.Topology {
	rng := rand.New(rand.NewPCG(s.Seed, 0x746f706f))
	if s.Hubs < 1 {
		s.Hubs = 1
	}
	if s.Nodes < s.Hubs {
		s.Nodes = s.Hubs
	}
	t := &topology.Topology{Schema: topology.Schema, Name: fmt.Sprintf("gen-%d", s.Seed), Addressing: addressing()}
	siteOf := func(i int) types.SiteID { return types.SiteID(fmt.Sprintf("site-%03d", i/16)) }
	seen := map[types.SiteID]bool{}
	var hubs, spokes []types.NodeID
	for i := 0; i < s.Nodes; i++ {
		site := siteOf(i)
		n := topology.Node{ID: types.NodeID(fmt.Sprintf("n%03d", i)), Site: site}
		if !seen[site] {
			seen[site] = true
			t.Sites = append(t.Sites, topology.Site{ID: site, Provider: "owned"})
			n.Advertises = []topology.Advertisement{{Site: true}}
		}
		if i < s.Hubs {
			n.Role, n.NAT = types.RoleHub, topology.NATNone
			n.Endpoints = []netip.AddrPort{netip.AddrPortFrom(netip.AddrFrom4([4]byte{203, 0, 113, byte(10 + i)}), 51820)}
			hubs = append(hubs, n.ID)
		} else {
			n.Role, n.NAT = types.RoleSpoke, topology.NATBehind
			if rng.Float64() >= s.NATRatio {
				n.NAT = topology.NATNone
				n.Endpoints = []netip.AddrPort{netip.AddrPortFrom(netip.AddrFrom4([4]byte{198, 51, byte(i / 250), byte(i%250 + 1)}), 51820)}
			}
			spokes = append(spokes, n.ID)
		}
		t.Nodes = append(t.Nodes, n)
	}
	for _, sp := range spokes {
		for _, h := range hubs {
			t.Links = append(t.Links, topology.Link{Kind: types.LinkHubSpoke, A: h, B: sp})
		}
	}
	have := map[[2]types.NodeID]bool{}
	for tries := 0; len(spokes) > 1 && s.Direct > 0 && tries < s.Direct*20; tries++ {
		a, b := spokes[rng.IntN(len(spokes))], spokes[rng.IntN(len(spokes))]
		if a == b {
			continue
		}
		if b < a {
			a, b = b, a
		}
		if have[[2]types.NodeID{a, b}] {
			continue
		}
		have[[2]types.NodeID{a, b}] = true
		t.Links = append(t.Links, topology.Link{Kind: types.LinkDirect, A: a, B: b, Cost: 10, Health: topology.HealthRequired})
		if len(have) == s.Direct {
			break
		}
	}
	t.Normalize()
	return t
}

// DirectLinks returns the IDs of the direct links of a topology, sorted.
func DirectLinks(t *topology.Topology) []string {
	var ids []string
	for _, l := range t.Links {
		if l.Kind == types.LinkDirect {
			ids = append(ids, string(types.LinkIDOf(l.A, l.B)))
		}
	}
	return ids
}
