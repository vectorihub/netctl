package plan_test

// This file holds the runnable examples shown on the package documentation. They use
// the exported API only, and the three-site reference network.

import (
	"fmt"
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
