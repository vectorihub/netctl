package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// threeSiteScenarioInput returns what a scenario run needs for the three-site
// network: the healthy graph with the direct link up, the routing input, and
// the 16 derived flows.
func threeSiteScenarioInput(t *testing.T) sim.ScenarioInput {
	t.Helper()
	topo, plan := threeSite(t)
	return sim.ScenarioInput{
		Graph: fixture.ThreeSite(true),
		Input: fixture.ThreeSiteInput(),
		Flows: mustFlows(t, topo, plan, sim.Options{}),
	}
}

// resultFor returns the result of the flow with the given label, or stops the
// test.
func resultFor(t *testing.T, report sim.Report, label string) sim.FlowResult {
	t.Helper()
	for _, result := range report.Flows {
		if result.Flow.Label == label {
			return result
		}
	}
	t.Fatalf("no flow labelled %q in the report", label)
	return sim.FlowResult{}
}

// P1-SIM-040
//
// TestScenarioDirectLinkDown fails the direct link and checks that every flow is
// still OK, now by way of the hub both ways, and symmetric: the engine recomputed
// the tables with the link out.
func TestScenarioDirectLinkDown(t *testing.T) {
	in := threeSiteScenarioInput(t)
	result, err := sim.RunScenario(in, sim.Scenario{Name: "direct-link-down", LinksDown: []types.LinkID{"builder-1~dgx-spark"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.Report.Summary.Count(sim.VerdictOK) != 13 || result.Report.Summary.Count(sim.VerdictUnreachableExpected) != 3 {
		t.Fatalf("passed %v, verdicts %+v; want 13 OK and 3 UNREACHABLE_EXPECTED", result.Passed, result.Report.Summary.Verdicts)
	}
	flow := resultFor(t, result.Report, "builder-1→dgx-spark (overlay)")
	want := []types.NodeID{"builder-1", "aws-hub", "dgx-spark"}
	if got := flow.Forward.Nodes(); !slices.Equal(got, want) {
		t.Errorf("forward path %v, want %v", got, want)
	}
	if flow.Return == nil || !flow.Symmetric || flow.Verdict != sim.VerdictOK {
		t.Errorf("return %v, symmetric %v, verdict %s; want a symmetric OK", flow.Return, flow.Symmetric, flow.Verdict)
	}
	if len(result.Unreachable) != 0 {
		t.Errorf("unreachable pairs %v, want none: the hub still joins everyone", result.Unreachable)
	}
}

// TestScenarioBaselineChangesNothing runs the scenario with no failures and
// checks the direct link is used and everything passes.
func TestScenarioBaselineChangesNothing(t *testing.T) {
	in := threeSiteScenarioInput(t)
	result, err := sim.RunScenario(in, sim.Scenario{Name: "baseline"})
	if err != nil {
		t.Fatal(err)
	}
	flow := resultFor(t, result.Report, "builder-1→dgx-spark (overlay)")
	if !result.Passed || len(flow.Forward.Nodes()) != 2 {
		t.Errorf("passed %v, path %v; want a pass over the direct link", result.Passed, flow.Forward.Nodes())
	}
}

// P1-SIM-041
//
// TestScenarioHubDown fails the hub of the three-site network: builder-1 and
// dgx-spark still reach each other over their direct link, and every flow to or
// from the hub is UNREACHABLE_EXPECTED, the hub being the thing that failed.
func TestScenarioHubDown(t *testing.T) {
	in := threeSiteScenarioInput(t)
	result, err := sim.RunScenario(in, sim.Scenario{
		Name: "hub-down", NodesDown: []types.NodeID{"aws-hub"},
		Expect: []sim.Expectation{
			{From: "builder-1", To: "dgx-spark", Verdict: sim.VerdictOK},
			{From: "builder-1", To: "aws-hub", Verdict: sim.VerdictUnreachableExpected},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || len(result.Mismatches) != 0 {
		t.Fatalf("passed %v, mismatches %+v", result.Passed, result.Mismatches)
	}
	flow := resultFor(t, result.Report, "builder-1→dgx-spark (overlay)")
	if got := flow.Forward.Nodes(); len(got) != 2 {
		t.Errorf("forward path %v, want the direct link", got)
	}
	for _, r := range result.Report.Flows {
		touchesHub := r.Flow.From == "aws-hub" || r.Flow.To == "aws-hub"
		if touchesHub && r.Verdict != sim.VerdictUnreachableExpected {
			t.Errorf("%s = %s, want UNREACHABLE_EXPECTED: it involves the failed hub", r.Flow.Label, r.Verdict)
		}
	}
}

// TestScenarioHubDownListsCutOffSpokes uses a network with a third spoke that has
// no direct link: with the hub down it is cut off from the other two, and those
// flows are NO_HEALTHY_PATH (they pass the gate) and the pairs are listed.
func TestScenarioHubDownListsCutOffSpokes(t *testing.T) {
	g := fixture.NewGraph().Hub("hub").Spoke("a", "b", "c")
	g.Link("hub", "a", 20)
	g.Link("hub", "b", 20)
	g.Link("hub", "c", 20)
	g.Link("a", "b", 10)
	addrs := map[types.NodeID]netip.Addr{"hub": addrOf("10.0.0.1"), "a": addrOf("10.0.0.2"), "b": addrOf("10.0.0.3"), "c": addrOf("10.0.0.4")}
	owned := route.Owned{}
	for node, addr := range addrs {
		owned[node] = []netip.Prefix{netip.PrefixFrom(addr, 32)}
	}
	var flows []sim.Flow
	for from, src := range addrs {
		for to, dst := range addrs {
			if from != to {
				flows = append(flows, sim.Flow{Label: string(from) + "→" + string(to), Family: sim.FamilyOverlay, Src: src, Dst: dst, From: from, To: to})
			}
		}
	}
	result, err := sim.RunScenario(sim.ScenarioInput{Graph: g, Input: route.Input{Owned: owned}, Flows: flows},
		sim.Scenario{Name: "hub-down", NodesDown: []types.NodeID{"hub"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"a→c", "c→a", "b→c", "c→b"} {
		if got := resultFor(t, result.Report, label).Verdict; got != sim.VerdictNoHealthyPath {
			t.Errorf("%s = %s, want NO_HEALTHY_PATH", label, got)
		}
	}
	if got := resultFor(t, result.Report, "a→b").Verdict; got != sim.VerdictOK {
		t.Errorf("a→b = %s, want OK over the direct link", got)
	}
	listed := map[[2]types.NodeID]bool{}
	for _, pair := range result.Unreachable {
		listed[[2]types.NodeID{pair.From, pair.To}] = true
	}
	for _, pair := range [][2]types.NodeID{{"a", "c"}, {"c", "a"}, {"b", "c"}, {"c", "b"}} {
		if !listed[pair] {
			t.Errorf("cut-off pair %v is not listed in Unreachable: %v", pair, result.Unreachable)
		}
	}
	if !result.Passed {
		t.Errorf("a network cut off by real failures should pass the gate; verdicts %+v", result.Report.Summary.Verdicts)
	}
}

// P1-SIM-042
//
// TestScenarioWrongExpectationFails asserts a verdict that is not true and checks
// the scenario fails and names the flow that disagreed.
func TestScenarioWrongExpectationFails(t *testing.T) {
	in := threeSiteScenarioInput(t)
	result, err := sim.RunScenario(in, sim.Scenario{
		Name: "wrong", LinksDown: []types.LinkID{"builder-1~dgx-spark"},
		Expect: []sim.Expectation{{From: "builder-1", To: "dgx-spark", Verdict: sim.VerdictBlackHole}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.Mismatches) != 1 {
		t.Fatalf("passed %v with %d mismatches, want a failure with one", result.Passed, len(result.Mismatches))
	}
	m := result.Mismatches[0]
	if m.Flow != "builder-1→dgx-spark (overlay)" || m.Got != sim.VerdictOK || !strings.Contains(m.Detail, "builder-1→dgx-spark (overlay)") {
		t.Errorf("mismatch %+v does not name the flow and its actual verdict", m)
	}
	if !result.Report.Passed {
		t.Error("the report itself passed; only the expectation should have failed")
	}
}

// TestScenarioExpectationMatchingNothingFails checks an expectation about a pair
// that has no flow is a mismatch, so a typo cannot pass by asserting nothing.
func TestScenarioExpectationMatchingNothingFails(t *testing.T) {
	in := threeSiteScenarioInput(t)
	result, err := sim.RunScenario(in, sim.Scenario{
		Name:   "typo",
		Expect: []sim.Expectation{{From: "builder-1", To: "dgx-spak", Verdict: sim.VerdictOK}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.Mismatches) != 1 || result.Mismatches[0].Flow != "" {
		t.Errorf("passed %v, mismatches %+v; want one mismatch that names no flow", result.Passed, result.Mismatches)
	}
}

// TestScenarioExpectationFamily checks an expectation can pick another family of
// flow, and that a negative flow is matched by an empty To.
func TestScenarioExpectationFamily(t *testing.T) {
	in := threeSiteScenarioInput(t)
	result, err := sim.RunScenario(in, sim.Scenario{
		Name: "families",
		Expect: []sim.Expectation{
			{From: "builder-1", To: "dgx-spark", Family: sim.FamilySite, Verdict: sim.VerdictOK},
			{From: "builder-1", Family: sim.FamilyNegative, Verdict: sim.VerdictUnreachableExpected},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Errorf("mismatches %+v", result.Mismatches)
	}
}

// countingCompute wraps route.Compute with a call counter, the spy of
// P1-SIM-043.
type countingCompute struct {
	// calls is how many times the wrapper was called.
	calls int
}

// compute counts the call and runs the real route.Compute.
func (c *countingCompute) compute(g route.Graph, in route.Input) (route.Result, error) {
	c.calls++
	return route.Compute(g, in)
}

// P1-SIM-043
//
// TestScenarioCallsComputeOncePerScenario spies on the routing computation and
// checks the runner calls it exactly once for each scenario, never zero times (a
// cached or invented answer) and never twice.
func TestScenarioCallsComputeOncePerScenario(t *testing.T) {
	in := threeSiteScenarioInput(t)
	spy := &countingCompute{}
	in.Compute = spy.compute
	scenarios := []sim.Scenario{
		{Name: "baseline"},
		{Name: "direct-link-down", LinksDown: []types.LinkID{"builder-1~dgx-spark"}},
		{Name: "hub-down", NodesDown: []types.NodeID{"aws-hub"}},
	}
	for i, sc := range scenarios {
		if _, err := sim.RunScenario(in, sc); err != nil {
			t.Fatal(err)
		}
		if spy.calls != i+1 {
			t.Fatalf("after %d scenarios Compute was called %d times, want %d", i+1, spy.calls, i+1)
		}
	}
	spy.calls = 0
	if _, err := sim.RunScenarios(in, scenarios); err != nil {
		t.Fatal(err)
	}
	if spy.calls != len(scenarios) {
		t.Errorf("RunScenarios called Compute %d times for %d scenarios", spy.calls, len(scenarios))
	}
}

// TestScenarioUsesTheRealComputeByDefault checks that with no Compute given the
// runner still recomputes: the failed link really changes the path.
func TestScenarioUsesTheRealComputeByDefault(t *testing.T) {
	in := threeSiteScenarioInput(t)
	up, err := sim.RunScenario(in, sim.Scenario{Name: "up"})
	if err != nil {
		t.Fatal(err)
	}
	down, err := sim.RunScenario(in, sim.Scenario{Name: "down", LinksDown: []types.LinkID{"builder-1~dgx-spark"}})
	if err != nil {
		t.Fatal(err)
	}
	label := "builder-1→dgx-spark (overlay)"
	if a, b := len(resultFor(t, up.Report, label).Forward.Nodes()), len(resultFor(t, down.Report, label).Forward.Nodes()); a != 2 || b != 3 {
		t.Errorf("path lengths up %d, down %d; want 2 and 3", a, b)
	}
}

// TestScenarioDoesNotChangeTheNetwork runs a scenario and checks the base graph
// is untouched: the next run on the same input sees the healthy network again.
func TestScenarioDoesNotChangeTheNetwork(t *testing.T) {
	in := threeSiteScenarioInput(t)
	if _, err := sim.RunScenario(in, sim.Scenario{Name: "hub-down", NodesDown: []types.NodeID{"aws-hub"}}); err != nil {
		t.Fatal(err)
	}
	for _, node := range in.Graph.Nodes() {
		for _, edge := range in.Graph.Edges(node) {
			if edge.Down {
				t.Errorf("edge %s from %s was left down by a scenario", edge.Link, node)
			}
		}
	}
	result, err := sim.RunScenario(in, sim.Scenario{Name: "baseline"})
	if err != nil || !result.Passed {
		t.Errorf("baseline after a scenario: %v, passed %v", err, result.Passed)
	}
}

// TestScenarioRefusesUnknownNames checks a scenario naming a link or node the
// network does not have is refused with INVALID_SCENARIO, every bad name at once.
func TestScenarioRefusesUnknownNames(t *testing.T) {
	in := threeSiteScenarioInput(t)
	_, err := sim.RunScenario(in, sim.Scenario{Name: "typo", LinksDown: []types.LinkID{"builder-1~dgx-spak"}, NodesDown: []types.NodeID{"aws-hubb"}})
	if !types.HasCode(err, types.ErrInvalidScenario) {
		t.Fatalf("error %v, want INVALID_SCENARIO", err)
	}
	for _, name := range []string{"builder-1~dgx-spak", "aws-hubb"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not mention %s: %v", name, err)
		}
	}
	if _, err := sim.RunScenario(in, sim.Scenario{}); !types.HasCode(err, types.ErrInvalidScenario) {
		t.Errorf("a scenario with no name: %v, want INVALID_SCENARIO", err)
	}
	if _, err := sim.RunScenario(sim.ScenarioInput{}, sim.Scenario{Name: "x"}); !types.HasCode(err, types.ErrInvalidScenario) {
		t.Errorf("no graph: %v, want INVALID_SCENARIO", err)
	}
}

// TestRunScenariosRefusesDuplicateNames checks two scenarios with one name are
// refused before any of them runs.
func TestRunScenariosRefusesDuplicateNames(t *testing.T) {
	in := threeSiteScenarioInput(t)
	spy := &countingCompute{}
	in.Compute = spy.compute
	_, err := sim.RunScenarios(in, []sim.Scenario{{Name: "a"}, {Name: "a"}})
	if !types.HasCode(err, types.ErrInvalidScenario) || spy.calls != 0 {
		t.Errorf("error %v after %d computations, want INVALID_SCENARIO and none", err, spy.calls)
	}
}

// TestScenarioReportsComputeFailure checks an error from the routing engine is
// returned, not swallowed into an empty report.
func TestScenarioReportsComputeFailure(t *testing.T) {
	in := threeSiteScenarioInput(t)
	in.Input.Owned["builder-1"] = append(in.Input.Owned["builder-1"], in.Input.Owned["dgx-spark"][0])
	if _, err := sim.RunScenario(in, sim.Scenario{Name: "overlap"}); !types.HasCode(err, types.ErrOverlap) {
		t.Errorf("error %v, want OVERLAP from the engine", err)
	}
}

// pdfScenarios is the example scenario document of the engineering plan.
const pdfScenarios = `
scenarios:
  - name: baseline
  - name: direct-link-down
    links_down: [builder-1~dgx-spark]
  - name: hub-down
    nodes_down: [aws-hub]
    expect:
      - { from: builder-1, to: dgx-spark, verdict: OK }
      - { from: builder-1, to: aws-hub, verdict: UNREACHABLE_EXPECTED }
`

// TestParseScenariosPlanExample reads the plan's example document and checks every
// field, then runs it: all three scenarios pass on the three-site network.
func TestParseScenariosPlanExample(t *testing.T) {
	scenarios, err := sim.ParseScenarios([]byte(pdfScenarios))
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 3 || scenarios[0].Name != "baseline" || scenarios[1].LinksDown[0] != "builder-1~dgx-spark" ||
		scenarios[2].NodesDown[0] != "aws-hub" || len(scenarios[2].Expect) != 2 ||
		scenarios[2].Expect[1] != (sim.Expectation{From: "builder-1", To: "aws-hub", Verdict: sim.VerdictUnreachableExpected}) {
		t.Fatalf("parsed %+v", scenarios)
	}
	results, err := sim.RunScenarios(threeSiteScenarioInput(t), scenarios)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("scenario %s failed: %+v", r.Name, r.Mismatches)
		}
	}
}

// TestParseScenariosFamily checks the optional family key is read.
func TestParseScenariosFamily(t *testing.T) {
	scenarios, err := sim.ParseScenarios([]byte("scenarios:\n  - name: s\n    expect:\n      - {from: a, family: negative, verdict: UNREACHABLE_EXPECTED}\n"))
	if err != nil || scenarios[0].Expect[0].Family != sim.FamilyNegative {
		t.Errorf("parsed %+v, error %v", scenarios, err)
	}
}

// TestParseScenariosRefusals checks every malformed document is refused with
// INVALID_SCENARIO.
func TestParseScenariosRefusals(t *testing.T) {
	cases := map[string]string{
		"empty document":     "",
		"unknown key":        "scenarios:\n  - name: a\n    link_down: [x]\n",
		"unknown verdict":    "scenarios:\n  - name: a\n    expect:\n      - {from: x, to: y, verdict: FINE}\n",
		"unknown family":     "scenarios:\n  - name: a\n    expect:\n      - {from: x, to: y, family: odd, verdict: OK}\n",
		"missing verdict":    "scenarios:\n  - name: a\n    expect:\n      - {from: x, to: y}\n",
		"missing from":       "scenarios:\n  - name: a\n    expect:\n      - {to: y, verdict: OK}\n",
		"missing name":       "scenarios:\n  - links_down: [x]\n",
		"duplicate name":     "scenarios:\n  - name: a\n  - name: a\n",
		"second document":    "scenarios: []\n---\nscenarios: []\n",
		"not a mapping":      "- a\n- b\n",
		"scenarios a scalar": "scenarios: 3\n",
	}
	for name, text := range cases {
		if _, err := sim.ParseScenarios([]byte(text)); !types.HasCode(err, types.ErrInvalidScenario) {
			t.Errorf("%s: error %v, want INVALID_SCENARIO", name, err)
		}
	}
	if got, err := sim.ParseScenarios([]byte("scenarios: []\n")); err != nil || len(got) != 0 {
		t.Errorf("an empty list: %v, %v; want no scenarios and no error", got, err)
	}
}

// TestNoSecondRouteComputation scans the simulator's own source and checks the
// routing computation is called in exactly one place, the scenario runner, and
// that nothing else in the package computes routes: no call to SPF, Collect or
// Diff.
func TestNoSecondRouteComputation(t *testing.T) {
	files, err := filepath.Glob("../*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("finding the simulator's source: %v (%d files)", err, len(files))
	}
	computeUses := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			selector, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "route" {
				switch selector.Sel.Name {
				case "Compute":
					computeUses++
					if filepath.Base(file) != "scenario.go" {
						t.Errorf("%s uses route.Compute; only the scenario runner may", file)
					}
				case "SPF", "Collect", "Diff":
					t.Errorf("%s uses route.%s: the simulator must not compute routes", file, selector.Sel.Name)
				}
			}
			return true
		})
	}
	if computeUses != 1 {
		t.Errorf("route.Compute is referenced %d times in the simulator, want exactly 1", computeUses)
	}
}

// FuzzParseScenarios feeds arbitrary bytes to ParseScenarios. It must never
// panic; it must either refuse the input with INVALID_SCENARIO or return
// scenarios that each have a name, with no name used twice and every
// expectation holding a from node and a verdict.
func FuzzParseScenarios(f *testing.F) {
	f.Add([]byte(pdfScenarios))
	f.Add([]byte("scenarios: []\n"))
	f.Add([]byte("scenarios:\n  - name: a\n    links_down: [x~y]\n"))
	f.Add([]byte("scenarios:\n  - name: a\n  - name: a\n"))
	f.Add([]byte("- not\n- a mapping\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		scenarios, err := sim.ParseScenarios(data)
		if err != nil {
			if !types.HasCode(err, types.ErrInvalidScenario) {
				t.Fatalf("error without the INVALID_SCENARIO code: %v", err)
			}
			return
		}
		seen := map[string]bool{}
		for _, sc := range scenarios {
			if sc.Name == "" || seen[sc.Name] {
				t.Fatalf("accepted a scenario with name %q (seen before: %v)", sc.Name, seen[sc.Name])
			}
			seen[sc.Name] = true
			for _, want := range sc.Expect {
				if want.From == "" || want.Verdict == sim.VerdictUnknown {
					t.Fatalf("accepted an incomplete expectation %+v", want)
				}
			}
		}
	})
}
