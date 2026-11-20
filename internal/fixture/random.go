package fixture

import (
	"fmt"
	"math/rand/v2"
	"net/netip"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// RandomNetwork builds a random hub-and-spoke network of between minNodes and
// maxNodes nodes (at least 4), the kind of network the routing and simulator
// property tests run on. It has one to three hubs, joined to each other with some
// probability; every spoke joined to one or two hubs; some direct links between
// spokes; costs from 1 to 30; and a few links marked down. Every node advertises
// a /24 and a /32 of its own.
//
// It returns the graph and what each node advertises. The same generator state
// always gives the same network.
func RandomNetwork(rng *rand.Rand, minNodes, maxNodes int) (*Graph, route.Owned) {
	count := minNodes + rng.IntN(maxNodes-minNodes+1)
	hubs := 1 + rng.IntN(3)
	g := NewGraph()
	names := make([]types.NodeID, count)
	for i := range names {
		names[i] = types.NodeID(fmt.Sprintf("n%02d", i))
		if i < hubs {
			g.Hub(names[i])
		} else {
			g.Spoke(names[i])
		}
	}
	join := func(a, b types.NodeID) {
		link := g.Link(a, b, 1+rng.IntN(30))
		if rng.IntN(100) < 5 {
			g.Down(link)
		}
	}
	for i := 0; i < hubs; i++ {
		for j := i + 1; j < hubs; j++ {
			if rng.IntN(100) < 70 {
				join(names[i], names[j])
			}
		}
	}
	for i := hubs; i < count; i++ {
		first := rng.IntN(hubs)
		join(names[first], names[i])
		if hubs > 1 && rng.IntN(100) < 40 {
			second := (first + 1 + rng.IntN(hubs-1)) % hubs
			join(names[second], names[i])
		}
	}
	for i := hubs; i < count; i++ {
		for j := i + 1; j < count; j++ {
			if rng.IntN(100) < 8 {
				join(names[i], names[j])
			}
		}
	}
	owned := route.Owned{}
	for i, node := range names {
		owned[node] = []netip.Prefix{
			netip.MustParsePrefix(fmt.Sprintf("10.%d.0.0/24", i)),
			netip.MustParsePrefix(fmt.Sprintf("10.200.0.%d/32", i)),
		}
	}
	return g, owned
}
