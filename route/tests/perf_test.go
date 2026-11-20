package tests

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// scaleNetwork builds a hub-and-spoke network of the given size: hubs hubs
// joined in a ring, every spoke joined to one hub (round robin), and
// directLinks direct links between random pairs of spokes. Every node
// advertises a /24 and a /32. The generator is seeded, so every run builds the
// same network.
func scaleNetwork(nodes, hubs, directLinks int) (*fixture.Graph, route.Input) {
	rng := rand.New(rand.NewPCG(38, 1))
	g := fixture.NewGraph()
	names := make([]types.NodeID, nodes)
	for i := range names {
		names[i] = types.NodeID(fmt.Sprintf("n%04d", i))
		if i < hubs {
			g.Hub(names[i])
		} else {
			g.Spoke(names[i])
		}
	}
	for i := 0; hubs > 1 && i < hubs; i++ {
		g.Link(names[i], names[(i+1)%hubs], 5)
	}
	for i := hubs; i < nodes; i++ {
		g.Link(names[i%hubs], names[i], 20)
	}
	direct := map[types.LinkID]bool{}
	for len(direct) < directLinks {
		a, b := hubs+rng.IntN(nodes-hubs), hubs+rng.IntN(nodes-hubs)
		if a == b || direct[types.LinkIDOf(names[a], names[b])] {
			continue
		}
		direct[g.Link(names[a], names[b], 10)] = true
	}
	owned := route.Owned{}
	for i, node := range names {
		owned[node] = []netip.Prefix{
			netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 0}), 24),
			netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 200, byte(i >> 8), byte(i)}), 32),
		}
	}
	return g, route.Input{Owned: owned}
}

// BenchmarkCompute500 is P1-ROUTE-PERF-001: the full computation for 500 nodes,
// one hub and 200 direct links. The target is under one second per run.
func BenchmarkCompute500(b *testing.B) {
	g, input := scaleNetwork(500, 1, 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := route.Compute(g, input); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompute2000 times the computation for 2,000 nodes and 4 hubs, the
// shape of P1-ROUTE-PERF-002. The plan's target for that case covers
// validation only, which does not exist yet; this benchmark records what the
// route computation alone costs at that size.
func BenchmarkCompute2000(b *testing.B) {
	g, input := scaleNetwork(2000, 4, 400)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := route.Compute(g, input); err != nil {
			b.Fatal(err)
		}
	}
}

// P1-ROUTE-PERF-001
//
// TestPerformance500Nodes checks the same case as BenchmarkCompute500 as an
// ordinary test, so a slowdown fails the build instead of waiting for someone to
// run a benchmark: 500 nodes, one hub and 200 direct links must compute in
// under one second. It also checks the result is complete. It is skipped with
// -short and under the race detector, which slows code down several times over,
// and a time limit means nothing there.
func TestPerformance500Nodes(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing needs a normal, full run")
	}
	g, input := scaleNetwork(500, 1, 200)
	started := time.Now()
	result, err := route.Compute(g, input)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("computing 500 nodes took %v, want under 1s", elapsed)
	}
	t.Logf("500 nodes, 1 hub, 200 direct links: %v", elapsed)
	if len(result.Tables) != 500 || len(result.Unreachable) != 0 {
		t.Errorf("%d tables and %d unreachable pairs, want 500 and 0", len(result.Tables), len(result.Unreachable))
	}
	if routes := len(result.Tables["n0250"].Routes); routes < 2*499 {
		t.Errorf("a spoke has %d routes, want at least %d", routes, 2*499)
	}
}
