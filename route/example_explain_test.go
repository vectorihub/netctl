package route_test

// This file holds the runnable examples for explaining routes and comparing
// tables. They use only the exported API.

import (
	"fmt"
	"net/netip"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// ExampleExplain shows why builder-1 sends a packet for 10.42.33.7 where it
// does: the route in use, the backup that would take over, and the health of
// the link, as text that can be printed or kept as a golden file.
func ExampleExplain() {
	result, _ := route.Compute(threeSite(false), route.Input{Owned: threeSiteOwned()})
	healthy := func(link types.LinkID) (bool, string) { return true, "probe ok, 3/3" }

	explanation := route.Explain(result.Tables["builder-1"], netip.MustParseAddr("10.42.33.7"),
		route.Notes{Owner: "dgx-spark, site dgx", Health: healthy})
	fmt.Print(explanation)
	// Output:
	// builder-1 → 10.42.33.7 (dgx-spark, site dgx)
	//    match:   10.42.32.0/20 (longest of 2 candidates)
	//    active:  via dgx-spark over builder-1~dgx-spark, metric 10, path builder-1 → dgx-spark
	//    backup:  via aws-hub over aws-hub~builder-1, metric 40, path builder-1 → aws-hub → dgx-spark
	//    reason:  the cheapest path, metric 10; link builder-1~dgx-spark is up (probe ok, 3/3)
}

// ExampleExplain_blackHole shows an address nothing covers: the explanation says
// so and calls it a black hole, instead of inventing a route.
func ExampleExplain_blackHole() {
	result, _ := route.Compute(threeSite(false), route.Input{Owned: threeSiteOwned()})
	fmt.Print(route.Explain(result.Tables["builder-1"], netip.MustParseAddr("192.168.1.1"), route.Notes{}))
	// Output:
	// builder-1 → 192.168.1.1
	//    match:   none
	//    active:  none, the packet would be dropped (black hole)
	//    backup:  none
	//    reason:  no active route covers 192.168.1.1, so the packet is dropped
}

// ExampleDiff shows what changes in the routing tables when the direct link
// fails. Only the two spokes change. Each loses its direct route, and its
// backup through the hub takes over; aws-hub is not listed.
func ExampleDiff() {
	input := route.Input{Owned: threeSiteOwned()}
	healthy, _ := route.Compute(threeSite(false), input)
	failed, _ := route.Compute(threeSite(true), input)

	for _, diff := range route.Diff(healthy.Tables, failed.Tables) {
		fmt.Println(diff.Node)
		for _, gone := range diff.Removed {
			fmt.Printf("  removed %s via %s, metric %d\n", gone.Prefix, gone.NextHop, gone.Metric)
		}
		for _, change := range diff.Changed {
			fmt.Printf("  changed %s via %s: was %s, now %s, active=%v\n",
				change.New.Prefix, change.New.NextHop, change.Old.Source, change.New.Source, change.New.Active)
			fmt.Println("    taken over from a backup:", change.Activated())
		}
	}
	// Output:
	// builder-1
	//   removed 10.42.32.0/20 via dgx-spark, metric 10
	//   changed 10.42.32.0/20 via aws-hub: was backup, now computed, active=true
	//     taken over from a backup: true
	// dgx-spark
	//   removed 10.42.16.0/20 via builder-1, metric 10
	//   changed 10.42.16.0/20 via aws-hub: was backup, now computed, active=true
	//     taken over from a backup: true
}
