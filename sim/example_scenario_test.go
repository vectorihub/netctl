package sim_test

// This file holds the runnable examples of scenarios, fault injection and
// reports. They use only the exported API and the three-node network of
// example_test.go.

import (
	"fmt"
	"net/netip"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// nodeID converts text to a node ID.
func nodeID(text string) types.NodeID { return types.NodeID(text) }

// nodeIDs converts text to a list of node IDs.
func nodeIDs(texts ...string) []types.NodeID {
	ids := make([]types.NodeID, len(texts))
	for i, text := range texts {
		ids[i] = types.NodeID(text)
	}
	return ids
}

// linkIDs converts text to a list of link IDs.
func linkIDs(texts ...string) []types.LinkID {
	ids := make([]types.LinkID, len(texts))
	for i, text := range texts {
		ids[i] = types.LinkID(text)
	}
	return ids
}

// exampleFlows returns the overlay flows between the three nodes, in one fixed
// order: every node's overlay address to every other node's.
func exampleFlows() []sim.Flow {
	addrs := map[string]string{"aws-hub": "10.42.240.1", "builder-1": "10.42.240.2", "dgx-spark": "10.42.240.3"}
	var flows []sim.Flow
	for _, from := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		for _, to := range []string{"aws-hub", "builder-1", "dgx-spark"} {
			if from != to {
				flows = append(flows, sim.Flow{
					Label: from + "→" + to, Family: sim.FamilyOverlay,
					Src: netip.MustParseAddr(addrs[from]), Dst: netip.MustParseAddr(addrs[to]),
					From: nodeID(from), To: nodeID(to),
				})
			}
		}
	}
	return flows
}

// ExampleRunScenario fails the direct link and shows the flow between the two
// spokes still works, now through the hub. The routes were recomputed by the
// routing engine, not guessed.
func ExampleRunScenario() {
	in := sim.ScenarioInput{Graph: network{direct: true}, Input: route.Input{Owned: owned()}, Flows: exampleFlows()}
	result, err := sim.RunScenario(in, sim.Scenario{Name: "direct-link-down", LinksDown: linkIDs("builder-1~dgx-spark")})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, flow := range result.Report.Flows {
		if flow.Flow.Label == "builder-1→dgx-spark" {
			printTrace(flow.Forward)
		}
	}
	fmt.Println("passed:", result.Passed)
	// Output:
	// builder-1 > aws-hub > dgx-spark: DELIVERED
	// passed: true
}

// ExampleRunScenario_expect turns a scenario into an assertion. With the hub down
// the spokes still reach each other over their direct link, and the hub is
// unreachable, which is the expected result for a node that has failed. A wrong
// expectation fails the scenario and names the flow.
func ExampleRunScenario_expect() {
	in := sim.ScenarioInput{Graph: network{direct: true}, Input: route.Input{Owned: owned()}, Flows: exampleFlows()}
	result, _ := sim.RunScenario(in, sim.Scenario{
		Name: "hub-down", NodesDown: nodeIDs("aws-hub"),
		Expect: []sim.Expectation{
			{From: "builder-1", To: "dgx-spark", Verdict: sim.VerdictOK},
			{From: "builder-1", To: "aws-hub", Verdict: sim.VerdictUnreachableExpected},
			{From: "dgx-spark", To: "builder-1", Verdict: sim.VerdictBlackHole},
		},
	})
	fmt.Println("passed:", result.Passed)
	for _, mismatch := range result.Mismatches {
		fmt.Println(mismatch.Detail)
	}
	// Output:
	// passed: false
	// dgx-spark→builder-1: expected BLACK_HOLE, got OK
}

// ExampleRunScenarios runs a set of scenarios, one route computation each.
func ExampleRunScenarios() {
	in := sim.ScenarioInput{Graph: network{direct: true}, Input: route.Input{Owned: owned()}, Flows: exampleFlows()}
	results, _ := sim.RunScenarios(in, []sim.Scenario{
		{Name: "baseline"},
		{Name: "direct-link-down", LinksDown: linkIDs("builder-1~dgx-spark")},
		{Name: "hub-down", NodesDown: nodeIDs("aws-hub")},
	})
	for _, result := range results {
		fmt.Println(result.Name, result.Passed)
	}
	// Output:
	// baseline true
	// direct-link-down true
	// hub-down true
}

// ExampleParseScenarios reads scenarios from YAML, the form a plan keeps them in.
func ExampleParseScenarios() {
	scenarios, err := sim.ParseScenarios([]byte(`
scenarios:
  - name: baseline
  - name: hub-down
    nodes_down: [aws-hub]
    expect:
      - { from: builder-1, to: dgx-spark, verdict: OK }
`))
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, sc := range scenarios {
		fmt.Println(sc.Name, len(sc.NodesDown), len(sc.Expect))
	}
	// Output:
	// baseline 0 0
	// hub-down 1 1
}

// ExampleCorruptTable plants a fault to see the simulator catch it: the hub
// forgets dgx-spark. A packet from builder-1 goes as far as the hub and is
// dropped there.
func ExampleCorruptTable() {
	g := network{}
	result, _ := route.Compute(g, route.Input{Owned: owned()})
	broken, err := sim.CorruptTable(result.Tables, "aws-hub", netip.MustParsePrefix("10.42.240.3/32"), "")
	if err != nil {
		fmt.Println(err)
		return
	}
	net := sim.NewNet(g, broken, owned(), nil)
	trace := net.Send(sim.Packet{Dst: netip.MustParseAddr("10.42.240.3")}, "builder-1")
	printTrace(trace)
	// The original tables are untouched.
	printTrace(sim.NewNet(g, result.Tables, owned(), nil).Send(sim.Packet{Dst: netip.MustParseAddr("10.42.240.3")}, "builder-1"))
	// Output:
	// builder-1 > aws-hub: BLACK_HOLE
	// builder-1 > aws-hub > dgx-spark: DELIVERED
}

// ExampleReport_Text writes a report as text: the counts, the verdict, and the
// journey of each flow listed.
func ExampleReport_Text() {
	net := build(false)
	flow := exampleFlows()[2] // builder-1 to aws-hub
	report := sim.NewReport([]sim.FlowResult{net.Run(flow, false)})
	fmt.Print(report.Text(sim.TextOptions{Title: "example", Verbose: true}))
	// Output:
	// SIMULATION example · 1 flow
	//   OK                          1
	//   UNREACHABLE_EXPECTED        0
	//   ASYMMETRIC                  0
	//   NO_RETURN                   0
	//   BLACK_HOLE                  0
	//   LOOP                        0
	//   LINK_DOWN                   0
	// PASS
	//
	// builder-1→aws-hub (10.42.240.2 → 10.42.240.1) OK
	//   fwd: builder-1 ─[aws-hub~builder-1]→ aws-hub  DELIVERED (1 hop)
	//   ret: aws-hub ─[aws-hub~builder-1]→ builder-1  DELIVERED (1 hop)
	//   symmetric: yes
}

// ExampleReport_JSON writes a report as JSON. The keys are always in the same
// order, so two runs of the same simulation give the same bytes.
func ExampleReport_JSON() {
	report := sim.NewReport(nil)
	data, _ := report.JSON()
	fmt.Print(string(data))
	// Output:
	// {
	//   "flows": [],
	//   "summary": {
	//     "total": 0,
	//     "failed": 0,
	//     "warnings": 0,
	//     "verdicts": [],
	//     "problems": []
	//   },
	//   "passed": true
	// }
}

// ExampleScenarioResult_Text shows a scenario's text: the name in the header, and
// the nodes the scenario cut off.
func ExampleScenarioResult_Text() {
	in := sim.ScenarioInput{Graph: network{direct: false}, Input: route.Input{Owned: owned()}, Flows: exampleFlows()}
	result, _ := sim.RunScenario(in, sim.Scenario{Name: "hub-down", NodesDown: nodeIDs("aws-hub")})
	text := result.Text(sim.TextOptions{Title: "example"})
	fmt.Println(text[:len("SIMULATION example · scenario hub-down · 6 flows")])
	fmt.Println(result.Passed)
	// Output:
	// SIMULATION example · scenario hub-down · 6 flows
	// true
}

// ExampleScenarioResult_JSON writes a scenario result as JSON, beginning with its
// name.
func ExampleScenarioResult_JSON() {
	in := sim.ScenarioInput{Graph: network{direct: true}, Input: route.Input{Owned: owned()}}
	result, _ := sim.RunScenario(in, sim.Scenario{Name: "baseline"})
	data, _ := result.JSON()
	fmt.Println(string(data[:len("{\n  \"name\": \"baseline\",")]))
	// Output:
	// {
	//   "name": "baseline",
}
