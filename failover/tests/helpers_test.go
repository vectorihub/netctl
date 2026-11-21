package tests

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// t0 is the instant every timeline starts from, fixed so a test replays the same
// seconds on every run.
var t0 = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// at returns the instant n seconds after t0.
func at(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }

// The links and nodes of the three-site network.
const (
	direct  = types.LinkID("builder-1~dgx-spark")
	hubB    = types.LinkID("aws-hub~builder-1")
	hubD    = types.LinkID("aws-hub~dgx-spark")
	hub     = types.NodeID("aws-hub")
	builder = types.NodeID("builder-1")
	dgx     = types.NodeID("dgx-spark")
)

// ends names the two nodes of each link of the three-site network.
var ends = map[types.LinkID][2]types.NodeID{direct: {builder, dgx}, hubB: {hub, builder}, hubD: {hub, dgx}}

// probes returns n probe signals on the link from both of its ends (or only the
// ends in only), one every 5 seconds from start seconds, good or not.
func probes(link types.LinkID, ok bool, n, start int, only ...types.NodeID) []health.Signal {
	pair := ends[link]
	nodes := pair[:]
	if len(only) > 0 {
		nodes = only
	}
	var out []health.Signal
	for i := 0; i < n; i++ {
		for _, node := range nodes {
			out = append(out, health.Signal{Link: link, From: node, Kind: health.SignalProbe, OK: ok, At: at(start + 5*i)})
		}
	}
	return out
}

// join concatenates lists of signals.
func join(lists ...[]health.Signal) []health.Signal {
	var out []health.Signal
	for _, list := range lists {
		out = append(out, list...)
	}
	return out
}

// threeSite returns the three-site network: the direct link is required and the
// hub links are assumed, and the guard simulates every flow of the plan.
func threeSite(t *testing.T) failover.Network {
	t.Helper()
	plan, err := fixture.ThreeSitePlan()
	if err != nil {
		t.Fatal(err)
	}
	flows, err := sim.Flows(fixture.ThreeSiteTopology(), plan, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return failover.Network{
		Graph: fixture.ThreeSite(true), Input: fixture.ThreeSiteInput(),
		Required: map[types.LinkID]bool{direct: true}, Flows: flows,
	}
}

// evaluate runs one evaluation and fails the test on an error.
func evaluate(t *testing.T, net failover.Network, state failover.State, signals []health.Signal, now int) failover.Result {
	t.Helper()
	result, err := failover.Evaluate(net, state, signals, at(now))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return result
}

// healthy returns the state of the three-site network with the direct link
// proven: five good probes from each end, evaluated at 30 seconds.
func healthy(t *testing.T, net failover.Network) failover.Result {
	t.Helper()
	result := evaluate(t, net, failover.State{}, probes(direct, true, 5, 5), 30)
	if l, _ := result.State.Link(direct); l.State != health.Healthy {
		t.Fatalf("the direct link is %s, want Healthy", l.State)
	}
	return result
}

// down returns the three-site network after the direct link failed: three bad
// probes from each end at 35, 40 and 45 seconds, evaluated at 50.
func down(t *testing.T, net failover.Network, before failover.State) failover.Result {
	t.Helper()
	return evaluate(t, net, before, probes(direct, false, 3, 35), 50)
}

// linkState returns the health of the link in the state.
func linkState(t *testing.T, s failover.State, id types.LinkID) health.LinkHealth {
	t.Helper()
	l, ok := s.Link(id)
	if !ok {
		t.Fatalf("the state holds no link %s", id)
	}
	return l.State
}

// counting wraps route.Compute and counts the calls.
type counting struct {
	// calls is how many times the wrapper was called.
	calls int
}

// compute is a sim.ComputeFunc that runs route.Compute and counts.
func (c *counting) compute(graph route.Graph, input route.Input) (route.Result, error) {
	c.calls++
	return route.Compute(graph, input)
}

// shuffled returns the signals in a random order from a seeded generator.
func shuffled(rng *rand.Rand, signals []health.Signal) []health.Signal {
	out := append([]health.Signal(nil), signals...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
