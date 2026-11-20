package fixture

import (
	"net/netip"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// PlanClock is the fixed clock the address-plan fixtures use.
var PlanClock = &types.FixedClock{T: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}

// NewPlan returns an empty address plan with the pools of the reference plan:
// sites /20 from 10.42.0.0/17, overlay /32 from 10.42.240.0/21 (the first
// address kept free), transit /30 from 10.42.252.0/22, and the reserved growth
// and infra pools.
func NewPlan() (*ipam.IPAM, error) {
	return ipam.New(ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: netip.MustParsePrefix("10.42.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
			{Name: "growth", Prefix: netip.MustParsePrefix("10.42.128.0/18"), ChildBits: 20, Reserved: true},
			{Name: "overlay", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
			{Name: "infra", Prefix: netip.MustParsePrefix("10.42.248.0/22"), Reserved: true},
			{Name: "transit", Prefix: netip.MustParsePrefix("10.42.252.0/22"), ChildBits: 30},
		},
	}, PlanClock, ipam.DefaultQuarantine)
}

// ThreeSitePlan returns the address plan of the three-site network: a /20 for
// each of the sites aws, builder and dgx (10.42.0.0/20, 10.42.16.0/20 and
// 10.42.32.0/20), an overlay /32 for each node (10.42.240.1 to .3) and the
// transit /30 of the direct link (10.42.252.0/30).
func ThreeSitePlan() (*ipam.IPAM, error) {
	registry, err := NewPlan()
	if err != nil {
		return nil, err
	}
	for _, site := range []string{"aws", "builder", "dgx"} {
		if _, err := registry.Allocate("sites", ipam.Site(site)); err != nil {
			return nil, err
		}
	}
	for _, node := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		if _, err := registry.Allocate("overlay", ipam.Node(node)); err != nil {
			return nil, err
		}
	}
	if _, err := registry.AllocateTransit("builder-1~dgx-spark"); err != nil {
		return nil, err
	}
	return registry, nil
}

// selector parses a selector, panicking on bad text because fixture data is fixed.
func selector(text string) topology.Selector {
	parsed, err := topology.ParseSelector(text)
	if err != nil {
		panic(err)
	}
	return parsed
}

// ThreeSiteTopology returns the reference topology in normal form, built in
// code: three sites, a hub and two spokes, the two hub links and the direct
// link, and the four intents of the reference file. Every node advertises its
// site, the hub included, as the engineering plan's tables assume.
func ThreeSiteTopology() *topology.Topology {
	t := &topology.Topology{
		Schema: topology.Schema,
		Name:   "three-site",
		Sites: []topology.Site{
			{ID: "aws", Provider: "aws", Region: "eu-west-1"},
			{ID: "builder", Provider: "owned"},
			{ID: "dgx", Provider: "owned"},
		},
		Nodes: []topology.Node{
			{ID: "aws-hub", Site: "aws", Role: types.RoleHub, NAT: topology.NATNone, Advertises: []topology.Advertisement{{Site: true}}},
			{ID: "builder-1", Site: "builder", Role: types.RoleSpoke, NAT: topology.NATBehind, Advertises: []topology.Advertisement{{Site: true}}},
			{ID: "dgx-spark", Site: "dgx", Role: types.RoleSpoke, NAT: topology.NATBehind, Advertises: []topology.Advertisement{{Site: true}}},
		},
		Links: []topology.Link{
			{Kind: types.LinkHubSpoke, A: "aws-hub", B: "builder-1"},
			{Kind: types.LinkHubSpoke, A: "aws-hub", B: "dgx-spark"},
			{Kind: types.LinkDirect, A: "builder-1", B: "dgx-spark", Cost: 10, Health: topology.HealthRequired},
		},
		Intents: []topology.Intent{
			{Action: topology.ActionAllow, From: selector("builder-1"), To: selector("dgx-spark")},
			{Action: topology.ActionAllow, From: selector("dgx-spark"), To: selector("builder-1")},
			{Action: topology.ActionAllow, From: selector("*"), To: selector("aws-hub")},
			{Action: topology.ActionDeny, From: selector("site:builder"), To: selector("site:aws"), Ports: []uint16{5432}},
		},
	}
	t.Normalize()
	return t
}
