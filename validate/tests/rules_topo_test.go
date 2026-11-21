package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// TestRule_V_TOPO_001 plants a link to a node that is not there and a duplicate ID, and
// checks the loader's own errors come back through the validator.
func TestRule_V_TOPO_001(t *testing.T) {
	quiet(t, "V-TOPO-001")
	p := cleanPlan(t, true)
	p.Topology.Links = append(p.Topology.Links, topology.Link{Kind: types.LinkDirect, A: builder, B: "ghost", Cost: 10, Health: topology.HealthAssumed})
	got := fires(t, p, "V-TOPO-001")
	if !strings.Contains(got[0].Message, "T4") || !strings.Contains(got[0].Message, "ghost") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	p.Topology.Nodes = append(p.Topology.Nodes, p.Topology.Nodes[0])
	fires(t, p, "V-TOPO-001")
	p = cleanPlan(t, true)
	p.Topology.Schema = "beaver.topology/v0"
	fires(t, p, "V-TOPO-001")
}

// TestRule_V_TOPO_002 takes down the hub link of builder-1, with the direct link up and
// then down, and checks the node is reported either way: the direct link to dgx-spark is
// no way to a hub, because a spoke does not forward.
func TestRule_V_TOPO_002(t *testing.T) {
	quiet(t, "V-TOPO-002")
	for _, down := range [][]types.LinkID{{hubB}, {hubB, direct}} {
		got := fires(t, withLinksDown(cleanPlan(t, true), down...), "V-TOPO-002")
		if len(got) != 1 || got[0].Subject != "builder-1" {
			t.Errorf("down %v: %+v", down, got)
		}
	}
	// Both hub links down: both spokes.
	if got := fires(t, withLinksDown(cleanPlan(t, true), hubB, hubD), "V-TOPO-002"); len(got) != 2 {
		t.Errorf("%+v", got)
	}
	// The direct link alone down leaves everyone with a hub.
	if got := findings(withLinksDown(cleanPlan(t, true), direct), "V-TOPO-002"); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

// TestRule_V_TOPO_003 gives the topology a spoke with one link.
func TestRule_V_TOPO_003(t *testing.T) {
	quiet(t, "V-TOPO-003")
	p := cleanPlan(t, true)
	addNode(p, "solo", types.RoleSpoke)
	p.Topology.Links = append(p.Topology.Links, topology.Link{Kind: types.LinkHubSpoke, A: hub, B: "solo", Cost: 20})
	got := fires(t, p, "V-TOPO-003")
	if len(got) != 1 || got[0].Subject != "solo" {
		t.Errorf("%+v", got)
	}
	// The hub, which has the most links but could have one, is not a spoke.
	p = cleanPlan(t, true)
	p.Topology.Links = p.Topology.Links[:1]
	for _, f := range findings(p, "V-TOPO-003") {
		if f.Subject == "aws-hub" {
			t.Errorf("the hub was reported: %+v", f)
		}
	}
}

// TestRule_V_TOPO_004 checks the direct link between the two NATed nodes of the three-site
// network is warned about, healthy or not yet proven, and only while both are NATed.
func TestRule_V_TOPO_004(t *testing.T) {
	for _, up := range []bool{true, false} {
		got := findings(cleanPlan(t, up), "V-TOPO-004")
		if len(got) != 1 || got[0].Subject != string(direct) {
			t.Errorf("directUp=%v: %+v", up, got)
		}
	}
	for _, nat := range []topology.NATMode{topology.NATNone} {
		p := cleanPlan(t, true)
		for i := range p.Topology.Nodes {
			if p.Topology.Nodes[i].ID == builder {
				p.Topology.Nodes[i].NAT = nat
			}
		}
		if got := findings(p, "V-TOPO-004"); len(got) != 0 {
			t.Errorf("a node that can be dialled: %+v", got)
		}
	}
	p := cleanPlan(t, true)
	for i := range p.Topology.Nodes {
		if p.Topology.Nodes[i].ID != hub {
			p.Topology.Nodes[i].NAT = topology.NATUnknown
		}
	}
	fires(t, p, "V-TOPO-004") // a node that never said counts as behind NAT
}

// TestRule_V_TOPO_020 adds a second hub.
func TestRule_V_TOPO_020(t *testing.T) {
	quiet(t, "V-TOPO-020")
	p := cleanPlan(t, true)
	addNode(p, "aws-hub-2", types.RoleHub)
	got := fires(t, p, "V-TOPO-020")
	if !strings.Contains(got[0].Message, "aws-hub, aws-hub-2") {
		t.Errorf("%+v", got)
	}
}
