package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// P1-VAL-010
//
// TestRule_V_LINK_001 plants a peer on one node that the other does not have, and a node
// with no config, and checks the finding names both nodes.
func TestRule_V_LINK_001(t *testing.T) {
	quiet(t, "V-LINK-001")
	p := cleanPlan(t, true)
	c := configOf(p, dgx)
	c.Peers = append(c.Peers[:peerIndex(c, builder)], c.Peers[peerIndex(c, builder)+1:]...)
	got := fires(t, p, "V-LINK-001")
	if len(got) != 1 || got[0].Subject != string(direct) || !strings.Contains(got[0].Message, "builder-1") || !strings.Contains(got[0].Message, "dgx-spark") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	for i, c := range p.Configs {
		if c.Node == dgx {
			p.Configs = append(p.Configs[:i], p.Configs[i+1:]...)
			break
		}
	}
	got = fires(t, p, "V-LINK-001")
	if !strings.Contains(got[0].Message, "has no config") {
		t.Errorf("%+v", got)
	}
	// The two ends disagree about the link's name.
	p = cleanPlan(t, true)
	c = configOf(p, dgx)
	c.Peers[peerIndex(c, builder)].Link = "dgx-spark~builder-1"
	fires(t, p, "V-LINK-001")
}

// TestRule_V_LINK_002 makes the two ends disagree about the kind of link, about the
// topology, and about the transit addresses.
func TestRule_V_LINK_002(t *testing.T) {
	quiet(t, "V-LINK-002")
	p := cleanPlan(t, true)
	c := configOf(p, dgx)
	c.Peers[peerIndex(c, builder)].Role = config.PeerSpoke
	got := fires(t, p, "V-LINK-002")
	if !strings.Contains(got[0].Message, "dgx-spark sees the link") && !strings.Contains(got[0].Message, "builder-1 sees the link") {
		t.Errorf("%+v", got)
	}
	// Both ends call it a hub link, but the topology says direct.
	p = cleanPlan(t, true)
	for _, node := range []types.NodeID{builder, dgx} {
		c := configOf(p, node)
		other := builder
		if node == builder {
			other = dgx
		}
		c.Peers[peerIndex(c, other)].Role = config.PeerSpoke
	}
	got = fires(t, p, "V-LINK-002")
	if !strings.Contains(got[0].Message, "the topology says the link is direct") {
		t.Errorf("%+v", got)
	}
	// The probe addresses do not mirror.
	p = cleanPlan(t, true)
	c = configOf(p, dgx)
	c.Peers[peerIndex(c, builder)].Probe.Remote = pfx("10.42.252.3/32").Addr()
	got = fires(t, p, "V-LINK-002")
	if !strings.Contains(got[0].Message, "do not mirror") {
		t.Errorf("%+v", got)
	}
}

// P1-VAL-012
//
// TestRule_V_LINK_003 takes the probe off one end of the required-health direct link, then
// the health target from the other.
func TestRule_V_LINK_003(t *testing.T) {
	quiet(t, "V-LINK-003")
	p := cleanPlan(t, true)
	c := configOf(p, dgx)
	c.Peers[peerIndex(c, builder)].Probe = nil
	got := fires(t, p, "V-LINK-003")
	if len(got) != 1 || got[0].Subject != string(direct) || !strings.Contains(got[0].Message, "dgx-spark's config has no probe") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	c = configOf(p, builder)
	c.Health.Probes = nil
	got = fires(t, p, "V-LINK-003")
	if !strings.Contains(got[0].Message, "builder-1's config has a probe address") {
		t.Errorf("%+v", got)
	}
	// An assumed direct link needs no probe.
	p = cleanPlan(t, true)
	for i := range p.Topology.Links {
		if p.Topology.Links[i].Kind == types.LinkDirect {
			p.Topology.Links[i].Health = topology.HealthAssumed
		}
	}
	c = configOf(p, dgx)
	c.Peers[peerIndex(c, builder)].Probe = nil
	if got := findings(p, "V-LINK-003"); len(got) != 0 {
		t.Errorf("an assumed link was reported: %+v", got)
	}
}

// P1-VAL-011
//
// TestRule_V_LINK_004 makes the direct link dearer than the way by the hub (20 and 20, so
// 40), at the edge and beyond it.
func TestRule_V_LINK_004(t *testing.T) {
	quiet(t, "V-LINK-004")
	for cost, want := range map[int]bool{10: false, 39: false, 40: false, 41: true, 50: true} {
		p := cleanPlan(t, true)
		for i := range p.Topology.Links {
			if p.Topology.Links[i].Kind == types.LinkDirect {
				p.Topology.Links[i].Cost = cost
			}
		}
		got := findings(p, "V-LINK-004")
		if (len(got) > 0) != want {
			t.Errorf("cost %d: %+v, want fire=%v", cost, got, want)
		}
		if want && (got[0].Subject != string(direct) || !strings.Contains(got[0].Message, "40")) {
			t.Errorf("cost %d: %+v", cost, got)
		}
	}
	// With no hub joining both ends there is no hub path to compare with.
	p := cleanPlan(t, true)
	for i := range p.Topology.Links {
		if p.Topology.Links[i].Kind == types.LinkDirect {
			p.Topology.Links[i].Cost = 500
		}
	}
	p.Topology.Links = p.Topology.Links[len(p.Topology.Links)-1:]
	if got := findings(p, "V-LINK-004"); len(got) != 0 {
		t.Errorf("no hub path, yet %+v", got)
	}
}
