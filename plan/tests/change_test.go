package tests

import (
	"slices"
	"testing"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/topology"
)

// without returns the topology minus a node, its links, and optionally its site.
func without(topo *topology.Topology, node string, site string) *topology.Topology {
	out := *topo
	out.Nodes = slices.DeleteFunc(slices.Clone(topo.Nodes), func(n topology.Node) bool { return string(n.ID) == node })
	out.Links = slices.DeleteFunc(slices.Clone(topo.Links), func(l topology.Link) bool { return string(l.A) == node || string(l.B) == node })
	if site != "" {
		out.Sites = slices.DeleteFunc(slices.Clone(topo.Sites), func(s topology.Site) bool { return string(s.ID) == site })
	}
	return dropDeadIntents(&out)
}

// dropDeadIntents removes the intents that no longer pick any node, as an author editing the
// file would: the planner (rightly) refuses an intent that matches nothing.
func dropDeadIntents(topo *topology.Topology) *topology.Topology {
	topo.Intents = slices.DeleteFunc(slices.Clone(topo.Intents), func(i topology.Intent) bool {
		return len(i.From.Resolve(topo)) == 0 || len(i.To.Resolve(topo)) == 0
	})
	return topo
}

// TestAMachineLeavesAndItsAddressesAreReleased checks a node taken out of the topology has
// its overlay address released into quarantine, along with the transit block of the link it
// was on, and the other nodes keep what they had.
func TestAMachineLeavesAndItsAddressesAreReleased(t *testing.T) {
	first := mustPlan(t, fresh(t))
	in := fresh(t)
	in.IPAM = first.IPAM
	in.Topology = without(in.Topology, "dgx-spark", "")
	r, err := plan.Plan(in)
	if err != nil {
		t.Fatal(err)
	}
	released := map[string]string{}
	for _, c := range r.Changes {
		if c.Kind == "released" {
			released[c.Subject] = c.Prefix.String()
		}
	}
	if released["node:dgx-spark"] != "10.42.240.3/32" || released["link:builder-1~dgx-spark"] != "10.42.252.0/30" || len(released) != 2 {
		t.Errorf("released %v", released)
	}
	if len(r.Configs) != 2 {
		t.Errorf("%d configs for two nodes", len(r.Configs))
	}
	var quarantined bool
	for _, a := range r.IPAM.Allocations {
		if a.Owner == ipam.Node("dgx-spark") && a.State == ipam.Quarantined {
			quarantined = true
		}
	}
	if !quarantined {
		t.Error("the address was not quarantined")
	}
	for _, c := range r.Changes {
		if c.Kind == "allocated" {
			t.Errorf("something was allocated: %v", c)
		}
	}
}

// TestANodeAndItsSiteCanLeaveInOnePlan checks the order of release: nodes and links first,
// then sites, so a site whose machine is also going is not refused for still having it.
func TestANodeAndItsSiteCanLeaveInOnePlan(t *testing.T) {
	first := mustPlan(t, fresh(t))
	in := fresh(t)
	in.IPAM = first.IPAM
	in.Topology = without(in.Topology, "dgx-spark", "dgx")
	r, err := plan.Plan(in)
	if err != nil {
		t.Fatalf("a node and its site leaving together: %v", err)
	}
	released := map[string]bool{}
	for _, c := range r.Changes {
		if c.Kind == "released" {
			released[c.Subject] = true
		}
	}
	for _, owner := range []string{"node:dgx-spark", "site:dgx", "link:builder-1~dgx-spark"} {
		if !released[owner] {
			t.Errorf("%s was not released: %v", owner, released)
		}
	}
}

// TestARenamedMachineIsARemovalAndAnAddition checks no address moves silently: the old name's
// address is quarantined, and the new name gets a different one.
func TestARenamedMachineIsARemovalAndAnAddition(t *testing.T) {
	first := mustPlan(t, fresh(t))
	in := fresh(t)
	in.IPAM = first.IPAM
	for i := range in.Topology.Nodes {
		if in.Topology.Nodes[i].ID == "dgx-spark" {
			in.Topology.Nodes[i].ID = "dgx-spark-2"
		}
	}
	for i := range in.Topology.Links {
		if in.Topology.Links[i].A == "dgx-spark" {
			in.Topology.Links[i].A = "dgx-spark-2"
		}
		if in.Topology.Links[i].B == "dgx-spark" {
			in.Topology.Links[i].B = "dgx-spark-2"
		}
	}
	in.Topology = dropDeadIntents(in.Topology)
	r, err := plan.Plan(in)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, c := range r.Changes {
		kinds[c.Kind+" "+c.Subject] = c.Prefix.String()
	}
	if kinds["released node:dgx-spark"] != "10.42.240.3/32" {
		t.Errorf("the old address was not released: %v", kinds)
	}
	if got := kinds["allocated node:dgx-spark-2"]; got == "" || got == "10.42.240.3/32" {
		t.Errorf("the new name got %q: it must not take the old address while it is quarantined", got)
	}
}

// TestChangeIsDescribedOnOneLine checks the text of a change.
func TestChangeIsDescribedOnOneLine(t *testing.T) {
	r := mustPlan(t, fresh(t))
	if got := r.Changes[0].String(); got != "allocated site:aws 10.42.0.0/20" {
		t.Errorf("%q", got)
	}
}
