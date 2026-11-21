package failover_test

// This file holds the runnable examples shown on the package documentation. They
// use the exported API only. The network is the three-site reference network:
// builder-1 and dgx-spark joined directly, and each joined to aws-hub.

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// start is the instant the examples count seconds from.
var start = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// at returns the instant n seconds after start.
func at(n int) time.Time { return start.Add(time.Duration(n) * time.Second) }

// direct is the direct link between builder-1 and dgx-spark.
const direct = types.LinkID("builder-1~dgx-spark")

// probes returns n probe signals on the direct link from both ends, one every 5
// seconds from the given second.
func probes(ok bool, n, from int) []health.Signal {
	var out []health.Signal
	for i := 0; i < n; i++ {
		for _, end := range []types.NodeID{"builder-1", "dgx-spark"} {
			out = append(out, health.Signal{Link: direct, From: end, Kind: health.SignalProbe, OK: ok, At: at(from + 5*i)})
		}
	}
	return out
}

// network returns the three-site network, with the direct link required.
func network() failover.Network {
	return failover.Network{
		Graph: fixture.ThreeSite(true), Input: fixture.ThreeSiteInput(),
		Required: map[types.LinkID]bool{direct: true},
	}
}

// ExampleEvaluate shows a failover: the direct link is proven, then fails, and the
// one evaluation says which routes moved on both ends.
func ExampleEvaluate() {
	net := network()
	first, _ := failover.Evaluate(net, failover.State{}, probes(true, 5, 5), at(30))
	result, _ := failover.Evaluate(net, first.State, probes(false, 3, 35), at(50))
	for _, d := range result.Decisions {
		fmt.Printf("%s %s to %s: %d withdrawn, %d activated\n", d.Link, d.Before, d.After, len(d.Withdrawn), len(d.Activated))
	}
	for _, r := range result.Decisions[1].Activated {
		fmt.Printf("%s now sends %s via %s\n", r.Node, r.Prefix, r.Via)
	}
	// Output:
	// builder-1~dgx-spark healthy to degraded: 0 withdrawn, 0 activated
	// builder-1~dgx-spark degraded to down: 4 withdrawn, 4 activated
	// builder-1 now sends 10.42.32.0/20 via aws-hub~builder-1
	// builder-1 now sends 10.42.240.3/32 via aws-hub~builder-1
	// dgx-spark now sends 10.42.16.0/20 via aws-hub~dgx-spark
	// dgx-spark now sends 10.42.240.2/32 via aws-hub~dgx-spark
}

// ExampleState_JSON shows a state being saved in the middle of a hold-down and
// loaded again: the hold-down still ends when it would have.
func ExampleState_JSON() {
	net := network()
	first, _ := failover.Evaluate(net, failover.State{}, probes(true, 5, 5), at(30))
	failed, _ := failover.Evaluate(net, first.State, probes(false, 3, 35), at(50))

	saved, _ := failed.State.JSON()
	restored, _ := failover.ParseState(saved)
	link, _ := restored.Link(direct)
	fmt.Println(link.State, "- hold-down ends", link.Dirs["builder-1"].HoldDownUntil.Sub(start))
	// Output:
	// down - hold-down ends 1m45s
}

// ExampleParseState shows a saved state of another version being refused with its
// code, and nothing else being checked.
func ExampleParseState() {
	_, err := failover.ParseState([]byte(`{"schema": "beaver.failover/v0", "links": []}`))
	fmt.Println(types.HasCode(err, types.ErrUnsupportedSchema))
	// Output:
	// true
}

// ExampleState_Link shows looking up one link's health in a state.
func ExampleState_Link() {
	first, _ := failover.Evaluate(network(), failover.State{}, probes(true, 5, 5), at(30))
	link, ok := first.State.Link(direct)
	fmt.Println(ok, link.State)
	_, ok = first.State.Link("nowhere~else")
	fmt.Println(ok)
	// Output:
	// true healthy
	// false
}

// ExampleEvaluate_rejected shows the guard. A bad static route on the hub sends
// dgx-spark's traffic to builder-1. That is harmless while builder-1 has the
// direct link, but once the link fails builder-1 hands the traffic to the hub and
// the hub hands it back. The simulation sees the loop, the failover is not
// installed, and the tables from before stay.
func ExampleEvaluate_rejected() {
	plan, _ := fixture.ThreeSitePlan()
	flows, _ := sim.Flows(fixture.ThreeSiteTopology(), plan, sim.Options{})
	net := network()
	net.Flows = flows
	net.Input.Overrides = []route.Override{{Node: "aws-hub", Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: "builder-1"}}

	first, _ := failover.Evaluate(net, failover.State{}, probes(true, 5, 5), at(30))
	result, _ := failover.Evaluate(net, first.State, probes(false, 3, 35), at(50))
	fmt.Println("rejected:", result.Rejected != nil)
	fmt.Println("tables kept:", len(route.Diff(first.Tables, result.Tables)) == 0)
	link, _ := result.State.Link(direct)
	fmt.Println("the link is still recorded as", link.State)
	for _, f := range result.Rejected.Failures {
		fmt.Println(f.Flow, f.Verdict)
	}
	// Output:
	// rejected: true
	// tables kept: true
	// the link is still recorded as down
	// builder→dgx (site) LOOP
	// dgx→builder (site) LOOP
}

// ExampleRequiredLinks shows which links of a topology must be proven: the direct
// link, and not the hub-spoke links, which are assumed to work.
func ExampleRequiredLinks() {
	required := failover.RequiredLinks(fixture.ThreeSiteTopology())
	fmt.Println(len(required), required[direct])
	// Output:
	// 1 true
}
