package tests

import (
	"slices"
	"testing"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// threeSiteHealth returns an evaluator for the three-site network: the direct link
// is required, so it must be proven, and the two hub-spoke links are assumed.
func threeSiteHealth(t *testing.T) *health.Evaluator {
	t.Helper()
	var links []*health.Link
	for _, spec := range []struct {
		a, b     types.NodeID
		required bool
	}{{b1, dgx, true}, {"aws-hub", b1, false}, {"aws-hub", dgx, false}} {
		l, err := health.NewLink(types.LinkIDOf(spec.a, spec.b), spec.a, spec.b, spec.required)
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, l)
	}
	e, err := health.NewEvaluator(cfg, links...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestHealthDrivesTheSimulator is the whole idea in one test: evidence goes into
// the health package, the links it says are unusable go to the simulator as a
// scenario, the real routing engine recomputes, and every flow still works, over
// the hub. Then the link recovers and the direct path comes back.
func TestHealthDrivesTheSimulator(t *testing.T) {
	e := threeSiteHealth(t)
	apply(t, e, probes(true, 5, sec(5)), sec(30))
	if got := e.UnusableLinks(); len(got) != 0 {
		t.Fatalf("with the direct link proven, unusable links = %v, want none", got)
	}
	apply(t, e, probes(false, 3, sec(35)), sec(50))
	unusable := e.UnusableLinks()
	if !slices.Equal(unusable, []types.LinkID{direct}) {
		t.Fatalf("with the direct link down, unusable links = %v, want just it", unusable)
	}

	topo := fixture.ThreeSiteTopology()
	plan, err := fixture.ThreeSitePlan()
	if err != nil {
		t.Fatal(err)
	}
	flows, err := sim.Flows(topo, plan, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	in := sim.ScenarioInput{Graph: fixture.ThreeSite(true), Input: fixture.ThreeSiteInput(), Flows: flows}
	result, err := sim.RunScenario(in, sim.Scenario{Name: "health says direct is down", LinksDown: unusable})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.Report.Summary.Count(sim.VerdictOK) != 13 {
		t.Fatalf("failed over: passed %v, %+v", result.Passed, result.Report.Summary.Verdicts)
	}
	for _, r := range result.Report.Flows {
		if r.Flow.Label == "builder-1→dgx-spark (overlay)" && r.Forward.Crossings() != 2 {
			t.Errorf("builder-1 to dgx-spark crossed %d links, want 2: by way of the hub", r.Forward.Crossings())
		}
	}

	apply(t, e, probes(true, 5, sec(110)), sec(131))
	want(t, e, health.Healthy)
	if got := e.UnusableLinks(); len(got) != 0 {
		t.Errorf("after recovery unusable links = %v, want none", got)
	}
}
