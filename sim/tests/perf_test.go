package tests

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// scaleNetwork builds a hub-and-spoke network of the given size: one hub, every
// other node joined to it, and directLinks direct links between random pairs of
// spokes. Every node holds a /24 site block and a /32 overlay address. The
// generator is seeded, so every run builds the same network.
func scaleNetwork(nodes, directLinks int) (*fixture.Graph, route.Input, []types.NodeID) {
	rng := rand.New(rand.NewPCG(46, 1))
	g := fixture.NewGraph()
	names := make([]types.NodeID, nodes)
	for i := range names {
		names[i] = types.NodeID(fmt.Sprintf("n%04d", i))
		if i == 0 {
			g.Hub(names[i])
		} else {
			g.Spoke(names[i])
		}
	}
	for i := 1; i < nodes; i++ {
		g.Link(names[0], names[i], 20)
	}
	direct := map[types.LinkID]bool{}
	for len(direct) < directLinks {
		a, b := 1+rng.IntN(nodes-1), 1+rng.IntN(nodes-1)
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
	return g, route.Input{Owned: owned}, names
}

// allPairsOverlay returns one overlay flow for every ordered pair of nodes: for n
// nodes, n×(n−1) flows. The overlay address of node i is the /32 scaleNetwork
// gave it.
func allPairsOverlay(names []types.NodeID) []sim.Flow {
	addr := func(i int) netip.Addr { return netip.AddrFrom4([4]byte{10, 200, byte(i >> 8), byte(i)}) }
	flows := make([]sim.Flow, 0, len(names)*(len(names)-1))
	for i, from := range names {
		for j, to := range names {
			if i != j {
				flows = append(flows, sim.Flow{Label: string(from) + "→" + string(to), Family: sim.FamilyOverlay, Src: addr(i), Dst: addr(j), From: from, To: to})
			}
		}
	}
	return flows
}

// runAll simulates every flow over the network the tables were computed for and
// returns how many were OK. Results are counted and dropped, not kept: 250,000
// full traces would measure the memory allocator more than the simulator.
func runAll(net sim.Net, flows []sim.Flow) (ok int) {
	for _, flow := range flows {
		if net.Run(flow, false).Verdict == sim.VerdictOK {
			ok++
		}
	}
	return ok
}

// BenchmarkAllPairs500 is P1-SIM-PERF-001: the all-pairs overlay flows of 500
// nodes (249,500 of them), each sent and answered, over tables the engine
// computed. The target is under ten seconds a run.
func BenchmarkAllPairs500(b *testing.B) {
	g, input, names := scaleNetwork(500, 200)
	result, err := route.Compute(g, input)
	if err != nil {
		b.Fatal(err)
	}
	net := sim.NewNet(g, result.Tables, input.Owned, input.Local)
	flows := allPairsOverlay(names)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ok := runAll(net, flows); ok != len(flows) {
			b.Fatalf("%d of %d flows OK", ok, len(flows))
		}
	}
}

// P1-SIM-PERF-001
//
// TestPerformanceAllPairs500 runs the same case as BenchmarkAllPairs500 as an
// ordinary test so a slowdown fails the build: 249,500 flows over 500 nodes in
// under ten seconds, every one OK. The routing computation is not part of the
// time, because it has its own budget. It is skipped with -short and under the
// race detector, where a time limit means nothing.
func TestPerformanceAllPairs500(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing needs a normal, full run")
	}
	g, input, names := scaleNetwork(500, 200)
	result, err := route.Compute(g, input)
	if err != nil {
		t.Fatal(err)
	}
	net := sim.NewNet(g, result.Tables, input.Owned, input.Local)
	flows := allPairsOverlay(names)
	started := time.Now()
	ok := runAll(net, flows)
	elapsed := time.Since(started)
	t.Logf("%d flows over 500 nodes: %v", len(flows), elapsed)
	if len(flows) != 500*499 || ok != len(flows) {
		t.Errorf("%d flows, %d OK; want %d, all OK", len(flows), ok, 500*499)
	}
	if elapsed > 10*time.Second {
		t.Errorf("simulating %d flows took %v, want under 10s", len(flows), elapsed)
	}
}
