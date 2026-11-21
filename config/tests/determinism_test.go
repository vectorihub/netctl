package tests

import (
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// shuffledInputs returns a copy of the inputs with the topology's nodes, links,
// sites and endpoints put in a random order. The tables are shared; see
// withShuffledTables.
func shuffledInputs(in config.Inputs, rng *rand.Rand) config.Inputs {
	topo := *in.Topology
	topo.Nodes = slices.Clone(topo.Nodes)
	topo.Links = slices.Clone(topo.Links)
	topo.Sites = slices.Clone(topo.Sites)
	rng.Shuffle(len(topo.Nodes), func(i, j int) { topo.Nodes[i], topo.Nodes[j] = topo.Nodes[j], topo.Nodes[i] })
	rng.Shuffle(len(topo.Links), func(i, j int) { topo.Links[i], topo.Links[j] = topo.Links[j], topo.Links[i] })
	rng.Shuffle(len(topo.Sites), func(i, j int) { topo.Sites[i], topo.Sites[j] = topo.Sites[j], topo.Sites[i] })
	for i := range topo.Nodes {
		topo.Nodes[i].Endpoints = slices.Clone(topo.Nodes[i].Endpoints)
		rng.Shuffle(len(topo.Nodes[i].Endpoints), func(a, b int) {
			topo.Nodes[i].Endpoints[a], topo.Nodes[i].Endpoints[b] = topo.Nodes[i].Endpoints[b], topo.Nodes[i].Endpoints[a]
		})
	}
	in.Topology = &topo
	return in
}

// withShuffledTables returns the inputs with a copy of every table whose routes are
// in a random order.
func withShuffledTables(in config.Inputs, original route.Tables, rng *rand.Rand) config.Inputs {
	in.Tables = route.Tables{}
	for node, table := range original {
		routes := slices.Clone(table.Routes)
		rng.Shuffle(len(routes), func(i, j int) { routes[i], routes[j] = routes[j], routes[i] })
		in.Tables[node] = &route.Table{Node: table.Node, Routes: routes}
	}
	return in
}

// hashes returns every node's hash and the plan ID, as one comparable list.
func hashes(configs []*config.NodeConfig) []string {
	var out []string
	for _, c := range configs {
		out = append(out, string(c.Node)+" "+c.Hash+" "+c.PlanID)
	}
	return out
}

// P1-CFG-010
//
// TestGenerateIsReproducible generates the three-site plan fifty times with the
// inputs in a different random order each time and a different number of processors,
// and checks every hash and the plan ID come out the same. It does the same for random
// networks, healthy and failed over.
func TestGenerateIsReproducible(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(0))
	procs := []int{1, 2, 4, 8}
	for _, up := range []bool{true, false} {
		base := threeSiteInputs(t, up)
		want, err := config.Generate(base)
		if err != nil {
			t.Fatal(err)
		}
		wantHashes := hashes(want)
		rng := rand.New(rand.NewPCG(61, 1))
		for i := 0; i < 50; i++ {
			runtime.GOMAXPROCS(procs[i%len(procs)])
			in := withShuffledTables(shuffledInputs(base, rng), base.Tables, rng)
			got, err := config.Generate(in)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(hashes(got), wantHashes) {
				t.Fatalf("directUp=%v run %d (GOMAXPROCS %d): hashes differ:\n%v\n%v", up, i, procs[i%len(procs)], hashes(got), wantHashes)
			}
		}
	}
	for seed := 0; seed < 30; seed++ {
		base := randomInputs(t, seed)
		want, err := config.Generate(base)
		if err != nil {
			t.Fatal(err)
		}
		rng := rand.New(rand.NewPCG(62, uint64(seed)))
		for i := 0; i < 5; i++ {
			runtime.GOMAXPROCS(procs[i%len(procs)])
			in := withShuffledTables(shuffledInputs(base, rng), base.Tables, rng)
			got, err := config.Generate(in)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(hashes(got), hashes(want)) {
				t.Fatalf("seed %d run %d: hashes differ", seed, i)
			}
		}
	}
}

// TestGenerateDoesNotChangeItsInputs checks the topology and tables are left as they
// were: Generate sorts its own copies.
func TestGenerateDoesNotChangeItsInputs(t *testing.T) {
	in := threeSiteInputs(t, true)
	links := slices.Clone(in.Topology.Links)
	nodes := slices.Clone(in.Topology.Nodes)
	routes := map[types.NodeID][]route.Route{}
	for node, table := range in.Tables {
		routes[node] = slices.Clone(table.Routes)
	}
	slices.Reverse(in.Topology.Links)
	slices.Reverse(links)
	if _, err := config.Generate(in); err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(in.Topology.Links, links, func(a, b topology.Link) bool { return a == b }) || len(in.Topology.Nodes) != len(nodes) {
		t.Error("Generate reordered the topology")
	}
	for node, table := range in.Tables {
		if !slices.EqualFunc(table.Routes, routes[node], func(a, b route.Route) bool { return route.Compare(a, b) == 0 }) {
			t.Errorf("Generate changed the table of %s", node)
		}
	}
}

// TestConfigsSurviveAWriteAndRead checks every config of 100 random networks, written
// and read back, writes the same bytes again and still verifies: nothing in the
// canonical form is lost or invented by Decode.
func TestConfigsSurviveAWriteAndRead(t *testing.T) {
	for seed := 0; seed < 100; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			first, err := c.Clone().Encode()
			if err != nil {
				t.Fatal(err)
			}
			back, err := config.Decode(first)
			if err != nil {
				t.Fatalf("seed %d: %s: %v", seed, c.Node, err)
			}
			second, err := back.Encode()
			if err != nil || string(first) != string(second) {
				t.Fatalf("seed %d: %s: the bytes changed over a write and a read (%v)", seed, c.Node, err)
			}
			if err := back.Verify(); err != nil {
				t.Fatalf("seed %d: %s: %v", seed, c.Node, err)
			}
		}
	}
}

// TestFailoverAndComputeGiveTheSameConfigs joins the two halves: the tables that
// failover.Evaluate installs after the direct link fails give the same configs, byte
// for byte, as tables computed straight from a network with that link down. So a
// config generated from a failover is the config of the failed-over network, and
// nothing about how the tables were reached shows in it.
func TestFailoverAndComputeGiveTheSameConfigs(t *testing.T) {
	healthyInputs := threeSiteInputs(t, true)
	net := failover.Network{Graph: fixture.ThreeSite(true), Input: fixture.ThreeSiteInput(), Required: map[types.LinkID]bool{direct: true}}
	at := func(n int) time.Time { return time.Date(2026, 11, 21, 10, 0, n, 0, time.UTC) }
	probes := func(ok bool, n, from int) []health.Signal {
		var out []health.Signal
		for i := 0; i < n; i++ {
			for _, end := range []types.NodeID{builder, dgx} {
				out = append(out, health.Signal{Link: direct, From: end, Kind: health.SignalProbe, OK: ok, At: at(from + 5*i)})
			}
		}
		return out
	}
	first, err := failover.Evaluate(net, failover.State{}, probes(true, 5, 5), at(30))
	if err != nil {
		t.Fatal(err)
	}
	healthy := healthyInputs
	healthy.Tables = first.Tables
	fromFailover, err := config.Generate(healthy)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hashes(fromFailover), hashes(mustGenerate(t, threeSiteInputs(t, true)))) {
		t.Error("the healthy plan from failover differs from the one from route.Compute")
	}
	failed, err := failover.Evaluate(net, first.State, probes(false, 3, 35), at(50))
	if err != nil {
		t.Fatal(err)
	}
	down := threeSiteInputs(t, false)
	down.Tables = failed.Tables
	fromFailover, err = config.Generate(down)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hashes(fromFailover), hashes(mustGenerate(t, threeSiteInputs(t, false)))) {
		t.Error("the failed-over plan from failover differs from the one from route.Compute")
	}
}

// mustGenerate generates configs and fails the test on an error.
func mustGenerate(t *testing.T, in config.Inputs) []*config.NodeConfig {
	t.Helper()
	configs, err := config.Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	return configs
}
