package tests

import (
	"testing"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/topology"
)

// owners maps every owner in an address plan to the block it holds, so tests can compare
// who has what before and after a change without caring about order.
func owners(s ipam.Snapshot) map[string]string {
	out := map[string]string{}
	for _, a := range s.Allocations {
		if a.State == ipam.Allocated {
			out[a.Owner.String()] = a.Prefix.String()
		}
	}
	return out
}

// TestAMachineThatSortsFirstDoesNotMoveTheOthers is the point of address reconciliation:
// "aaa-new" sorts before every existing machine, but it must take the next free address and
// leave the machines already running where they are.
func TestAMachineThatSortsFirstDoesNotMoveTheOthers(t *testing.T) {
	first := mustPlan(t, fresh(t))
	before := owners(first.IPAM)

	in := fresh(t)
	in.IPAM = first.IPAM
	in.Topology.Nodes = append(in.Topology.Nodes, topology.Node{ID: nodeID("aaa-new"), Site: "builder", Role: 2, NAT: topology.NATBehind})
	in.Topology.Links = append(in.Topology.Links, topology.Link{Kind: 1, A: hub, B: nodeID("aaa-new"), Cost: 20})
	second := mustPlan(t, in)

	after := owners(second.IPAM)
	for owner, prefix := range before {
		if after[owner] != prefix {
			t.Errorf("%s moved from %s to %s", owner, prefix, after[owner])
		}
	}
	if got := after["node:aaa-new"]; got != "10.42.240.4/32" {
		t.Errorf("the new machine got %q, want the next free address 10.42.240.4/32", got)
	}
}

// TestRemovingAMachineLeavesAGapNobodyFillsInstantly checks that the address of a machine
// that left stays quarantined: a machine added in the same plan must not take it.
func TestRemovingAMachineLeavesAGapNobodyFillsInstantly(t *testing.T) {
	first := mustPlan(t, fresh(t))
	gone := owners(first.IPAM)["node:dgx-spark"]

	in := fresh(t)
	in.IPAM = first.IPAM
	in.Topology = dropDeadIntents(without(in.Topology, "dgx-spark", ""))
	in.Topology.Nodes = append(in.Topology.Nodes, topology.Node{ID: nodeID("newcomer"), Site: "builder", Role: 2, NAT: topology.NATBehind})
	in.Topology.Links = append(in.Topology.Links, topology.Link{Kind: 1, A: hub, B: nodeID("newcomer"), Cost: 20})
	second := mustPlan(t, in)

	if got := owners(second.IPAM)["node:newcomer"]; got == gone {
		t.Errorf("the newcomer took %s, which dgx-spark held a moment ago", gone)
	}
}

// TestTheTransitBlockOfALinkSurvivesReordering checks the /30 of the direct link is the
// same however the links are listed and whichever end is written first.
func TestTheTransitBlockOfALinkSurvivesReordering(t *testing.T) {
	first := mustPlan(t, fresh(t))
	want := owners(first.IPAM)["link:"+string(direct)]
	if want == "" {
		t.Fatal("the direct link has no transit block")
	}
	in := fresh(t)
	in.IPAM = first.IPAM
	for i, j := 0, len(in.Topology.Links)-1; i < j; i, j = i+1, j-1 {
		in.Topology.Links[i], in.Topology.Links[j] = in.Topology.Links[j], in.Topology.Links[i]
	}
	for i := range in.Topology.Links {
		in.Topology.Links[i].A, in.Topology.Links[i].B = in.Topology.Links[i].B, in.Topology.Links[i].A
	}
	second := mustPlan(t, in)
	if got := owners(second.IPAM)["link:"+string(direct)]; got != want {
		t.Errorf("the transit block moved from %s to %s", want, got)
	}
}
