package sim

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// Expectation is one assertion inside a scenario: the flow from one node to
// another must get this verdict. It turns a scenario from a report into a test.
//
// Example:
//
//	Expectation{From: "builder-1", To: "dgx-spark", Verdict: VerdictOK} asserts the overlay
//	flow from builder-1 to dgx-spark is OK.
type Expectation struct {
	// From is the node the flow is injected at.
	From types.NodeID `yaml:"from" json:"from"`
	// To is the node that holds the destination address. Leave it empty to
	// match a negative flow, which has no destination node.
	To types.NodeID `yaml:"to,omitempty" json:"to,omitempty"`
	// Family says which kind of flow is meant. The zero value means overlay,
	// the node-to-node flow, because that is what "can A reach B" asks about.
	Family Family `yaml:"family,omitempty" json:"family,omitempty"`
	// Verdict is the verdict the flow must get.
	Verdict Verdict `yaml:"verdict" json:"verdict"`
}

// Scenario is a what-if: the same flows, run with some links or nodes failed.
//
// A scenario never changes the network. It describes health, and the runner
// applies it to the graph, recomputes every route through route.Compute, and
// simulates the flows over the new tables.
//
// Example:
//
//	Scenario{Name: "direct-link-down", LinksDown: []types.LinkID{"builder-1~dgx-spark"}}
//	fails the direct link.
type Scenario struct {
	// Name identifies the scenario in reports. It must be unique in a set.
	Name string `yaml:"name" json:"name"`
	// LinksDown lists links that cannot carry traffic.
	LinksDown []types.LinkID `yaml:"links_down,omitempty" json:"links_down,omitempty"`
	// NodesDown lists nodes that have failed. Every link of a failed node is
	// down, and a flow that starts or ends at one is expected to be undeliverable.
	NodesDown []types.NodeID `yaml:"nodes_down,omitempty" json:"nodes_down,omitempty"`
	// Expect lists what must still hold in this scenario.
	Expect []Expectation `yaml:"expect,omitempty" json:"expect,omitempty"`
}

// ComputeFunc is the signature of route.Compute. A scenario runner calls one of
// these exactly once per scenario; tests pass a counting wrapper around the real
// function to prove it.
//
// Example:
//
//	route.Compute is a ComputeFunc; a test passes a wrapper that counts calls.
type ComputeFunc func(graph route.Graph, input route.Input) (route.Result, error)

// ScenarioInput is everything a scenario run needs besides the scenario.
//
// Example:
//
//	ScenarioInput{Graph: g, Input: in, Flows: flows} runs scenarios on graph g with the
//	routing input in and the given flows, using route.Compute.
type ScenarioInput struct {
	// Graph is the network with every link as it is when healthy (or as it is
	// already marked). Scenarios fail links on top of it.
	Graph route.Graph
	// Input is what the routing engine needs: who owns which prefixes.
	Input route.Input
	// Flows is what to simulate, usually from Flows.
	Flows []Flow
	// Compute is the routing computation. A nil Compute means route.Compute,
	// which is what a real run uses; the field exists so a test can watch the
	// call. It is not a way to substitute a different routing algorithm.
	Compute ComputeFunc
}

// Mismatch is an expectation a scenario did not meet.
//
// Example:
//
//	an expectation of OK for a flow that was BLACK_HOLE gives a Mismatch whose Detail
//	says "expected OK, got BLACK_HOLE" and names the flow.
type Mismatch struct {
	// Expect is the expectation that failed.
	Expect Expectation `json:"expect"`
	// Flow is the label of the flow that got the wrong verdict, or empty if no
	// flow matched the expectation at all.
	Flow string `json:"flow,omitempty"`
	// Got is the verdict the flow got. It is unset when no flow matched.
	Got Verdict `json:"got,omitempty"`
	// Detail says what went wrong in words.
	Detail string `json:"detail"`
}

// ScenarioResult is the outcome of one scenario.
//
// Example:
//
//	the result of the scenario "hub-down" has Name "hub-down", a Report of every flow,
//	the Unreachable pairs, and Passed true only if no flow fails and every expectation holds.
type ScenarioResult struct {
	// Name is the scenario's name.
	Name string `json:"name"`
	// Report is every flow's result under the scenario.
	Report Report `json:"report"`
	// Unreachable lists the pairs of nodes the routing engine found no path
	// between, so a reader can see what the scenario cut off.
	Unreachable []route.Pair `json:"unreachable"`
	// Mismatches lists the expectations that were not met.
	Mismatches []Mismatch `json:"mismatches"`
	// Passed is true if no flow fails the gate and every expectation is met.
	Passed bool `json:"passed"`
}

// degraded is a graph with scenario failures laid over it.
type degraded struct {
	// base is the healthy network.
	base route.Graph
	// links and nodes are the failed links and nodes.
	links map[types.LinkID]bool
	nodes map[types.NodeID]bool
}

// Nodes returns the base graph's nodes: a failed node is still in the network.
func (g degraded) Nodes() []types.NodeID { return g.base.Nodes() }

// Role returns the base graph's role for the node.
func (g degraded) Role(node types.NodeID) types.Role { return g.base.Role(node) }

// Edges returns the node's edges with those that cannot carry traffic marked
// down: an edge already down, an edge of a failed link, and any edge that
// leaves or enters a failed node.
func (g degraded) Edges(node types.NodeID) []route.Edge {
	edges := g.base.Edges(node)
	out := make([]route.Edge, len(edges))
	for i, edge := range edges {
		edge.Down = edge.Down || g.links[edge.Link] || g.nodes[node] || g.nodes[edge.To]
		out[i] = edge
	}
	return out
}

// Degrade returns the graph with the given links and nodes failed: every edge of a
// failed link, and every edge that leaves or enters a failed node, is marked down on
// top of what the graph already says. The graph itself is not changed, and the failed
// nodes stay in it (a failed node is still in the network). It does not check the
// names; RunScenario does.
//
// Example:
//
//	sim.Degrade(graph, []types.LinkID{"builder-1~dgx-spark"}, nil) is the three-site
//	network with its direct link down.
func Degrade(graph route.Graph, links []types.LinkID, nodes []types.NodeID) route.Graph {
	g := degraded{base: graph, links: map[types.LinkID]bool{}, nodes: map[types.NodeID]bool{}}
	for _, link := range links {
		g.links[link] = true
	}
	for _, node := range nodes {
		g.nodes[node] = true
	}
	return g
}

// Simulate sends every flow through the network given by the tables, over the graph,
// and reports. It computes nothing: the tables are taken as they are, so a fault planted
// in them, or a failover that did not happen, shows in the report. unreachable lists the
// pairs the routing engine found no path between (its Result.Unreachable), so that a flow
// between two cut-off nodes is judged NO_HEALTHY_PATH and not a black hole.
//
// Example:
//
//	report := sim.Simulate(graph, result.Tables, input, result.Unreachable, flows)
//	if !report.Passed { ... }
func Simulate(graph route.Graph, tables route.Tables, in route.Input, unreachable []route.Pair, flows []Flow) Report {
	net := NewNet(graph, tables, in.Owned, in.Local)
	cutOff := map[[2]types.NodeID]bool{}
	for _, pair := range unreachable {
		cutOff[[2]types.NodeID{pair.From, pair.To}] = true
	}
	results := make([]FlowResult, 0, len(flows))
	for _, flow := range flows {
		results = append(results, net.Run(flow, cutOff[[2]types.NodeID{flow.From, flow.To}]))
	}
	return NewReport(results)
}

// check returns an error naming every link and node of the scenario that the
// graph does not have, or nil.
func (sc Scenario) check(graph route.Graph) error { return sc.problems(graph).Err() }

// problems lists everything wrong with the scenario on graph: a missing name,
// and every link and node it names that the graph does not have.
func (sc Scenario) problems(graph route.Graph) types.List {
	var problems types.List
	if sc.Name == "" {
		problems.Add(types.Errf(types.ErrInvalidScenario, "scenario", "it has no name"))
	}
	nodes := map[types.NodeID]bool{}
	links := map[types.LinkID]bool{}
	for _, node := range graph.Nodes() {
		nodes[node] = true
		for _, edge := range graph.Edges(node) {
			links[edge.Link] = true
		}
	}
	for _, link := range sc.LinksDown {
		if !links[link] {
			problems.Add(types.Errf(types.ErrInvalidScenario, "scenario "+sc.Name, "links_down names %s, which is not a link of the network", link))
		}
	}
	for _, node := range sc.NodesDown {
		if !nodes[node] {
			problems.Add(types.Errf(types.ErrInvalidScenario, "scenario "+sc.Name, "nodes_down names %s, which is not a node of the network", node))
		}
	}
	return problems
}

// RunScenario runs one scenario: it fails the links and nodes the scenario names,
// recomputes every route with route.Compute (exactly one call, never a shortcut
// or a second implementation), simulates every flow over the new tables, and
// checks the expectations.
//
// The scenario's failed links and nodes are the health evaluation: the runner
// takes health as a given and applies it to the graph, and the recomputation is
// the real one. A flow that starts or ends at a failed node is judged as one
// that must not be delivered, so it is UNREACHABLE_EXPECTED when it is dropped
// for want of a route. A flow between live nodes that the engine reports cut off
// is NO_HEALTHY_PATH, which passes the gate and is listed in
// ScenarioResult.Unreachable.
//
// A scenario that names a link or node the network does not have is refused with
// INVALID_SCENARIO, listing every such name, because a typo would otherwise
// quietly test the healthy network.
//
// Example:
//
//	on the three-site network, the scenario "direct-link-down" with
//	links_down [builder-1~dgx-spark] gives every flow OK, the builder and DGX
//	Spark paths now crossing aws-hub.
func RunScenario(in ScenarioInput, sc Scenario) (ScenarioResult, error) {
	if in.Graph == nil {
		return ScenarioResult{}, types.Errf(types.ErrInvalidScenario, "scenario "+sc.Name, "there is no network to run it on")
	}
	if err := sc.check(in.Graph); err != nil {
		return ScenarioResult{}, err
	}
	g := Degrade(in.Graph, sc.LinksDown, sc.NodesDown).(degraded)
	compute := in.Compute
	if compute == nil {
		compute = route.Compute
	}
	computed, err := compute(g, in.Input)
	if err != nil {
		return ScenarioResult{}, fmt.Errorf("sim: scenario %s: recomputing routes: %w", sc.Name, err)
	}
	net := NewNet(g, computed.Tables, in.Input.Owned, in.Input.Local)
	cutOff := map[[2]types.NodeID]bool{}
	for _, pair := range computed.Unreachable {
		cutOff[[2]types.NodeID{pair.From, pair.To}] = true
	}
	results := make([]FlowResult, 0, len(in.Flows))
	for _, flow := range in.Flows {
		failed := g.nodes[flow.From] || (flow.To != "" && g.nodes[flow.To])
		results = append(results, net.runCase(flow, Case{
			Negative:        flow.Negative() || failed,
			Partitioned:     !failed && cutOff[[2]types.NodeID{flow.From, flow.To}],
			AllowAsymmetric: flow.AllowAsymmetric,
		}))
	}
	report := NewReport(results)
	mismatches := check(report, sc.Expect)
	unreachable := computed.Unreachable
	if unreachable == nil {
		unreachable = []route.Pair{}
	}
	return ScenarioResult{
		Name: sc.Name, Report: report, Unreachable: unreachable, Mismatches: mismatches,
		Passed: report.Passed && len(mismatches) == 0,
	}, nil
}

// check compares a report with the expectations and returns the ones that fail.
// An expectation covers every flow with its from, to and family; one that
// matches no flow is itself a mismatch, so a typo cannot pass by asserting
// nothing.
func check(report Report, expects []Expectation) []Mismatch {
	mismatches := []Mismatch{}
	for _, want := range expects {
		family := want.Family
		if family == FamilyUnknown {
			family = FamilyOverlay
		}
		matched := false
		for _, result := range report.Flows {
			flow := result.Flow
			if flow.From != want.From || flow.To != want.To || flow.Family != family {
				continue
			}
			matched = true
			if result.Verdict != want.Verdict {
				mismatches = append(mismatches, Mismatch{
					Expect: want, Flow: flow.Label, Got: result.Verdict,
					Detail: fmt.Sprintf("%s: expected %s, got %s", flow.Label, want.Verdict, result.Verdict),
				})
			}
		}
		if !matched {
			mismatches = append(mismatches, Mismatch{
				Expect: want,
				Detail: fmt.Sprintf("no %s flow from %s to %s to check", family, want.From, want.To),
			})
		}
	}
	return mismatches
}

// RunScenarios runs each scenario in order, one route computation each, and
// returns the results in the same order. Every scenario is checked before any
// runs, and every problem is reported at once as INVALID_SCENARIO: two
// scenarios with the same name, a scenario with no name, and every link or node
// a scenario names that the network does not have, as is a missing network.
//
// Example:
//
//	the set baseline, direct-link-down and hub-down gives three results.
func RunScenarios(in ScenarioInput, scenarios []Scenario) ([]ScenarioResult, error) {
	if in.Graph == nil {
		return nil, types.Errf(types.ErrInvalidScenario, "scenarios", "there is no network to run them on")
	}
	seen := map[string]bool{}
	var problems types.List
	for _, sc := range scenarios {
		if seen[sc.Name] {
			problems.Add(types.Errf(types.ErrInvalidScenario, "scenario "+sc.Name, "the name is used more than once"))
		}
		seen[sc.Name] = true
		problems = append(problems, sc.problems(in.Graph)...)
	}
	if err := problems.Err(); err != nil {
		return nil, err
	}
	results := make([]ScenarioResult, 0, len(scenarios))
	for _, sc := range scenarios {
		result, err := RunScenario(in, sc)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// scenarioFile is the shape of a scenarios document.
type scenarioFile struct {
	// Scenarios is the list of scenarios.
	Scenarios []Scenario `yaml:"scenarios"`
}

// ParseScenarios reads the scenarios of a YAML document:
//
//	scenarios:
//	  - name: baseline
//	  - name: hub-down
//	    nodes_down: [aws-hub]
//	    expect:
//	      - { from: builder-1, to: dgx-spark, verdict: OK }
//
// The reading is strict: an unknown key, a verdict or family that does not
// exist, a missing name or verdict, a name used twice, or a second document is
// refused with INVALID_SCENARIO. A document with an empty scenarios list is
// valid and has no scenarios. ParseScenarios reads bytes; the caller reads the
// file.
//
// Example:
//
//	the document above gives two scenarios, the second with one
//	expectation.
func ParseScenarios(data []byte) ([]Scenario, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var file scenarioFile
	if err := decoder.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, types.Errf(types.ErrInvalidScenario, "scenarios", "the document is empty")
		}
		return nil, types.Errf(types.ErrInvalidScenario, "scenarios", "%v", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, types.Errf(types.ErrInvalidScenario, "scenarios", "more than one YAML document")
	}
	var problems types.List
	seen := map[string]bool{}
	for i, sc := range file.Scenarios {
		subject := fmt.Sprintf("scenarios[%d]", i)
		switch {
		case sc.Name == "":
			problems.Add(types.Errf(types.ErrInvalidScenario, subject, "it has no name"))
		case seen[sc.Name]:
			problems.Add(types.Errf(types.ErrInvalidScenario, subject, "the name %q is used more than once", sc.Name))
		}
		seen[sc.Name] = true
		for j, want := range sc.Expect {
			if want.Verdict == VerdictUnknown {
				problems.Add(types.Errf(types.ErrInvalidScenario, fmt.Sprintf("%s.expect[%d]", subject, j), "it has no verdict"))
			}
			if want.From == "" {
				problems.Add(types.Errf(types.ErrInvalidScenario, fmt.Sprintf("%s.expect[%d]", subject, j), "it has no from node"))
			}
		}
	}
	if err := problems.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(file.Scenarios), nil
}
