package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// alloc returns an allocation of a prefix to an owner in a pool.
func alloc(prefix, pool string, owner ipam.Owner, note string) ipam.Allocation {
	return ipam.Allocation{Prefix: pfx(prefix), Pool: ipam.PoolName(pool), Owner: owner, State: ipam.Allocated, CreatedAt: time.Unix(0, 0).UTC(), Note: note}
}

// P1-VAL-001
//
// TestRule_V_ADDR_001 plants an allocation inside a site's block and checks the overlap is
// reported with both owners, and that a clean plan has none.
func TestRule_V_ADDR_001(t *testing.T) {
	quiet(t, "V-ADDR-001")
	p := cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("10.42.16.0/24", "sites", ipam.Node("intruder"), ""))
	got := fires(t, p, "V-ADDR-001")
	if len(got) != 1 || !strings.Contains(got[0].Message, "node:intruder") || !strings.Contains(got[0].Message, "site:builder") || got[0].Subject != "10.42.16.0/24" {
		t.Errorf("%+v", got)
	}
	// Several blocks inside one wide one are each reported against it.
	p = cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations,
		alloc("10.42.16.0/28", "sites", ipam.Node("a"), ""), alloc("10.42.16.32/28", "sites", ipam.Node("b"), ""), alloc("10.42.16.5/32", "sites", ipam.Node("c"), ""))
	if got := findings(p, "V-ADDR-001"); len(got) != 3 {
		t.Errorf("%d findings, want 3: %v", len(got), got)
	}
	// The same block given to two owners is an overlap too.
	p = cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("10.42.240.2/32", "overlay", ipam.Node("twin"), ""))
	fires(t, p, "V-ADDR-001")
}

// P1-VAL-002
//
// TestRule_V_ADDR_002 hand-edits the snapshot so that it no longer loads (an allocation
// outside the supernet, a state that does not exist) and checks the rule says why. An
// overlap alone is V-ADDR-001's and does not make this one fire.
func TestRule_V_ADDR_002(t *testing.T) {
	quiet(t, "V-ADDR-002")
	p := cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("192.0.2.0/24", "sites", ipam.Node("stray"), ""))
	got := fires(t, p, "V-ADDR-002")
	if !strings.Contains(got[0].Message, "does not load") || !strings.Contains(got[0].Message, "OUT_OF_SUPERNET") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	p.IPAM.Allocations[0].State = "imaginary"
	fires(t, p, "V-ADDR-002")
	p = cleanPlan(t, true)
	p.IPAM.Schema = "beaver.ipam/v0"
	fires(t, p, "V-ADDR-002")

	overlap := cleanPlan(t, true)
	overlap.IPAM.Allocations = append(overlap.IPAM.Allocations, alloc("10.42.16.0/24", "sites", ipam.Node("intruder"), ""))
	if got := findings(overlap, "V-ADDR-002"); len(got) != 0 {
		t.Errorf("an overlap made V-ADDR-002 fire as well as V-ADDR-001: %v", got)
	}
}

// P1-VAL-003
//
// TestRule_V_ADDR_003 takes away a node's overlay address, then gives another node a
// second one.
func TestRule_V_ADDR_003(t *testing.T) {
	quiet(t, "V-ADDR-003")
	p := cleanPlan(t, true)
	p.IPAM = withoutAllocation(p.IPAM, func(a ipam.Allocation) bool { return a.Owner == ipam.Node("dgx-spark") })
	got := fires(t, p, "V-ADDR-003")
	if len(got) != 1 || got[0].Subject != "dgx-spark" || !strings.Contains(got[0].Message, "no overlay address") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("10.42.240.99/32", "overlay", ipam.Node("builder-1"), ""))
	got = fires(t, p, "V-ADDR-003")
	if len(got) != 1 || got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "2 overlay addresses") {
		t.Errorf("%+v", got)
	}
	// A quarantined address is not a held one.
	p = cleanPlan(t, true)
	for i, a := range p.IPAM.Allocations {
		if a.Owner == ipam.Node("aws-hub") {
			p.IPAM.Allocations[i].State = ipam.Quarantined
		}
	}
	fires(t, p, "V-ADDR-003")
}

// TestRule_V_ADDR_004 takes away the transit /30 of the direct link, then gives a probe
// the wrong hosts.
func TestRule_V_ADDR_004(t *testing.T) {
	quiet(t, "V-ADDR-004")
	p := cleanPlan(t, true)
	p.IPAM = withoutAllocation(p.IPAM, func(a ipam.Allocation) bool { return a.Owner == ipam.Link(string(direct)) })
	got := fires(t, p, "V-ADDR-004")
	if got[0].Subject != string(direct) || !strings.Contains(got[0].Message, "transit /30") {
		t.Errorf("%+v", got)
	}
	// The ends swap their probe addresses: the lower host must go to the smaller node.
	p = cleanPlan(t, true)
	c := configOf(p, builder)
	probe := c.Peers[peerIndex(c, dgx)].Probe
	probe.Local, probe.Remote = pfx(probe.Remote.String()+"/30"), probe.Local.Addr()
	got = fires(t, p, "V-ADDR-004")
	if !strings.Contains(got[0].Message, "10.42.252.") {
		t.Errorf("%+v", got)
	}
	// A probe address outside the block.
	p = cleanPlan(t, true)
	c = configOf(p, dgx)
	c.Peers[peerIndex(c, builder)].Probe.Local = pfx("10.99.0.2/30")
	fires(t, p, "V-ADDR-004")
	// Two blocks for one link.
	p = cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("10.42.252.4/30", "transit", ipam.Link(string(direct)), ""))
	fires(t, p, "V-ADDR-004")
}

// TestRule_V_ADDR_005 allocates inside a reserved pool without a reason, with an empty
// reason and with a real one.
func TestRule_V_ADDR_005(t *testing.T) {
	quiet(t, "V-ADDR-005")
	for note, wantFire := range map[string]bool{"": true, "override:": true, "override:   ": true, "just a note": true, "override: a new region, ticket BC-7": false} {
		p := cleanPlan(t, true)
		p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("10.42.128.0/20", "growth", ipam.Site("newregion"), note))
		got := findings(p, "V-ADDR-005")
		if (len(got) > 0) != wantFire {
			t.Errorf("note %q: findings %v, want fire=%v", note, got, wantFire)
		}
		if wantFire && (got[0].Subject != "10.42.128.0/20" || !strings.Contains(got[0].Message, "growth")) {
			t.Errorf("note %q: %+v", note, got)
		}
	}
	// A reserved pool with nothing allocated, and a reserved-state allocation, are fine.
	p := cleanPlan(t, true)
	reserved := alloc("10.42.248.0/24", "infra", ipam.Infra("dns"), "")
	reserved.State = ipam.Reserved
	p.IPAM.Allocations = append(p.IPAM.Allocations, reserved)
	if got := findings(p, "V-ADDR-005"); len(got) != 0 {
		t.Errorf("a reservation was reported: %v", got)
	}
}

// poolWith returns an address plan whose one pool, sixteen /28s, has used of them
// allocated.
func poolWith(t *testing.T, used int) ipam.Snapshot {
	t.Helper()
	registry, err := ipam.New(ipam.Snapshot{Schema: ipam.Schema, Supernet: pfx("10.42.0.0/16"), Pools: []ipam.Pool{{Name: "small", Prefix: pfx("10.42.0.0/24"), ChildBits: 28}}},
		&types.FixedClock{T: time.Unix(0, 0).UTC()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < used; i++ {
		if _, err := registry.Allocate("small", ipam.Site("s"+string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	return registry.Snapshot()
}

// P1-VAL-005
//
// TestRule_V_ADDR_006 checks a pool 81% used warns and one 75% used does not.
func TestRule_V_ADDR_006(t *testing.T) {
	quiet(t, "V-ADDR-006")
	p := cleanPlan(t, true)
	p.IPAM = poolWith(t, 14)
	got := fires(t, p, "V-ADDR-006")
	if got[0].Subject != "small" || !strings.Contains(got[0].Message, "87% used") {
		t.Errorf("%+v", got)
	}
	p.IPAM = poolWith(t, 13)
	fires(t, p, "V-ADDR-006") // 13 of 16 is 81%
	p.IPAM = poolWith(t, 12)
	if got := findings(p, "V-ADDR-006"); len(got) != 0 {
		t.Errorf("a pool 75%% used warned: %v", got)
	}
	p.IPAM = poolWith(t, 0)
	if got := findings(p, "V-ADDR-006"); len(got) != 0 {
		t.Errorf("an empty pool warned: %v", got)
	}
}

// P1-VAL-004
//
// TestRule_V_ADDR_007 plants an allocation in each forbidden range, and one that is wide
// enough to contain one.
func TestRule_V_ADDR_007(t *testing.T) {
	quiet(t, "V-ADDR-007")
	for _, prefix := range []string{"169.254.1.0/24", "127.0.0.1/32", "224.0.0.0/24", "239.1.2.3/32", "0.0.0.0/8", "169.254.0.0/16", "0.1.0.0/16"} {
		p := cleanPlan(t, true)
		p.IPAM.Allocations = append(p.IPAM.Allocations, alloc(prefix, "sites", ipam.Node("bad"), ""))
		got := findings(p, "V-ADDR-007")
		if len(got) == 0 || got[0].Subject != prefix {
			t.Errorf("%s: %v", prefix, got)
		}
	}
	p := cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("160.0.0.0/3", "sites", ipam.Node("wide"), "")) // 128.0.0.0 to 159.255.255.255 is not forbidden, but /3 from 160 holds 169.254/16 and 224/4
	fires(t, p, "V-ADDR-007")
	p = cleanPlan(t, true)
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("240.0.0.0/8", "sites", ipam.Node("future"), ""))
	if got := findings(p, "V-ADDR-007"); len(got) != 0 {
		t.Errorf("240/8 is not in the list: %v", got)
	}
}

// TestRule_V_ADDR_008 checks supernets that overlap a well-known default warn, and the
// reference 10.42.0.0/16 does not.
func TestRule_V_ADDR_008(t *testing.T) {
	quiet(t, "V-ADDR-008")
	for _, supernet := range []string{"10.0.0.0/16", "10.0.0.0/8", "192.168.0.0/16", "192.168.0.0/24", "172.31.0.0/16", "172.16.0.0/12", "10.0.0.0/24"} {
		p := cleanPlan(t, true)
		p.IPAM.Supernet = pfx(supernet)
		if got := findings(p, "V-ADDR-008"); len(got) == 0 || got[0].Subject != supernet {
			t.Errorf("%s: %v", supernet, got)
		}
	}
	for _, fine := range []string{"10.42.0.0/16", "10.64.0.0/10", "192.168.50.0/24", "172.20.0.0/16"} {
		p := cleanPlan(t, true)
		p.IPAM.Supernet = pfx(fine)
		if got := findings(p, "V-ADDR-008"); len(got) != 0 {
			t.Errorf("%s: %v", fine, got)
		}
	}
}

// TestAddressingRulesLeaveTheCleanPlanAlone checks all eight rules together: the clean
// three-site plan has no addressing finding, healthy or not yet proven.
func TestAddressingRulesLeaveTheCleanPlanAlone(t *testing.T) {
	for _, up := range []bool{true, false} {
		for _, f := range findings0(cleanPlan(t, up)) {
			if strings.HasPrefix(f.Rule, "V-ADDR-") {
				t.Errorf("directUp=%v: %v", up, f)
			}
		}
	}
}

// TestRule_V_ADDR_002_overlappingPoolsAreNotLost checks two pools that overlap are reported
// even when there are no allocations, since the address plan refuses to load, while an
// overlap of allocations stays V-ADDR-001's alone.
func TestRule_V_ADDR_002_overlappingPoolsAreNotLost(t *testing.T) {
	p := cleanPlan(t, true)
	p.IPAM = ipam.Snapshot{Schema: ipam.Schema, Supernet: pfx("10.42.0.0/16"), Pools: []ipam.Pool{
		{Name: "a", Prefix: pfx("10.42.0.0/20"), ChildBits: 24},
		{Name: "b", Prefix: pfx("10.42.8.0/21"), ChildBits: 24},
	}}
	got := fires(t, p, "V-ADDR-002")
	if !strings.Contains(got[0].Message, "pool b overlaps pool a") {
		t.Errorf("%+v", got)
	}
	// Pools that overlap and allocations that overlap: both are said, each by its own rule.
	p = cleanPlan(t, true)
	p.IPAM.Pools = append(p.IPAM.Pools, ipam.Pool{Name: "twin", Prefix: pfx("10.42.0.0/20"), ChildBits: 24})
	p.IPAM.Allocations = append(p.IPAM.Allocations, alloc("10.42.16.0/24", "sites", ipam.Node("intruder"), ""))
	fires(t, p, "V-ADDR-002")
	fires(t, p, "V-ADDR-001")
}
