package phase1

import (
	"math/rand/v2"
	"os"
	"slices"
	"time"

	"github.com/vectorihub/netctl"
	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/internal/topogen"
	"github.com/vectorihub/netctl/topology"
)

// init registers Group 6, scale: planning stays fast at hundreds of nodes. The tests are
// skipped under -short. Limits are checked here on whatever machine runs them; the gate
// records the CI runner's numbers.
func init() {
	registerSlow("P1-E2E-100", e2e100Nodes100)
	registerSlow("P1-E2E-101", e2e101Nodes500)
	registerSlow("P1-E2E-102", e2e102Failover500)
}

// within fails the test when a plan took longer than the guide's limit, but only when
// E2E_STRICT_TIMING is set, as it is on the CI runner and at the gate. On a laptop the parallel
// tests and anything else running share the CPU, so the time is logged instead.
func within(w *e2e.W, out *e2e.Out, limit time.Duration) {
	w.T.Helper()
	w.T.Logf("%s took %v (limit %v)", out.Name, out.Took, limit)
	if os.Getenv("E2E_STRICT_TIMING") != "" && out.Took > limit {
		w.Fatalf("%s took %v, limit %v", out.Name, out.Took, limit)
	}
}

// scaled plans a generated network fresh and again with every direct link proven healthy,
// returning the topology, the two plans and the links.
func scaled(w *e2e.W, spec topogen.Spec) (*topology.Topology, *e2e.Out, *e2e.Out, []string) {
	w.T.Helper()
	topo := topogen.Generate(spec)
	links := topogen.DirectLinks(topo)
	w.Step("fresh plan")
	fresh := w.MustPlan(e2e.Options{Topology: topo, Name: "out-0"})
	proof := new(e2e.Signals)
	for _, l := range links {
		proof.Probes(l, true, 5, 5*time.Second, 5*time.Second)
	}
	w.Step("prove every direct link healthy")
	w.At(30 * time.Second)
	base := w.MustPlan(e2e.Options{Topology: topo, From: fresh, Signals: proof.List(), Name: "out-1"})
	return topo, fresh, base, links
}

// e2e100Nodes100 is P1-E2E-100: 100 nodes with 30 direct links. Each plan takes under two
// seconds, the simulation passes, the oracle delivers every pair and the IPAM checker agrees.
func e2e100Nodes100(w *e2e.W) {
	_, fresh, base, _ := scaled(w, topogen.Spec{Nodes: 100, Hubs: 1, Direct: 30, NATRatio: 0.6, Seed: 1})
	for _, out := range []*e2e.Out{fresh, base} {
		within(w, out, 2*time.Second)
		w.ExpectSimPass(out, -1)
		w.ExpectIPAMOK(out)
	}
	w.ExpectOracleOK(base)
}

// e2e101Nodes500 is P1-E2E-101: 500 nodes with 200 direct links. Each plan takes under 15
// seconds and the simulation passes; the oracle walks a sample of 2,000 pairs.
func e2e101Nodes500(w *e2e.W) {
	_, fresh, base, _ := scaled(w, topogen.Spec{Nodes: 500, Hubs: 1, Direct: 200, NATRatio: 0.6, Seed: 1})
	for _, out := range []*e2e.Out{fresh, base} {
		within(w, out, 15*time.Second)
		w.ExpectSimPass(out, -1)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	nodes := base.Nodes()
	var pairs [][2]string
	for len(pairs) < 2000 {
		a, b := nodes[rng.IntN(len(nodes))], nodes[rng.IntN(len(nodes))]
		if a != b {
			pairs = append(pairs, [2]string{a, b})
		}
	}
	got, err := base.OracleSample(pairs)
	if err != nil {
		w.Fatalf("oracle: %v", err)
	}
	for k, p := range got {
		if !p.Forward.Delivered || !p.Return.Delivered || !p.Symmetric {
			w.Fatalf("%s -> %s: forward %q return %q symmetric %v", k[0], k[1], p.Forward.Problem, p.Return.Problem, p.Symmetric)
		}
	}
}

// e2e102Failover500 is P1-E2E-102: ten direct links of the 500-node network fail at once. The
// plan takes under 15 seconds, only the endpoints of those links change (at most twenty
// nodes), the hub is unchanged and the simulation passes.
func e2e102Failover500(w *e2e.W) {
	topo, _, base, links := scaled(w, topogen.Spec{Nodes: 500, Hubs: 1, Direct: 200, NATRatio: 0.6, Seed: 1})
	rng := rand.New(rand.NewPCG(7, 7))
	picked := slices.Clone(links)
	rng.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
	picked = picked[:10]
	fail := new(e2e.Signals)
	for _, l := range picked {
		fail.Probes(l, false, 3, 35*time.Second, 5*time.Second)
	}
	w.Step("ten direct links fail")
	w.At(50 * time.Second)
	out := w.MustPlan(e2e.Options{Topology: topo, From: base, Signals: fail.List(), Name: "out-2"})
	within(w, out, 15*time.Second)
	ends := map[string]bool{}
	for _, l := range picked {
		for _, e := range splitLink(l) {
			ends[e] = true
		}
	}
	d := netctl.Diff(base.Result, out.Result)
	if len(d.Nodes) == 0 || len(d.Nodes) > 20 {
		w.Fatalf("%d nodes changed, want between 1 and 20", len(d.Nodes))
	}
	for _, n := range d.Nodes {
		if !ends[string(n.Node)] {
			w.Fatalf("%s changed but is not the end of a failed link", n.Node)
		}
	}
	expectSameConfig(w, base, out, "n000")
	w.ExpectSimPass(out, -1)
}

// splitLink returns the two node IDs of a link ID.
func splitLink(id string) []string {
	for i := 0; i < len(id); i++ {
		if id[i] == '~' {
			return []string{id[:i], id[i+1:]}
		}
	}
	return []string{id}
}
