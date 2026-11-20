package tests

import (
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// nodesOf returns the nodes of a trace as plain strings, for comparisons.
func nodesOf(trace sim.Trace) []types.NodeID { return trace.Nodes() }

// TestDeliveredWhereItStarts checks that a packet for the sending node's own
// address is delivered at once, without crossing a link.
func TestDeliveredWhereItStarts(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	trace := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "dgx-spark")
	if !trace.Delivered() || trace.Crossings() != 0 || len(trace.Hops) != 1 || trace.Hops[0].Route != nil {
		t.Errorf("trace = %+v, want delivered at the first hop with no crossing", trace)
	}
}

// TestSendFollowsTheTables checks the journey with the direct link up (one
// crossing) and with it down (through the hub, two crossings), and that every
// forwarding hop records the route it used.
func TestSendFollowsTheTables(t *testing.T) {
	up := computePlan(t, fixture.ThreeSite(true))
	trace := up.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "builder-1")
	if !trace.Delivered() || !slices.Equal(nodesOf(trace), []types.NodeID{"builder-1", "dgx-spark"}) {
		t.Fatalf("direct link up: %+v", trace)
	}
	if hop := trace.Hops[0]; hop.Result != sim.Forwarded || hop.Route == nil || hop.Route.Via != "builder-1~dgx-spark" {
		t.Errorf("first hop = %+v, want forwarded over the direct link", hop)
	}

	down := computePlan(t, fixture.ThreeSiteDirectDown())
	trace = down.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "builder-1")
	if !trace.Delivered() || !slices.Equal(nodesOf(trace), []types.NodeID{"builder-1", "aws-hub", "dgx-spark"}) || trace.Crossings() != 2 {
		t.Errorf("direct link down: %+v", trace)
	}
}

// TestHopRoutesAreCopies checks that changing a route in a trace cannot change
// the table it came from.
func TestHopRoutesAreCopies(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	trace := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "builder-1")
	trace.Hops[0].Route.Metric = 999
	trace.Hops[0].Route.NextHop = "tampered"
	again := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "builder-1")
	if !again.Delivered() || again.Hops[0].Route.Metric == 999 {
		t.Errorf("the table changed: %+v", again.Hops[0].Route)
	}
}

// plantTwoNodeLoop makes the hub send dgx-spark's overlay address back to
// builder-1, which sends it to the hub, in a plan where the direct link is down.
func plantTwoNodeLoop(t *testing.T) plan {
	t.Helper()
	p := computePlan(t, fixture.ThreeSite(false))
	retarget(p.tables, "aws-hub", "10.42.240.3/32", "builder-1", "aws-hub~builder-1")
	return p
}

// P1-SIM-010
//
// TestPlantedTwoNodeLoop checks that a loop between the hub and builder-1 is
// reported as LOOP, and that the trace lists the repeating nodes: builder-1,
// the hub, and builder-1 again as the node where the loop was caught.
func TestPlantedTwoNodeLoop(t *testing.T) {
	p := plantTwoNodeLoop(t)
	trace := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "builder-1")
	if trace.Final != sim.LoopDetected {
		t.Fatalf("final = %s, want LOOP", trace.Final)
	}
	if want := []types.NodeID{"builder-1", "aws-hub", "builder-1"}; !slices.Equal(nodesOf(trace), want) {
		t.Errorf("nodes = %v, want %v", nodesOf(trace), want)
	}
	if last := trace.Hops[len(trace.Hops)-1]; last.Result != sim.LoopDetected {
		t.Errorf("last hop = %s, want LOOP", last.Result)
	}
}

// P1-SIM-011
//
// TestPlantedThreeNodeLoop checks a loop across the hub and both spokes:
// builder-1 sends a made-up prefix to the hub, the hub to dgx-spark, and dgx-spark
// back to builder-1.
func TestPlantedThreeNodeLoop(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	addRoute(p.tables, "builder-1", "10.9.9.0/24", "aws-hub")
	addRoute(p.tables, "aws-hub", "10.9.9.0/24", "dgx-spark")
	addRoute(p.tables, "dgx-spark", "10.9.9.0/24", "builder-1")
	trace := p.net().Send(sim.Packet{Src: b1Addr, Dst: addrOf("10.9.9.9")}, "builder-1")
	if trace.Final != sim.LoopDetected {
		t.Fatalf("final = %s, want LOOP", trace.Final)
	}
	if want := []types.NodeID{"builder-1", "aws-hub", "dgx-spark", "builder-1"}; !slices.Equal(nodesOf(trace), want) {
		t.Errorf("nodes = %v, want %v", nodesOf(trace), want)
	}
	if result := p.net().Run(sim.Flow{Label: "loop", Family: sim.FamilyOverlay, Src: b1Addr, Dst: addrOf("10.9.9.9"), From: "builder-1", To: "dgx-spark"}, false); result.Verdict != sim.VerdictLoop {
		t.Errorf("verdict = %s, want LOOP", result.Verdict)
	}
}

// P1-SIM-012
//
// TestLoopIsCaughtBeforeTheHopLimit checks that with a hop limit of 64 a loop is
// reported as LOOP and not TTL_EXCEEDED, after only a few hops: the exact test
// fires long before the safety net. A hop limit too small to complete the
// journey is TTL_EXCEEDED, not LOOP.
func TestLoopIsCaughtBeforeTheHopLimit(t *testing.T) {
	p := plantTwoNodeLoop(t)
	trace := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr, TTL: 64}, "builder-1")
	if trace.Final != sim.LoopDetected || len(trace.Hops) > 4 {
		t.Errorf("final %s after %d hops, want LOOP within 4", trace.Final, len(trace.Hops))
	}
	short := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr, TTL: 1}, "builder-1")
	if short.Final != sim.TTLExceeded {
		t.Errorf("with a hop limit of 1, final = %s, want TTL_EXCEEDED", short.Final)
	}
}

// TestHopLimitBoundaries checks the hop limit exactly: a path of two crossings
// is delivered with a limit of 2, runs out at the hub with a limit of 1, and a
// limit of zero means the default. A negative limit is already used up.
func TestHopLimitBoundaries(t *testing.T) {
	p := computePlan(t, fixture.ThreeSiteDirectDown())
	send := func(ttl int) sim.Trace {
		return p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr, TTL: ttl}, "builder-1")
	}
	if trace := send(2); !trace.Delivered() {
		t.Errorf("limit 2: %s, want delivered", trace.Final)
	}
	trace := send(1)
	if trace.Final != sim.TTLExceeded || trace.Hops[len(trace.Hops)-1].Node != "aws-hub" {
		t.Errorf("limit 1: %s at %v, want TTL_EXCEEDED at aws-hub", trace.Final, nodesOf(trace))
	}
	if trace := send(0); !trace.Delivered() || trace.Packet.TTL != sim.DefaultTTL {
		t.Errorf("limit 0: %s with TTL %d, want delivered with %d", trace.Final, trace.Packet.TTL, sim.DefaultTTL)
	}
	if trace := send(-3); trace.Final != sim.TTLExceeded || len(trace.Hops) != 1 {
		t.Errorf("limit -3: %s after %d hops, want TTL_EXCEEDED at once", trace.Final, len(trace.Hops))
	}
}

// TestBlackHoleAndMissingTable checks a packet to an address no route covers,
// and a packet injected at a node that has no table at all: both are black holes
// at that node.
func TestBlackHoleAndMissingTable(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	trace := p.net().Send(sim.Packet{Src: b1Addr, Dst: addrOf("192.168.1.1")}, "builder-1")
	if trace.Final != sim.BlackHole || trace.Hops[0].Route != nil {
		t.Errorf("no route: %+v", trace)
	}
	ghost := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "no-such-node")
	if ghost.Final != sim.BlackHole || len(ghost.Hops) != 1 || ghost.Hops[0].Node != "no-such-node" {
		t.Errorf("no table: %+v", ghost)
	}
	noTables := sim.Net{Local: func(types.NodeID, netip.Addr) bool { return false }}
	if trace := noTables.Send(sim.Packet{Dst: dgxAddr}, "builder-1"); trace.Final != sim.BlackHole {
		t.Errorf("nil tables: %s, want BLACK_HOLE", trace.Final)
	}
}

// P1-SIM-030, P1-SIM-031
//
// TestWrongNeighbourAndLinkDown checks the two ways a route can be unusable. A
// route to a node that is not joined by the link it names is WRONG_NEIGHBOUR;
// an active route over a link that is down is LINK_DOWN, which is what a
// failover that did not happen looks like. The failed hop records the route.
func TestWrongNeighbourAndLinkDown(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(false))
	retarget(p.tables, "builder-1", "10.42.240.3/32", "dgx-spark", "aws-hub~builder-1")
	trace := p.net().Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "builder-1")
	if trace.Final != sim.WrongNeighbour || trace.Hops[0].Route == nil || trace.Hops[0].Route.NextHop != "dgx-spark" {
		t.Errorf("wrong neighbour: %+v", trace)
	}

	healthy := computePlan(t, fixture.ThreeSite(true))
	trace = healthy.netOver(fixture.ThreeSiteDirectDown()).Send(sim.Packet{Src: b1Addr, Dst: dgxAddr}, "builder-1")
	if trace.Final != sim.LinkDown || trace.Hops[0].Route == nil || trace.Hops[0].Route.Via != "builder-1~dgx-spark" {
		t.Errorf("link down: %+v", trace)
	}
}

// TestNilQuestions checks how a Net with missing functions behaves: nothing is
// local, nothing is adjacent (so every forward is a wrong neighbour, which makes a
// missing wire obvious), and every link is up.
func TestNilQuestions(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	full := p.net()
	noLocal := full
	noLocal.Local = nil
	if trace := noLocal.Send(sim.Packet{Dst: dgxAddr}, "dgx-spark"); trace.Delivered() {
		t.Error("with no Local, a packet was delivered")
	}
	noAdjacent := full
	noAdjacent.Adjacent = nil
	if trace := noAdjacent.Send(sim.Packet{Dst: dgxAddr}, "builder-1"); trace.Final != sim.WrongNeighbour {
		t.Errorf("with no Adjacent, final = %s, want WRONG_NEIGHBOUR", trace.Final)
	}
	noUp := full
	noUp.Up = nil
	if trace := noUp.Send(sim.Packet{Dst: dgxAddr}, "builder-1"); !trace.Delivered() {
		t.Errorf("with no Up, final = %s, want delivered", trace.Final)
	}
}

// corruptRandomly damages a few tables in random ways: retarget a route to any
// node over any link (including one that does not exist), switch routes on or
// off, delete a route, and add a default route or a route for a made-up prefix.
func corruptRandomly(rng *rand.Rand, nodes []types.NodeID, tables route.Tables) {
	for damage := 1 + rng.IntN(8); damage > 0; damage-- {
		node := nodes[rng.IntN(len(nodes))]
		table := tables[node]
		switch rng.IntN(5) {
		case 0:
			if len(table.Routes) > 0 {
				r := &table.Routes[rng.IntN(len(table.Routes))]
				r.NextHop = nodes[rng.IntN(len(nodes))]
				r.Via = types.LinkIDOf(node, r.NextHop)
				if rng.IntN(4) == 0 {
					r.Via = "bogus~link"
				}
			}
		case 1:
			if len(table.Routes) > 0 {
				r := &table.Routes[rng.IntN(len(table.Routes))]
				r.Active = !r.Active
			}
		case 2:
			if len(table.Routes) > 0 {
				at := rng.IntN(len(table.Routes))
				table.Routes = slices.Delete(table.Routes, at, at+1)
			}
		case 3:
			addRoute(tables, node, "0.0.0.0/0", nodes[rng.IntN(len(nodes))])
		default:
			addRoute(tables, node, prefixOf("10.200.0.0/24").String(), nodes[rng.IntN(len(nodes))])
		}
	}
}

// P1-SIM-013
//
// TestRandomCorruptionAlwaysTerminates plants random damage in the tables of
// 2,000 random networks and sends five packets through each, 10,000 in all, with
// hop limits from 0 to 64. Every packet must end with a real result, and the
// trace must be well formed: all hops but the last forwarded, the last hop
// carrying the final result, no more hops than the limit or the number of nodes
// allows, and no node repeated except the last one, where a loop was caught or the hop
// limit ran out as the packet came back round. Flows
// judged from the same damaged tables must always get a verdict.
func TestRandomCorruptionAlwaysTerminates(t *testing.T) {
	rng := rand.New(rand.NewPCG(40, 1))
	limits := []int{0, 1, 2, 5, 16, 64}
	for round := 0; round < 2000; round++ {
		g, owned := fixture.RandomNetwork(rng, 4, 10)
		result, err := route.Compute(g, route.Input{Owned: owned})
		if err != nil {
			t.Fatalf("round %d: Compute: %v", round, err)
		}
		nodes := g.Nodes()
		slices.Sort(nodes)
		corruptRandomly(rng, nodes, result.Tables)
		net := sim.NewNet(g, result.Tables, owned, nil)
		for send := 0; send < 5; send++ {
			from := nodes[rng.IntN(len(nodes))]
			target := owned[nodes[rng.IntN(len(nodes))]][1].Addr()
			if rng.IntN(5) == 0 {
				target = addrOf("192.168.7.7")
			}
			ttl := limits[rng.IntN(len(limits))]
			trace := net.Send(sim.Packet{Src: addrOf("10.200.0.1"), Dst: target, TTL: ttl}, from)
			checkTrace(t, round, trace, ttl, len(nodes))
			flow := sim.Flow{Label: "random", Family: sim.FamilyOverlay, Src: owned[from][1].Addr(), Dst: target, From: from, To: nodes[rng.IntN(len(nodes))]}
			if verdict := net.Run(flow, rng.IntN(10) == 0).Verdict; verdict == sim.VerdictUnknown {
				t.Fatalf("round %d: a flow got no verdict", round)
			}
		}
	}
}

// checkTrace checks that a trace is well formed, whatever the tables were like.
func checkTrace(t *testing.T, round int, trace sim.Trace, ttl, nodes int) {
	t.Helper()
	if trace.Final == sim.HopUnknown || len(trace.Hops) == 0 {
		t.Fatalf("round %d: a packet ended with no result: %+v", round, trace)
	}
	last := trace.Hops[len(trace.Hops)-1]
	if last.Result != trace.Final {
		t.Fatalf("round %d: final %s but the last hop says %s", round, trace.Final, last.Result)
	}
	if ttl == 0 {
		ttl = sim.DefaultTTL
	}
	if limit := min(max(ttl, 0), nodes) + 1; len(trace.Hops) > limit {
		t.Fatalf("round %d: %d hops, want at most %d", round, len(trace.Hops), limit)
	}
	seen := map[types.NodeID]bool{}
	for i, hop := range trace.Hops {
		if i < len(trace.Hops)-1 && hop.Result != sim.Forwarded {
			t.Fatalf("round %d: hop %d of %d is %s, want forwarded", round, i, len(trace.Hops), hop.Result)
		}
		endsHere := i == len(trace.Hops)-1 && (trace.Final == sim.LoopDetected || trace.Final == sim.TTLExceeded)
		if seen[hop.Node] && !endsHere {
			t.Fatalf("round %d: node %s repeats before the end: %v", round, hop.Node, nodesOf(trace))
		}
		seen[hop.Node] = true
	}
	if trace.Final == sim.LoopDetected && !seen[last.Node] {
		t.Fatalf("round %d: a loop was reported at a node the packet had not visited", round)
	}
}

// P1-SIM-014
//
// TestUncorruptedPlansNeverLoop checks 1,500 random networks computed by the real
// engine and not damaged: for every pair of nodes the overlay flow is never LOOP,
// never BLACK_HOLE, NO_RETURN, LINK_DOWN, WRONG_NEIGHBOUR or TTL_EXCEEDED, and is
// either OK or, when two equal-cost paths are tied differently in the two
// directions, ASYMMETRIC. A pair the engine reported unreachable is judged
// NO_HEALTHY_PATH. The engine and the simulator therefore agree.
func TestUncorruptedPlansNeverLoop(t *testing.T) {
	rng := rand.New(rand.NewPCG(40, 2))
	counts := map[sim.Verdict]int{}
	for round := 0; round < 1500; round++ {
		g, owned := fixture.RandomNetwork(rng, 4, 14)
		result, err := route.Compute(g, route.Input{Owned: owned})
		if err != nil {
			t.Fatalf("round %d: Compute: %v", round, err)
		}
		net := sim.NewNet(g, result.Tables, owned, nil)
		cut := map[[2]types.NodeID]bool{}
		for _, pair := range result.Unreachable {
			cut[[2]types.NodeID{pair.From, pair.To}] = true
		}
		for _, from := range g.Nodes() {
			for _, to := range g.Nodes() {
				if from == to {
					continue
				}
				flow := sim.Flow{Label: string(from) + string(to), Family: sim.FamilyOverlay, Src: owned[from][1].Addr(), Dst: owned[to][1].Addr(), From: from, To: to}
				got := net.Run(flow, cut[[2]types.NodeID{from, to}])
				counts[got.Verdict]++
				switch {
				case cut[[2]types.NodeID{from, to}]:
					if got.Verdict != sim.VerdictNoHealthyPath {
						t.Fatalf("round %d: %s to %s is cut off but got %s", round, from, to, got.Verdict)
					}
				case got.Verdict != sim.VerdictOK && got.Verdict != sim.VerdictAsymmetric:
					t.Fatalf("round %d: %s to %s got %s: %+v", round, from, to, got.Verdict, got.Problems)
				}
			}
		}
	}
	if counts[sim.VerdictOK] == 0 || counts[sim.VerdictNoHealthyPath] == 0 {
		t.Errorf("the property was not exercised: verdicts %v", counts)
	}
	t.Logf("verdicts over all random pairs: %v", counts)
}
