package netctl_test

// This file holds the runnable examples of the root package. They use the exported API only
// and the three-site reference network.

import (
	"fmt"
	"sort"
	"time"

	"github.com/vectorihub/netctl"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/types"
)

// inputs returns the inputs of a first plan of the three-site network.
func inputs() netctl.Inputs {
	registry, _ := fixture.NewPlan()
	return netctl.Inputs{
		Topology: fixture.ThreeSiteTopology(),
		IPAM:     registry.Snapshot(),
		Clock:    &types.FixedClock{T: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)},
	}
}

// ExamplePlan plans the three-site network and writes the names of the files a caller would
// put on disk.
func ExamplePlan() {
	result, err := netctl.Plan(inputs())
	if err != nil {
		fmt.Println(err)
		return
	}
	files, _ := result.Render()
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Println(result.PlanID[:11], result.Validation.OK)
	for _, name := range names {
		fmt.Println(name)
	}
	// Output:
	// three-site@ true
	// ipam.json
	// nodes/aws-hub.json
	// nodes/builder-1.json
	// nodes/dgx-spark.json
	// plan.json
	// routes.txt
	// simulation.json
	// validation.json
}

// ExampleDiff compares a plan with itself: the machines would see no change.
func ExampleDiff() {
	result, _ := netctl.Plan(inputs())
	fmt.Println(netctl.Diff(result, result).Empty())
	// Output:
	// true
}
