package tests

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// bigTopology writes a valid topology of nodes machines and about 3*nodes links
// as YAML: 4 hubs, the rest spokes, each spoke on two hubs and one direct link.
func bigTopology(nodes int) string {
	const hubs = 4
	var b strings.Builder
	b.WriteString("schema: beaver.topology/v1alpha1\nname: big\nsites:\n")
	for s := 0; s < 50; s++ {
		fmt.Fprintf(&b, "  - id: site-%02d\n", s)
	}
	b.WriteString("nodes:\n")
	for n := 0; n < nodes; n++ {
		role := "spoke"
		if n < hubs {
			role = "hub"
		}
		fmt.Fprintf(&b, "  - {id: n-%04d, site: site-%02d, role: %s, nat: none, labels: {tier: t%d}}\n", n, n%50, role, n%7)
	}
	b.WriteString("links:\n")
	for n := hubs; n < nodes; n++ {
		fmt.Fprintf(&b, "  - {kind: hub-spoke, a: n-%04d, b: n-%04d}\n", n%hubs, n)
		fmt.Fprintf(&b, "  - {kind: hub-spoke, a: n-%04d, b: n-%04d}\n", (n+1)%hubs, n)
		if n+1 < nodes {
			fmt.Fprintf(&b, "  - {kind: direct, a: n-%04d, b: n-%04d}\n", n, n+1)
		}
	}
	b.WriteString("intents:\n  - allow: {from: \"*\", to: \"label:tier=t1\"}\n  - deny: {from: \"site:site-03\", to: \"site:site-04\", ports: [22, 443]}\n")
	return b.String()
}

// TestBigTopologyIsValid checks that the generated 500-node topology really is valid, so the
// benchmark measures a successful load.
func TestBigTopologyIsValid(t *testing.T) {
	topo, report, err := topology.Load("big.yaml", []byte(bigTopology(500)), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Spokes joined to each other are both behind no NAT, so nothing warns.
	if len(topo.Nodes) != 500 || len(topo.Links) < 1450 || len(report.Findings) != 0 {
		t.Fatalf("%d nodes, %d links, findings %v", len(topo.Nodes), len(topo.Links), report.Findings)
	}
}

// P1-TOPO-041: loading 500 nodes and about 1,500 links takes under 300 ms.
// "Load" is what a caller does: decode, normalise, check. Run with: make bench
func BenchmarkLoad500Nodes(b *testing.B) {
	data := []byte(bigTopology(500))
	best := time.Duration(1<<63 - 1)
	for b.Loop() {
		start := time.Now()
		topo, _, err := topology.Load("big.yaml", data, 0)
		elapsed := time.Since(start)
		if err != nil || len(topo.Links) < 1450 {
			b.Fatalf("%v", err)
		}
		best = min(best, elapsed)
	}
	// Judge the best run, so a busy machine does not fail a correct implementation.
	if best > 300*time.Millisecond {
		b.Fatalf("loading 500 nodes took %s at best, want under 300ms", best)
	}
}

// The graph and the hash on the same input stay fast too; they run on every plan.
func BenchmarkGraphAndHash500Nodes(b *testing.B) {
	topo, _, err := topology.Load("big.yaml", []byte(bigTopology(500)), 0)
	if err != nil {
		b.Fatal(err)
	}
	best := time.Duration(1<<63 - 1)
	for b.Loop() {
		start := time.Now()
		graph := topology.BuildGraph(topo, func(types.LinkID) bool { return true })
		_ = topo.Hash()
		best = min(best, time.Since(start))
		if graph.EdgeCount() != 2*len(topo.Links) {
			b.Fatal(graph.EdgeCount())
		}
	}
	if best > 100*time.Millisecond {
		b.Fatalf("graph and hash took %s at best, want under 100ms", best)
	}
}

// FuzzLoad is P1-TOPO-040. Whatever bytes arrive: no panic; every failure is a
// coded INVALID_TOPOLOGY; and whatever loads cleanly hashes the same on a second
// load, builds a graph with two edges per sound link, and passes Check again.
func FuzzLoad(f *testing.F) {
	for _, name := range []string{"three-site.yaml", "three-site-typo.yaml", "adjacency.yaml", "adjacency-one-sided.yaml", "five-faults.yaml"} {
		f.Add(fixtureBytes(name))
	}
	f.Add([]byte(laughs(9, 10)))
	f.Add([]byte("---\n---\n"))
	f.Add([]byte("schema: beaver.topology/v1alpha1\nsites: [{id: a}]\nnodes: [{id: n, site: a, role: spoke, peers: [n]}]\n"))
	f.Add([]byte("schema: beaver.topology/v1alpha1\nsites: [{id: a}]\nnodes: [{id: h, site: a, role: hub, peers: [s]}, {id: s, site: a, role: spoke, peers: [h]}]\nintents: [{allow: {from: '*', to: 'site:a'}}]\n"))
	f.Add([]byte(bigTopology(12)))
	f.Fuzz(func(t *testing.T, data []byte) {
		topo, report, err := topology.Load("fuzz.yaml", data, 1<<20)
		if err != nil {
			if !types.HasCode(err, types.ErrInvalidTopology) {
				t.Fatalf("error without INVALID_TOPOLOGY: %v", err)
			}
			return
		}
		if topo == nil || len(report.Errors()) != 0 {
			t.Fatalf("no error but topology %v, errors %v", topo, report.Errors())
		}
		again, _, err := topology.Load("fuzz.yaml", data, 1<<20)
		if err != nil || again.Hash() != topo.Hash() {
			t.Fatalf("second load differs: %v", err)
		}
		if graph := topology.BuildGraph(topo, nil); graph.EdgeCount() != 2*len(topo.Links) {
			t.Fatalf("%d edges for %d links", graph.EdgeCount(), len(topo.Links))
		}
		if len(topo.Check().Errors()) != 0 {
			t.Fatal("Check disagrees with itself")
		}
		for _, intent := range topo.Intents {
			if len(intent.From.Resolve(topo)) == 0 || len(intent.To.Resolve(topo)) == 0 {
				t.Fatal("a clean topology has an intent that picks nothing")
			}
		}
	})
}
