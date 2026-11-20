package tests

import (
	"math/rand/v2"
	"net/netip"
	"reflect"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// largeRandomNetwork builds a random hub-and-spoke network of 4 to 25 nodes, the
// size where the brute-force oracle used for small networks would be far too
// slow, and returns what each node advertises and who owns each prefix.
func largeRandomNetwork(rng *rand.Rand) (*fixture.Graph, route.Owned, map[netip.Prefix]types.NodeID) {
	g, owned := fixture.RandomNetwork(rng, 4, 25)
	owner := map[netip.Prefix]types.NodeID{}
	for node, prefixes := range owned {
		for _, prefix := range prefixes {
			owner[prefix] = node
		}
	}
	return g, owned, owner
}

// oracleCost finds the cheapest cost and fewest hops from src to every node it
// can reach, with Bellman-Ford: relax every edge again and again until nothing
// improves. Only src and hubs forward, down links are not used, and the link
// "without", if not empty, is not used. It is a different algorithm from the
// library's Dijkstra, so a shared mistake is unlikely.
func oracleCost(g route.Graph, src types.NodeID, without types.LinkID) map[types.NodeID][2]int {
	best := map[types.NodeID][2]int{src: {0, 0}}
	for changed := true; changed; {
		changed = false
		for _, from := range g.Nodes() {
			here, reached := best[from]
			if !reached || (from != src && g.Role(from) != types.RoleHub) {
				continue
			}
			for _, edge := range g.Edges(from) {
				if edge.Down || edge.Link == without {
					continue
				}
				candidate := [2]int{here[0] + edge.Cost, here[1] + 1}
				known, seen := best[edge.To]
				if !seen || candidate[0] < known[0] || (candidate[0] == known[0] && candidate[1] < known[1]) {
					best[edge.To] = candidate
					changed = true
				}
			}
		}
	}
	delete(best, src)
	return best
}

// P1-ROUTE-018, P1-ROUTE-019
//
// TestPathPropertiesOnLargeRandomNetworks checks 600 random networks of up to 25
// nodes. For every active computed route and every backup, the path starts at
// the node and ends at the prefix owner (018), visits no node twice (019),
// crosses only working links, passes only through hubs and costs exactly the
// route's metric. Constant-time checks run on every route; the cost is compared
// with an independent Bellman-Ford.
func TestPathPropertiesOnLargeRandomNetworks(t *testing.T) {
	rng := rand.New(rand.NewPCG(37, 1))
	for round := 0; round < 600; round++ {
		g, owned, owner := largeRandomNetwork(rng)
		result := mustCompute(t, g, route.Input{Owned: owned})
		for _, src := range g.Nodes() {
			for _, r := range result.Tables[src].Routes {
				switch r.Source {
				case route.SourceComputed, route.SourceBackup:
					checkPath(t, round, g, src, r, owner[r.Prefix])
				case route.SourceConnected:
					if owner[r.Prefix] != src {
						t.Fatalf("round %d: %s holds a connected route for %s, owned by %s", round, src, r.Prefix, owner[r.Prefix])
					}
				default:
					t.Fatalf("round %d: unexpected route %s", round, rowOf(r))
				}
			}
		}
	}
}

// TestRoutesAreOptimalOnLargeRandomNetworks checks that every primary costs
// exactly what the independent Bellman-Ford finds, that a pair is unreachable
// exactly when the oracle cannot reach it, and that every backup costs exactly
// the best path that avoids the primary's first link, and exists exactly when
// there is one.
func TestRoutesAreOptimalOnLargeRandomNetworks(t *testing.T) {
	rng := rand.New(rand.NewPCG(37, 2))
	for round := 0; round < 400; round++ {
		g, owned, owner := largeRandomNetwork(rng)
		result := mustCompute(t, g, route.Input{Owned: owned})
		unreachable := map[[2]types.NodeID]bool{}
		for _, pair := range result.Unreachable {
			unreachable[[2]types.NodeID{pair.From, pair.To}] = true
		}
		for _, src := range g.Nodes() {
			best := oracleCost(g, src, "")
			for _, dst := range g.Nodes() {
				if dst == src {
					continue
				}
				if _, reachable := best[dst]; reachable == unreachable[[2]types.NodeID{src, dst}] {
					t.Fatalf("round %d: %s to %s: oracle reachable=%v, listed unreachable=%v", round, src, dst, reachable, unreachable[[2]types.NodeID{src, dst}])
				}
			}
			primaries := map[netip.Prefix]route.Route{}
			backups := map[netip.Prefix]route.Route{}
			for _, r := range result.Tables[src].Routes {
				switch r.Source {
				case route.SourceComputed:
					primaries[r.Prefix] = r
				case route.SourceBackup:
					backups[r.Prefix] = r
				}
			}
			for prefix, primary := range primaries {
				dst := owner[prefix]
				if want := best[dst][0]; primary.Metric != want {
					t.Fatalf("round %d: %s to %s costs %d, the oracle says %d", round, src, prefix, primary.Metric, want)
				}
				alternative := oracleCost(g, src, primary.Via)
				backup, has := backups[prefix]
				_, possible := alternative[dst]
				if has != possible {
					t.Fatalf("round %d: %s to %s: backup present=%v, possible=%v", round, src, prefix, has, possible)
				}
				if has && backup.Metric != alternative[dst][0] {
					t.Fatalf("round %d: %s to %s: backup costs %d, the oracle says %d", round, src, prefix, backup.Metric, alternative[dst][0])
				}
			}
		}
	}
}

// TestHopByHopForwardingDelivers checks, on 400 random networks, that the
// tables of different nodes agree with each other. Starting at any node and
// looking up a destination, then moving to the next hop and looking up again,
// reaches the owner without visiting a node twice and without a hop over a
// down link. Each node computes its own table, so this is the property that
// shows they are consistent.
func TestHopByHopForwardingDelivers(t *testing.T) {
	rng := rand.New(rand.NewPCG(37, 3))
	for round := 0; round < 400; round++ {
		g, owned, owner := largeRandomNetwork(rng)
		result := mustCompute(t, g, route.Input{Owned: owned})
		for _, src := range g.Nodes() {
			for prefix, holder := range owner {
				if holder == src || !hasActive(result.Tables[src], prefix) {
					continue
				}
				dst := prefix.Addr()
				at, seen := src, map[types.NodeID]bool{}
				for at != holder {
					if seen[at] {
						t.Fatalf("round %d: %s to %s loops at %s", round, src, dst, at)
					}
					seen[at] = true
					hop, ok := result.Tables[at].Lookup(dst)
					if !ok {
						t.Fatalf("round %d: %s to %s is dropped at %s", round, src, dst, at)
					}
					if edge, up := upEdge(g, at, hop.NextHop); !up || edge.Link != hop.Via {
						t.Fatalf("round %d: %s to %s leaves %s over %s, which is not a working link to %s", round, src, dst, at, hop.Via, hop.NextHop)
					}
					at = hop.NextHop
				}
			}
		}
	}
}

// hasActive reports whether a table has an active route for exactly prefix.
func hasActive(table *route.Table, prefix netip.Prefix) bool {
	for _, r := range table.Routes {
		if r.Prefix == prefix && r.Active {
			return true
		}
	}
	return false
}

// P1-ROUTE-033
//
// TestDeterminismOnLargeRandomNetworks checks 40 random networks, each computed
// 50 times with its nodes and edges, and the order of its prefixes, shuffled.
// All 50 results must be identical.
func TestDeterminismOnLargeRandomNetworks(t *testing.T) {
	rng := rand.New(rand.NewPCG(37, 4))
	for round := 0; round < 40; round++ {
		g, owned, _ := largeRandomNetwork(rng)
		want := mustCompute(t, g, route.Input{Owned: owned})
		for run := 0; run < 50; run++ {
			shuffled := route.Owned{}
			for node, prefixes := range owned {
				copyOf := append([]netip.Prefix(nil), prefixes...)
				rng.Shuffle(len(copyOf), func(i, j int) { copyOf[i], copyOf[j] = copyOf[j], copyOf[i] })
				shuffled[node] = copyOf
			}
			got := mustCompute(t, g.Shuffled(rng), route.Input{Owned: shuffled})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d, run %d: a shuffled copy gave a different result", round, run)
			}
		}
	}
}
