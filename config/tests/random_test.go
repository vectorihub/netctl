package tests

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"testing"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// plan is a random network, ready to generate configs for.
type plan struct {
	// in is the generator's input.
	in config.Inputs
	// configs are the generated configs.
	configs []*config.NodeConfig
}

// randomInputs builds a random network from a seed: one or two hubs, two to seven
// spokes behind NAT or not, a hub-spoke link from each spoke to each hub, random
// direct links between spokes with a transit /30 each, and a random set of links
// marked down before the routes are computed. The generator is seeded, so a failure
// can be replayed by its number.
func randomInputs(t testing.TB, seed int) config.Inputs {
	t.Helper()
	rng := rand.New(rand.NewPCG(58, uint64(seed)))
	topo := &topology.Topology{Schema: topology.Schema, Name: fmt.Sprintf("random-%d", seed)}
	owned, local := route.Owned{}, route.Owned{}
	in := config.Inputs{Topology: topo, Overlay: map[types.NodeID]netip.Prefix{}, Sites: map[types.SiteID]netip.Prefix{}, Transit: map[types.LinkID]netip.Prefix{}}

	hubs, spokes := 1+rng.IntN(2), 2+rng.IntN(6)
	var hubIDs, spokeIDs []types.NodeID
	for i := 0; i < hubs+spokes; i++ {
		id := types.NodeID(fmt.Sprintf("n%d", i))
		site := types.SiteID(fmt.Sprintf("s%d", i))
		node := topology.Node{ID: id, Site: site, Advertises: []topology.Advertisement{{Site: true}}}
		if i < hubs {
			node.Role, node.NAT = types.RoleHub, topology.NATNone
			node.Endpoints = []netip.AddrPort{netip.AddrPortFrom(netip.AddrFrom4([4]byte{203, 0, 113, byte(10 + i)}), 51820)}
			hubIDs = append(hubIDs, id)
		} else {
			node.Role = types.RoleSpoke
			node.NAT = []topology.NATMode{topology.NATBehind, topology.NATNone, topology.NATUnknown}[rng.IntN(3)]
			if node.NAT == topology.NATNone {
				node.Endpoints = []netip.AddrPort{netip.AddrPortFrom(netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}), 51820)}
			}
			spokeIDs = append(spokeIDs, id)
		}
		topo.Sites = append(topo.Sites, topology.Site{ID: site})
		topo.Nodes = append(topo.Nodes, node)
		overlay := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 42, 240, byte(i + 1)}), 32)
		block := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 1, byte(i), 0}), 24)
		in.Overlay[id], in.Sites[site] = overlay, block
		owned[id] = []netip.Prefix{block, overlay}
	}
	down := map[types.LinkID]bool{}
	for _, s := range spokeIDs {
		for _, h := range hubIDs {
			topo.Links = append(topo.Links, topology.Link{Kind: types.LinkHubSpoke, A: h, B: s, Cost: 20})
			if rng.IntN(5) == 0 {
				down[types.LinkIDOf(h, s)] = true
			}
		}
	}
	transit := 0
	for i, a := range spokeIDs {
		for _, b := range spokeIDs[i+1:] {
			if rng.IntN(3) != 0 {
				continue
			}
			id := types.LinkIDOf(a, b)
			topo.Links = append(topo.Links, topology.Link{Kind: types.LinkDirect, A: a, B: b, Cost: 10, Health: topology.HealthRequired})
			block := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 42, 252, byte(4 * transit)}), 30)
			transit++
			in.Transit[id] = block
			local[a], local[b] = append(local[a], block), append(local[b], block)
			if rng.IntN(3) == 0 {
				down[id] = true
			}
		}
	}
	topo.Normalize()
	graph := fixture.FromTopology(topology.BuildGraph(topo, func(id types.LinkID) bool { return !down[id] }))
	computed, err := route.Compute(graph, route.Input{Owned: owned, Local: local})
	if err != nil {
		t.Fatalf("seed %d: Compute: %v", seed, err)
	}
	in.Tables = computed.Tables
	return in
}

// planCache keeps the plans already built, by seed: building 400 networks is the
// slow part of the property tests, and they only read what they are given.
var planCache = map[int]plan{}

// randomPlan builds a random network and generates its configs. The result is
// shared between tests, so callers must not change it.
func randomPlan(t testing.TB, seed int) plan {
	t.Helper()
	if p, ok := planCache[seed]; ok {
		return p
	}
	in := randomInputs(t, seed)
	configs, err := config.Generate(in)
	if err != nil {
		t.Fatalf("seed %d: Generate: %v", seed, err)
	}
	planCache[seed] = plan{in: in, configs: configs}
	return planCache[seed]
}
