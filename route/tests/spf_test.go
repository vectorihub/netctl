package tests

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// pathText writes a path as "a > b > c", for readable failure messages.
func pathText(path []types.NodeID) string {
	text := ""
	for i, node := range path {
		if i > 0 {
			text += " > "
		}
		text += string(node)
	}
	return text
}

// wantBest checks the best path from src to dst: its total cost, its first hop
// and the whole path.
func wantBest(t *testing.T, best map[types.NodeID]route.Best, dst types.NodeID, cost int, first types.NodeID, path ...types.NodeID) {
	t.Helper()
	got, ok := best[dst]
	if !ok {
		t.Fatalf("no path to %s, want %s at cost %d", dst, pathText(path), cost)
	}
	if got.Cost != cost || got.First != first || !slices.Equal(got.Path, path) {
		t.Errorf("path to %s = %s (cost %d, first hop %s), want %s (cost %d, first hop %s)",
			dst, pathText(got.Path), got.Cost, got.First, pathText(path), cost, first)
	}
}

// P1-ROUTE-013
//
// TestSpokeNeverTransits checks the rule that makes the network hub-and-spoke.
// Spokes a and b and spokes b and c have direct links, but a and c do not, and
// all of them hang off one hub. Going from a to c through b would cost 20;
// going through the hub costs 40. The hub path must win, because b is a
// spoke and never forwards for others.
func TestSpokeNeverTransits(t *testing.T) {
	g := fixture.NewGraph().Hub("hub").Spoke("a", "b", "c", "d")
	for _, spoke := range []types.NodeID{"a", "b", "c", "d"} {
		g.Link("hub", spoke, 20)
	}
	g.Link("a", "b", 10)
	g.Link("b", "c", 10)

	fromA := route.SPF(g, "a", "")
	wantBest(t, fromA, "c", 40, "hub", "a", "hub", "c")
	wantBest(t, fromA, "b", 10, "b", "a", "b")
	wantBest(t, fromA, "d", 40, "hub", "a", "hub", "d")

	fromB := route.SPF(g, "b", "")
	wantBest(t, fromB, "a", 10, "a", "b", "a")
	wantBest(t, fromB, "c", 10, "c", "b", "c")
	wantBest(t, fromB, "d", 40, "hub", "b", "hub", "d")
}

// TestSpokeSourceUsesItsOwnLinks checks that the rule applies only to a spoke
// in the middle of a path: a spoke that is the source can use all its links.
func TestSpokeSourceUsesItsOwnLinks(t *testing.T) {
	best := route.SPF(fixture.ThreeSite(true), "builder-1", "")
	wantBest(t, best, "dgx-spark", 10, "dgx-spark", "builder-1", "dgx-spark")
	wantBest(t, best, "aws-hub", 20, "aws-hub", "builder-1", "aws-hub")
	if len(best) != 2 {
		t.Errorf("builder-1 reaches %d nodes, want 2", len(best))
	}
}

// TestPathCanEndAtASpokeButNotPassThrough checks the chain a, b, c with no
// hub: a can reach b, but not c, because the only way to c crosses b.
func TestPathCanEndAtASpokeButNotPassThrough(t *testing.T) {
	g := fixture.NewGraph().Spoke("a", "b", "c")
	g.Link("a", "b", 10)
	g.Link("b", "c", 10)
	best := route.SPF(g, "a", "")
	wantBest(t, best, "b", 10, "b", "a", "b")
	if _, ok := best["c"]; ok {
		t.Errorf("a reaches c through spoke b: %s", pathText(best["c"].Path))
	}
}

// TestUnknownRoleNeverForwards checks that only a node known to be a hub
// forwards: a node with no role set is treated like a spoke.
func TestUnknownRoleNeverForwards(t *testing.T) {
	g := fixture.NewGraph().Spoke("a", "c").Node("m", types.RoleUnknown)
	g.Link("a", "m", 10)
	g.Link("m", "c", 10)
	best := route.SPF(g, "a", "")
	if _, ok := best["c"]; ok {
		t.Errorf("a reaches c through a node of unknown role: %s", pathText(best["c"].Path))
	}
	wantBest(t, best, "m", 10, "m", "a", "m")
}

// P1-ROUTE-014
//
// TestEqualCostTieChoosesSmallerNextHop checks the tie-break. Two hubs both
// join s1 and s2 at the same cost, so there are two equal paths. The one
// through the lexically smaller hub must win, and it must win on every run
// whatever order the edges are listed in.
func TestEqualCostTieChoosesSmallerNextHop(t *testing.T) {
	g := fixture.NewGraph().Hub("hub-a", "hub-b").Spoke("s1", "s2")
	for _, hub := range []types.NodeID{"hub-a", "hub-b"} {
		g.Link(hub, "s1", 20)
		g.Link(hub, "s2", 20)
	}
	rng := rand.New(rand.NewPCG(14, 1))
	for run := 0; run < 50; run++ {
		best := route.SPF(g.Shuffled(rng), "s1", "")
		wantBest(t, best, "s2", 40, "hub-a", "s1", "hub-a", "s2")
	}
}

// TestFewerHopsBeatNextHopOrder checks that the tie-break looks at hops before
// the next hop's name. Two paths cost 40. The one through z has two hops and
// the one through b has three, so z wins even though b sorts first.
func TestFewerHopsBeatNextHopOrder(t *testing.T) {
	g := fixture.NewGraph().Hub("b", "h", "z").Spoke("s", "d")
	g.Link("s", "z", 10)
	g.Link("z", "d", 30)
	g.Link("s", "b", 10)
	g.Link("b", "h", 10)
	g.Link("h", "d", 20)
	best := route.SPF(g, "s", "")
	wantBest(t, best, "d", 40, "z", "s", "z", "d")
}

// TestLowerCostBeatsEverything checks that cost is looked at first: a path
// with more hops and a smaller first-hop name wins if it is cheaper.
func TestLowerCostBeatsEverything(t *testing.T) {
	g := fixture.NewGraph().Hub("a", "b", "z").Spoke("s", "d")
	g.Link("s", "z", 10)
	g.Link("z", "d", 30)
	g.Link("s", "a", 10)
	g.Link("a", "b", 5)
	g.Link("b", "d", 5)
	best := route.SPF(g, "s", "")
	wantBest(t, best, "d", 20, "a", "s", "a", "b", "d")
}

// TestTieAfterFirstHopChoosesSmallerPath checks the last tie-break. Both paths
// start with the same hop and cost the same; the one whose nodes sort first
// wins.
func TestTieAfterFirstHopChoosesSmallerPath(t *testing.T) {
	g := fixture.NewGraph().Hub("h1", "x", "y").Spoke("s", "d")
	g.Link("s", "h1", 10)
	g.Link("h1", "x", 10)
	g.Link("h1", "y", 10)
	g.Link("x", "d", 10)
	g.Link("y", "d", 10)
	rng := rand.New(rand.NewPCG(14, 2))
	for run := 0; run < 50; run++ {
		best := route.SPF(g.Shuffled(rng), "s", "")
		wantBest(t, best, "d", 30, "h1", "s", "h1", "x", "d")
	}
}

// TestSPFWithoutALink checks the "without" argument that backup routes rely
// on: that link is ignored in both directions.
func TestSPFWithoutALink(t *testing.T) {
	g := fixture.ThreeSite(true)
	direct := types.LinkIDOf("builder-1", "dgx-spark")
	hubLink := types.LinkIDOf("aws-hub", "builder-1")

	best := route.SPF(g, "builder-1", direct)
	wantBest(t, best, "dgx-spark", 40, "aws-hub", "builder-1", "aws-hub", "dgx-spark")

	best = route.SPF(g, "builder-1", hubLink)
	wantBest(t, best, "dgx-spark", 10, "dgx-spark", "builder-1", "dgx-spark")
	if _, ok := best["aws-hub"]; ok {
		t.Errorf("builder-1 reaches aws-hub with its hub link removed, via %s", pathText(best["aws-hub"].Path))
	}

	best = route.SPF(g, "dgx-spark", direct)
	wantBest(t, best, "builder-1", 40, "aws-hub", "dgx-spark", "aws-hub", "builder-1")
}

// TestSPFIgnoresDownAndNegativeEdges checks that a link that is down carries no
// traffic, and that an edge with a negative cost, which Dijkstra cannot handle,
// is skipped rather than followed.
func TestSPFIgnoresDownAndNegativeEdges(t *testing.T) {
	best := route.SPF(fixture.ThreeSiteDirectDown(), "builder-1", "")
	wantBest(t, best, "dgx-spark", 40, "aws-hub", "builder-1", "aws-hub", "dgx-spark")

	g := fixture.NewGraph().Hub("hub").Spoke("a", "b")
	g.Link("a", "hub", 10)
	g.Link("hub", "b", -5)
	best = route.SPF(g, "a", "")
	if _, ok := best["b"]; ok {
		t.Errorf("a reaches b over a negative-cost link: %s", pathText(best["b"].Path))
	}
}

// TestSPFUnknownSourceAndZeroCost checks two edge cases: a source that is not in
// the graph reaches nothing, and zero-cost links work (the hop count then
// settles ties).
func TestSPFUnknownSourceAndZeroCost(t *testing.T) {
	if got := route.SPF(fixture.ThreeSite(true), "nobody", ""); len(got) != 0 {
		t.Errorf("SPF from a missing node reached %d nodes, want 0", len(got))
	}
	g := fixture.NewGraph().Hub("h1", "h2").Spoke("a", "b")
	g.Link("a", "h1", 0)
	g.Link("h1", "b", 0)
	g.Link("a", "h2", 0)
	g.Link("h2", "h1", 0)
	best := route.SPF(g, "a", "")
	wantBest(t, best, "b", 0, "h1", "a", "h1", "b")
}

// TestSPFPathsDoNotShareMemory checks that changing one returned path cannot
// change another.
func TestSPFPathsDoNotShareMemory(t *testing.T) {
	best := route.SPF(fixture.ThreeSite(true), "aws-hub", "")
	best["builder-1"].Path[0] = "tampered"
	if best["dgx-spark"].Path[0] != "aws-hub" {
		t.Errorf("changing one path changed another: %s", pathText(best["dgx-spark"].Path))
	}
}

// randomGraph builds a small random network of between 2 and 7 nodes. Nodes
// are hubs or spokes at random; each pair is joined with some probability by a
// link of cost 0 to 5, and a few links are down. It returns the graph.
func randomGraph(rng *rand.Rand) *fixture.Graph {
	count := 2 + rng.IntN(6)
	g := fixture.NewGraph()
	names := make([]types.NodeID, count)
	for i := range names {
		names[i] = types.NodeID(fmt.Sprintf("n%d", i))
		if rng.IntN(5) < 2 {
			g.Hub(names[i])
		} else {
			g.Spoke(names[i])
		}
	}
	for i := 0; i < count; i++ {
		for j := i + 1; j < count; j++ {
			if rng.IntN(100) < 45 {
				link := g.Link(names[i], names[j], rng.IntN(6))
				if rng.IntN(100) < 10 {
					g.Down(link)
				}
			}
		}
	}
	return g
}

// bruteForce finds the best path from src to every node by trying every
// simple path and keeping the smallest under the documented order: cost, then
// hops, then first hop, then first link, then the list of nodes. Only src and
// hubs forward, and down links are not used. It is slow and obviously right,
// which makes it the oracle for SPF.
func bruteForce(g route.Graph, src types.NodeID) map[types.NodeID]route.Best {
	found := map[types.NodeID]route.Best{}
	var walk func(node types.NodeID, current route.Best)
	walk = func(node types.NodeID, current route.Best) {
		if node != src {
			if best, seen := found[node]; !seen || lessBest(current, best) {
				found[node] = route.Best{Cost: current.Cost, Hops: current.Hops, First: current.First, Link: current.Link, Path: slices.Clone(current.Path)}
			}
			if g.Role(node) != types.RoleHub {
				return
			}
		}
		for _, edge := range g.Edges(node) {
			if edge.Down || slices.Contains(current.Path, edge.To) {
				continue
			}
			next := route.Best{Cost: current.Cost + edge.Cost, Hops: current.Hops + 1, First: current.First, Link: current.Link,
				Path: append(slices.Clone(current.Path), edge.To)}
			if node == src {
				next.First, next.Link = edge.To, edge.Link
			}
			walk(edge.To, next)
		}
	}
	walk(src, route.Best{Path: []types.NodeID{src}})
	return found
}

// lessBest reports whether first is a better path than second under the order
// bruteForce documents. It is written out separately from the library so that
// the two cannot share a mistake.
func lessBest(first, second route.Best) bool {
	switch {
	case first.Cost != second.Cost:
		return first.Cost < second.Cost
	case first.Hops != second.Hops:
		return first.Hops < second.Hops
	case first.First != second.First:
		return first.First < second.First
	case first.Link != second.Link:
		return first.Link < second.Link
	}
	return slices.Compare(first.Path, second.Path) < 0
}

// TestSPFAgainstBruteForce compares SPF with the brute-force oracle on 3,000
// random networks, from every source: same set of reachable nodes, and for
// each the same cost, hops, first hop, first link and path. It also checks
// that shuffling the order of nodes and edges changes nothing.
func TestSPFAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(33, 1))
	for round := 0; round < 3000; round++ {
		g := randomGraph(rng)
		shuffled := g.Shuffled(rng)
		for _, src := range g.Nodes() {
			want := bruteForce(g, src)
			got := route.SPF(g, src, "")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d, from %s: SPF differs from brute force\n got: %v\nwant: %v", round, src, got, want)
			}
			if again := route.SPF(shuffled, src, ""); !reflect.DeepEqual(again, got) {
				t.Fatalf("round %d, from %s: shuffling the graph changed the result\n got: %v\nwant: %v", round, src, again, got)
			}
		}
	}
}

// TestSPFWithoutAgainstBruteForce checks the "without" argument the same way:
// removing any one link from a random network gives what brute force finds
// on the network with that link marked down.
func TestSPFWithoutAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(33, 2))
	for round := 0; round < 1500; round++ {
		g := randomGraph(rng)
		for _, src := range g.Nodes() {
			for _, edge := range g.Edges(src) {
				cut := fixtureWithout(g, edge.Link)
				want := bruteForce(cut, src)
				got := route.SPF(g, src, edge.Link)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("round %d, from %s without %s: SPF differs from brute force\n got: %v\nwant: %v", round, src, edge.Link, got, want)
				}
			}
		}
	}
}

// fixtureWithout returns a copy of g with one link marked down.
func fixtureWithout(g *fixture.Graph, link types.LinkID) *fixture.Graph {
	copyOf := g.Shuffled(rand.New(rand.NewPCG(1, 1)))
	return copyOf.Down(link)
}

// oddLinks is a graph whose link IDs do not follow the naming rule: s joins the
// hubs h1 and h2 on links "zz-1" and "aa-2", the opposite order to the hubs'
// own names. d joins them on "zz-3" and "aa-4". Every link costs 10.
type oddLinks struct{}

// Nodes returns the four nodes.
func (oddLinks) Nodes() []types.NodeID { return []types.NodeID{"s", "h1", "h2", "d"} }

// Role returns hub for h1 and h2 and spoke for the others.
func (oddLinks) Role(node types.NodeID) types.Role {
	if node == "h1" || node == "h2" {
		return types.RoleHub
	}
	return types.RoleSpoke
}

// Edges returns the edges out of a node.
func (oddLinks) Edges(node types.NodeID) []route.Edge {
	switch node {
	case "s":
		return []route.Edge{{Link: "zz-1", To: "h1", Cost: 10}, {Link: "aa-2", To: "h2", Cost: 10}}
	case "d":
		return []route.Edge{{Link: "zz-3", To: "h1", Cost: 10}, {Link: "aa-4", To: "h2", Cost: 10}}
	case "h1":
		return []route.Edge{{Link: "zz-1", To: "s", Cost: 10}, {Link: "zz-3", To: "d", Cost: 10}}
	}
	return []route.Edge{{Link: "aa-2", To: "s", Cost: 10}, {Link: "aa-4", To: "d", Cost: 10}}
}

// TestNextHopIsComparedBeforeLinkID checks that the tie-break looks at the next
// hop's name before the link's ID, even when the two disagree: the link to h2
// has the smaller ID, but h1 is the smaller next hop, so h1 wins.
func TestNextHopIsComparedBeforeLinkID(t *testing.T) {
	best := route.SPF(oddLinks{}, "s", "")
	wantBest(t, best, "d", 20, "h1", "s", "h1", "d")
	if best["d"].Link != "zz-1" {
		t.Errorf("first link = %s, want zz-1", best["d"].Link)
	}
}

// parallelLinks is a graph of a spoke s and a hub h joined by two links of equal
// cost, "z-link" listed before "a-link".
type parallelLinks struct{}

// Nodes returns the two nodes.
func (parallelLinks) Nodes() []types.NodeID { return []types.NodeID{"s", "h"} }

// Role returns spoke for s and hub for h.
func (parallelLinks) Role(node types.NodeID) types.Role {
	if node == "h" {
		return types.RoleHub
	}
	return types.RoleSpoke
}

// Edges returns both links from either end, the larger ID first.
func (parallelLinks) Edges(node types.NodeID) []route.Edge {
	other := types.NodeID("h")
	if node == "h" {
		other = "s"
	}
	return []route.Edge{{Link: "z-link", To: other, Cost: 10}, {Link: "a-link", To: other, Cost: 10}}
}

// TestParallelLinksChooseTheSmallerLinkID checks the tie-break between two links
// to the same neighbour at the same cost: the one with the smaller ID wins,
// whichever is listed first.
func TestParallelLinksChooseTheSmallerLinkID(t *testing.T) {
	best := route.SPF(parallelLinks{}, "s", "")
	if got := best["h"]; got.Link != "a-link" || got.Cost != 10 {
		t.Errorf("path to h uses %s at cost %d, want a-link at 10", got.Link, got.Cost)
	}
	if got := route.SPF(parallelLinks{}, "s", "a-link")["h"]; got.Link != "z-link" {
		t.Errorf("without a-link the path uses %s, want z-link", got.Link)
	}
}
