package tests

import (
	"slices"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// TestSimulateGivesTheSameReportAsRunScenario checks Simulate over tables the engine made
// for the failed network is the report RunScenario makes for the same failure: they are
// one code path.
func TestSimulateGivesTheSameReportAsRunScenario(t *testing.T) {
	in := threeSiteScenarioInput(t)
	scenario := sim.Scenario{Name: "direct-down", LinksDown: []types.LinkID{"builder-1~dgx-spark"}}
	want, err := sim.RunScenario(in, scenario)
	if err != nil {
		t.Fatal(err)
	}
	down := sim.Degrade(in.Graph, scenario.LinksDown, nil)
	computed, err := route.Compute(down, in.Input)
	if err != nil {
		t.Fatal(err)
	}
	got := sim.Simulate(down, computed.Tables, in.Input, computed.Unreachable, in.Flows)
	a, _ := got.JSON()
	b, _ := want.Report.JSON()
	if string(a) != string(b) {
		t.Errorf("reports differ:\n%s\n%s", a, b)
	}
}

// TestSimulateSeesAFaultInTheTables checks a fault planted in the tables shows, which is
// the point of simulating tables as given: a route removed is a black hole, and the
// graph says nothing different.
func TestSimulateSeesAFaultInTheTables(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	in := fixture.ThreeSiteInput()
	flows := []sim.Flow{overlayFlow("builder-1", "dgx-spark")}
	if !sim.Simulate(p.graph, p.tables, in, nil, flows).Passed {
		t.Fatal("the healthy tables fail")
	}
	dropRoutes(p.tables, "builder-1", "10.42.240.3/32")
	report := sim.Simulate(p.graph, p.tables, in, nil, flows)
	if report.Passed || report.Summary.Count(sim.VerdictBlackHole) != 1 {
		t.Errorf("a missing route: %+v", report.Summary.Verdicts)
	}
}

// TestSimulateJudgesCutOffPairsAsCutOff checks the unreachable pairs turn a black hole
// into NO_HEALTHY_PATH, which does not fail the gate.
func TestSimulateJudgesCutOffPairsAsCutOff(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	dropRoutes(p.tables, "builder-1", "10.42.240.3/32")
	flows := []sim.Flow{overlayFlow("builder-1", "dgx-spark")}
	cut := []route.Pair{{From: "builder-1", To: "dgx-spark"}}
	report := sim.Simulate(p.graph, p.tables, fixture.ThreeSiteInput(), cut, flows)
	if !report.Passed || report.Summary.Count(sim.VerdictNoHealthyPath) != 1 {
		t.Errorf("%+v", report.Summary.Verdicts)
	}
}

// TestSimulateWithNoFlowsPasses checks an empty set of flows is an empty, passing report.
func TestSimulateWithNoFlowsPasses(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	if r := sim.Simulate(p.graph, p.tables, fixture.ThreeSiteInput(), nil, nil); !r.Passed || r.Summary.Total != 0 {
		t.Errorf("%+v", r)
	}
}

// TestDegradeMarksLinksAndNodesAndLeavesTheGraphAlone checks the failed links, and every
// edge to or from a failed node, are down in the degraded graph and not in the original.
func TestDegradeMarksLinksAndNodesAndLeavesTheGraphAlone(t *testing.T) {
	g := fixture.ThreeSite(true)
	d := sim.Degrade(g, []types.LinkID{"builder-1~dgx-spark"}, []types.NodeID{"aws-hub"})
	down := func(graph route.Graph, node types.NodeID) []types.LinkID {
		var out []types.LinkID
		for _, e := range graph.Edges(node) {
			if e.Down {
				out = append(out, e.Link)
			}
		}
		slices.Sort(out)
		return out
	}
	if got := down(d, "builder-1"); !slices.Equal(got, []types.LinkID{"aws-hub~builder-1", "builder-1~dgx-spark"}) {
		t.Errorf("builder-1 down edges %v", got)
	}
	if got := down(d, "aws-hub"); len(got) != 2 {
		t.Errorf("the failed hub's edges: %v", got)
	}
	for _, node := range g.Nodes() {
		if got := down(g, node); len(got) != 0 {
			t.Errorf("the original graph changed at %s: %v", node, got)
		}
	}
	if len(d.Nodes()) != 3 || d.Role("aws-hub") != types.RoleHub {
		t.Error("the failed node must stay in the graph with its role")
	}
}
