package phase1

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/topology"
)

// init registers the edge-case golden tests and the lab A4 convergence test.
func init() {
	register("P1-GOLD-005", goldTenSpokes)
	register("P1-GOLD-006", goldSpokeChain)
	register("P1-GOLD-007", goldSingleNode)
	register("P1-GOLD-008", goldTwoHubs)
	register("P1-GOLD-009", goldStable)
	register("P1-GOLD-010", goldLabA4)
}

// planFixture copies the address plan and one fixture of testdata/e2e into the workspace,
// loads it and plans it from an empty address plan.
func planFixture(w *e2e.W, name string) (*topology.Topology, *e2e.Out) {
	w.T.Helper()
	topo := setupLab(w, name)
	w.Step("plan " + name)
	return topo, w.MustPlan(e2e.Options{Topology: topo})
}

// spokePairs returns every ordered pair of distinct spokes of a plan.
func spokePairs(out *e2e.Out) [][2]string {
	var spokes []string
	for _, n := range out.Nodes() {
		if n != "aws-hub" {
			spokes = append(spokes, n)
		}
	}
	var pairs [][2]string
	for _, a := range spokes {
		for _, b := range spokes {
			if a != b {
				pairs = append(pairs, [2]string{a, b})
			}
		}
	}
	return pairs
}

// goldTenSpokes is P1-GOLD-005: a hub with ten spokes. The output equals the golden
// directory, the simulation passes, and all ninety ordered spoke pairs are delivered through
// the hub, in both directions, on the exact reverse path.
func goldTenSpokes(w *e2e.W) {
	_, out := planFixture(w, "ten-spoke")
	w.GoldenDir(out, "ten-spoke")
	w.ExpectSimPass(out, -1)
	w.ExpectOracleOK(out)
	pairs, err := out.Oracle()
	if err != nil {
		w.Fatalf("oracle: %v", err)
	}
	n := 0
	for _, pair := range spokePairs(out) {
		hops := pairs[pair].Forward.Hops
		if len(hops) != 3 || hops[1] != "aws-hub" {
			w.Fatalf("%s -> %s goes %v, want through the hub", pair[0], pair[1], hops)
		}
		n++
	}
	if n != 90 {
		w.Fatalf("checked %d spoke pairs, want 90", n)
	}
}

// goldSpokeChain is P1-GOLD-006: spokes chained by direct links. With the links unproven and
// again with all of them proven healthy, no pair goes through a third spoke: every node in
// the middle of a path is the hub. The output equals the golden directory.
func goldSpokeChain(w *e2e.W) {
	topo, fresh := planFixture(w, "spoke-chain")
	w.GoldenDir(fresh, "spoke-chain")
	signals := new(e2e.Signals)
	for _, link := range []string{"spoke-1~spoke-2", "spoke-2~spoke-3", "spoke-3~spoke-4"} {
		signals.Probes(link, true, 5, 5*time.Second, 5*time.Second)
	}
	w.At(30 * time.Second)
	w.Step("prove the chain links healthy")
	healthy := w.MustPlan(e2e.Options{Topology: topo, From: fresh, Signals: signals.List(), Name: "out-1"})
	w.GoldenDir(healthy, "spoke-chain-healthy")
	for _, out := range []*e2e.Out{fresh, healthy} {
		w.ExpectOracleOK(out)
		pairs, _ := out.Oracle()
		for _, pair := range spokePairs(out) {
			hops := pairs[pair].Forward.Hops
			for _, mid := range hops[1 : len(hops)-1] {
				if mid != "aws-hub" {
					w.Fatalf("%s: %s -> %s goes %v, through the spoke %s", out.Name, pair[0], pair[1], hops, mid)
				}
			}
		}
	}
	if hops := w.OraclePath(healthy, "spoke-1", "spoke-2"); len(hops.Hops) != 2 {
		w.Fatalf("neighbours on a healthy chain go %v, want the direct link", hops.Hops)
	}
}

// goldSingleNode is P1-GOLD-007: one node. It has one config, no peers and no routes beyond
// its own connected prefixes.
func goldSingleNode(w *e2e.W) {
	_, out := planFixture(w, "single-node")
	w.GoldenDir(out, "single-node")
	if got := out.Nodes(); len(got) != 1 {
		w.Fatalf("configs for %v, want one", got)
	}
	cfg := w.JSON(out, "nodes/aws-hub.json")
	if cfg.Len("peers") != 0 || cfg.Len("routes") != 0 {
		w.Fatalf("a lone node has %d peers and %d routes, want none", cfg.Len("peers"), cfg.Len("routes"))
	}
}

// goldTwoHubs is P1-GOLD-008: two hubs, a Phase 5 preview. The network loads and plans, and
// the validator warns that multiple hubs are not optimised (V-TOPO-020).
func goldTwoHubs(w *e2e.W) {
	_, out := planFixture(w, "two-hubs")
	w.ExpectFinding(out, "V-TOPO-020", "warning", "hubs")
	w.ExpectNoErrors(out)
	w.ExpectSimPass(out, -1)
}

// goldStable is P1-GOLD-009: two consecutive runs of the same plan write the same bytes, and
// those bytes are the reviewed golden files, so running the suite twice leaves git clean.
func goldStable(w *e2e.W) {
	var hashes [2]string
	for i := range hashes {
		x := e2e.New(w.T, w.ID)
		_, out := planFixture(x, "ten-spoke")
		hashes[i] = x.DirHash(out.Name)
	}
	if hashes[0] != hashes[1] {
		w.Fatalf("two runs of the same plan wrote different bytes")
	}
}

// pathsText writes the oracle's forward path between every ordered pair of nodes, one per
// line, sorted: the final converged paths a routing protocol would settle on.
func pathsText(out *e2e.Out) []byte {
	pairs, err := out.Oracle()
	if err != nil {
		return []byte(err.Error())
	}
	keys := make([][2]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return slices.Compare(keys[i][:], keys[j][:]) < 0
	})
	var b strings.Builder
	for _, k := range keys {
		p := pairs[k].Forward
		if p.Delivered {
			fmt.Fprintf(&b, "%s -> %s: %s\n", k[0], k[1], strings.Join(p.Hops, " "))
		} else {
			fmt.Fprintf(&b, "%s -> %s: undelivered (%s)\n", k[0], k[1], p.Problem)
		}
	}
	return []byte(b.String())
}

// goldLabA4 is P1-GOLD-010: lab A4, OSPF. netctl computes paths statically, so only the final
// converged paths are compared, in three states: every link up, one direct link failed, two
// failed. Each state's paths equal a golden file, and every pair stays delivered because the
// hub is always a way round.
func goldLabA4(w *e2e.W) {
	topo := setupLab(w, "lab-a4")
	links := []string{"builder-1~dgx-spark", "dgx-spark~mac-studio", "builder-1~mac-studio"}
	up := new(e2e.Signals)
	for _, l := range links {
		up.Probes(l, true, 5, 5*time.Second, 5*time.Second)
	}
	w.At(30 * time.Second)
	w.Step("all links up")
	fresh := w.MustPlan(e2e.Options{Topology: topo, Name: "out-0"})
	allUp := w.MustPlan(e2e.Options{Topology: topo, From: fresh, Signals: up.List(), Name: "out-1"})
	w.Golden("paths-all-up.txt", pathsText(allUp))

	w.Step("one link fails")
	w.At(50 * time.Second)
	one := w.MustPlan(e2e.Options{Topology: topo, From: allUp, Signals: new(e2e.Signals).Probes(links[0], false, 3, 35*time.Second, 5*time.Second).List(), Name: "out-2"})
	w.Golden("paths-one-failed.txt", pathsText(one))

	w.Step("a second link fails")
	w.At(70 * time.Second)
	two := w.MustPlan(e2e.Options{Topology: topo, From: one, Signals: new(e2e.Signals).Probes(links[1], false, 3, 55*time.Second, 5*time.Second).List(), Name: "out-3"})
	w.Golden("paths-two-failed.txt", pathsText(two))

	for _, out := range []*e2e.Out{allUp, one, two} {
		w.ExpectOracleOK(out)
	}
	obs, have := observation(w, "lab-a4")
	matchesLab(w, allUp, topo, obs, have, "all-up")
	matchesLab(w, one, topo, obs, have, "one-failed")
	matchesLab(w, two, topo, obs, have, "two-failed")
}
