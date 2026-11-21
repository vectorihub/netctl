package plan_test

// This file holds the runnable examples shown on the package documentation. They use
// the exported API only, and the three-site reference network.

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/types"
)

// ExamplePlan plans the three-site network from an empty address plan: the addresses it
// hands out, the configs it makes, and what the validator says. The direct link is not
// proven yet, so it is not used, and the validator warns about the weaknesses that
// leaves.
func ExamplePlan() {
	registry, _ := fixture.NewPlan()
	result, err := plan.Plan(plan.Inputs{
		Topology: fixture.ThreeSiteTopology(),
		IPAM:     registry.Snapshot(),
		Clock:    &types.FixedClock{T: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)},
	})
	fmt.Println("error:", err)
	fmt.Println("configs:", len(result.Configs), "valid:", result.Validation.OK)
	for _, c := range result.Changes[3:6] {
		fmt.Println(c)
	}
	for _, f := range result.Validation.Warnings() {
		fmt.Println(f.Rule, f.Subject)
	}
	fmt.Println("unusable:", result.Unusable)
	// Output:
	// error: <nil>
	// configs: 3 valid: true
	// allocated node:aws-hub 10.42.240.1/32
	// allocated node:builder-1 10.42.240.2/32
	// allocated node:dgx-spark 10.42.240.3/32
	// V-SIM-003 aws-hub~builder-1
	// V-SIM-003 aws-hub~dgx-spark
	// V-TOPO-004 builder-1~dgx-spark
	// unusable: [builder-1~dgx-spark]
}

// ExamplePlan_refused shows a plan the validator refuses: a bad static route makes traffic
// between the spokes loop. The error has the code a command line maps to exit code 2, and
// there are no configs to write.
func ExamplePlan_refused() {
	registry, _ := fixture.NewPlan()
	in := plan.Inputs{
		Topology: fixture.ThreeSiteTopology(),
		IPAM:     registry.Snapshot(),
		Clock:    &types.FixedClock{T: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)},
	}
	in.Overrides = append(in.Overrides, fixture.ThreeSiteLoop()...)
	result, err := plan.Plan(in)
	fmt.Println(types.HasCode(err, types.ErrValidationFailed), len(result.Configs))
	// Output:
	// true 0
}

// ExampleChange_String shows a change to the address plan on one line.
func ExampleChange_String() {
	fmt.Println(plan.Change{Kind: "released", Subject: "node:dgx-spark", Prefix: fixture.Prefix("10.42.240.3/32")})
	// Output:
	// released node:dgx-spark 10.42.240.3/32
}

// planned returns a first plan of the three-site network for the examples below.
func planned() plan.Result {
	registry, _ := fixture.NewPlan()
	result, _ := plan.Plan(plan.Inputs{
		Topology: fixture.ThreeSiteTopology(),
		IPAM:     registry.Snapshot(),
		Clock:    &types.FixedClock{T: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)},
	})
	return result
}

// ExampleResult_Render lists the files a plan renders, ready to be written by the caller.
func ExampleResult_Render() {
	files, err := planned().Render()
	fmt.Println(err, len(files), string(files[plan.FileRoutes][:15]))
	// Output:
	// <nil> 8 node aws-hub
	//
}

// ExampleResult_RoutesText prints the first lines of the routing tables.
func ExampleResult_RoutesText() {
	lines := strings.Split(planned().RoutesText(), "\n")
	fmt.Println(lines[0])
	fmt.Println(lines[1])
	// Output:
	// node aws-hub
	//   10.42.0.0/20  connected  active
}

// ExampleResult_SimulationText prints the verdict line of the simulation.
func ExampleResult_SimulationText() {
	text := strings.TrimSpace(planned().SimulationText(false))
	fmt.Println(text[strings.LastIndex(text, "\n")+1:])
	// Output:
	// PASS
}

// ExampleResult_SimulationJSON shows the simulation report is JSON.
func ExampleResult_SimulationJSON() {
	data, err := planned().SimulationJSON()
	fmt.Println(err, json.Valid(data))
	// Output:
	// <nil> true
}

// ExampleResult_Explain asks which route builder-1 uses for an address in dgx-spark's site.
func ExampleResult_Explain() {
	e, err := planned().Explain("builder-1", netip.MustParseAddr("10.42.33.7"))
	fmt.Println(err, e.Match.Prefix, e.Owner)
	// Output:
	// <nil> 10.42.32.0/20 site dgx
}

// ExampleResult_ExplainText prints the first line of the explanation.
func ExampleResult_ExplainText() {
	text, _ := planned().ExplainText("builder-1", netip.MustParseAddr("10.42.33.7"))
	fmt.Println(strings.SplitN(text, "\n", 2)[0])
	// Output:
	// builder-1 → 10.42.33.7 (site dgx)
}

// ExampleResult_ExplainJSON shows the explanation as JSON.
func ExampleResult_ExplainJSON() {
	data, err := planned().ExplainJSON("builder-1", netip.MustParseAddr("10.42.33.7"))
	fmt.Println(err, json.Valid(data))
	// Output:
	// <nil> true
}

// ExampleDiff compares a plan with itself: nothing would change on the machines.
func ExampleDiff() {
	result := planned()
	fmt.Println(plan.Diff(result, result).Empty())
	// Output:
	// true
}

// ExampleDiffJSON writes the diff of a plan with itself as JSON.
func ExampleDiffJSON() {
	result := planned()
	data, err := plan.DiffJSON(result, result)
	fmt.Println(err, strings.TrimSpace(string(data)))
	// Output:
	// <nil> {}
}
