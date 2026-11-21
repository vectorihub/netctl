package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// The nodes and links of the three-site network.
const (
	hub     = types.NodeID("aws-hub")
	builder = types.NodeID("builder-1")
	dgx     = types.NodeID("dgx-spark")
	direct  = types.LinkID("builder-1~dgx-spark")
)

// t0 is the instant the plan's clock stands at.
var t0 = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// clock returns a clock standing at t0.
func clock() types.Clock { return &types.FixedClock{T: t0} }

// at returns the instant n seconds after t0.
func at(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }

// fresh returns the inputs of a first plan of the three-site network: an empty address
// plan with the standard pools, and no health.
func fresh(t testing.TB) plan.Inputs {
	t.Helper()
	registry, err := fixture.NewPlan()
	if err != nil {
		t.Fatal(err)
	}
	return plan.Inputs{Topology: fixture.ThreeSiteTopology(), IPAM: registry.Snapshot(), Clock: clock()}
}

// mustPlan plans and fails the test on an error.
func mustPlan(t testing.TB, in plan.Inputs) plan.Result {
	t.Helper()
	result, err := plan.Plan(in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return result
}

// evaluator returns a health evaluator for the three-site links, with the direct link
// proven Healthy (up) or Down (not up), and the hub links assumed.
func evaluator(t testing.TB, up bool) *health.Evaluator {
	t.Helper()
	topo := fixture.ThreeSiteTopology()
	var links []*health.Link
	for _, l := range topo.Links {
		link, err := health.NewLink(types.LinkIDOf(l.A, l.B), l.A, l.B, l.Kind == types.LinkDirect)
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, link)
	}
	e, err := health.NewEvaluator(health.DefaultConfig(), links...)
	if err != nil {
		t.Fatal(err)
	}
	probes := func(ok bool, n, from int) []health.Signal {
		var out []health.Signal
		for i := 0; i < n; i++ {
			for _, end := range []types.NodeID{builder, dgx} {
				out = append(out, health.Signal{Link: direct, From: end, Kind: health.SignalProbe, OK: ok, At: at(from + 5*i)})
			}
		}
		return out
	}
	e.Apply(probes(true, 5, 5), at(30))
	if !up {
		e.Apply(probes(false, 3, 35), at(50))
	}
	return e
}

// golden reads a reviewed config file.
func golden(t testing.TB, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "config", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// withAddressing returns the three-site topology with an inline address plan, for a plan
// that starts from the topology alone.
func withAddressing(t testing.TB) *topology.Topology {
	t.Helper()
	topo := fixture.ThreeSiteTopology()
	registry, err := fixture.NewPlan()
	if err != nil {
		t.Fatal(err)
	}
	s := registry.Snapshot()
	topo.Addressing.Supernet = s.Supernet
	for _, p := range s.Pools {
		topo.Addressing.Pools = append(topo.Addressing.Pools, topology.AddressPool{Name: string(p.Name), Prefix: p.Prefix, ChildBits: p.ChildBits, Reserved: p.Reserved, SkipFirst: p.SkipFirst})
	}
	return topo
}

// healthLink returns a new required link between the two ends of the ID.
func healthLink(id types.LinkID) (*health.Link, error) {
	a, b, _ := strings.Cut(string(id), "~")
	return health.NewLink(id, types.NodeID(a), types.NodeID(b), true)
}

// nodeID converts text to a node ID.
func nodeID(text string) types.NodeID { return types.NodeID(text) }
