// Package consumer is a program that depends on netctl the way vhb-server or the agent
// will: from outside the module, through the root package, with the network built in code.
package consumer

import (
	"net/netip"
	"time"

	"github.com/vectorihub/netctl"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// ThreeSite builds the three-site network in Go values: an AWS hub and two spokes behind
// NAT, the two hub links and a direct link between the spokes that must be proven. The
// address plan is inline, so the first plan needs no saved state. It cannot fail: the
// values are fixed.
//
// Example:
//
//	topo := consumer.ThreeSite() // 3 nodes, 3 links
func ThreeSite() *topology.Topology {
	all, _ := topology.ParseSelector("*")
	hub, _ := topology.ParseSelector("aws-hub")
	return &topology.Topology{
		Schema: topology.Schema,
		Name:   "consumer-three-site",
		Addressing: topology.Addressing{
			Supernet: netip.MustParsePrefix("10.42.0.0/16"),
			Pools: []topology.AddressPool{
				{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
				{Name: "growth", Prefix: netip.MustParsePrefix("10.42.128.0/18"), ChildBits: 20, Reserved: true},
				{Name: "overlay", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
				{Name: "infra", Prefix: netip.MustParsePrefix("10.42.248.0/22"), Reserved: true},
				{Name: "transit", Prefix: netip.MustParsePrefix("10.42.252.0/22"), ChildBits: 30},
			},
		},
		Sites: []topology.Site{
			{ID: "aws", Provider: "aws", Region: "eu-west-1"},
			{ID: "builder", Provider: "owned"},
			{ID: "dgx", Provider: "owned"},
		},
		Nodes: []topology.Node{
			{ID: "aws-hub", Site: "aws", Role: types.RoleHub, NAT: topology.NATNone, Endpoints: []netip.AddrPort{netip.MustParseAddrPort("203.0.113.10:51820")}, Advertises: []topology.Advertisement{{Site: true}}},
			{ID: "builder-1", Site: "builder", Role: types.RoleSpoke, NAT: topology.NATBehind, Advertises: []topology.Advertisement{{Site: true}}},
			{ID: "dgx-spark", Site: "dgx", Role: types.RoleSpoke, NAT: topology.NATBehind, Advertises: []topology.Advertisement{{Site: true}}},
		},
		Links: []topology.Link{
			{Kind: types.LinkHubSpoke, A: "aws-hub", B: "builder-1"},
			{Kind: types.LinkHubSpoke, A: "aws-hub", B: "dgx-spark"},
			{Kind: types.LinkDirect, A: "builder-1", B: "dgx-spark", Cost: 10, Health: topology.HealthRequired},
		},
		Intents: []topology.Intent{{Action: topology.ActionAllow, From: all, To: hub}},
	}
}

// PlanThreeSite plans the network once, with the clock fixed so the answer is the same on
// every machine. It returns the error of netctl.Plan, which has a code from the types
// package (for example VALIDATION_FAILED when the validator refuses the plan).
//
// Example:
//
//	result, err := consumer.PlanThreeSite()
func PlanThreeSite() (netctl.Result, error) {
	return netctl.Plan(netctl.Inputs{
		Topology: ThreeSite(),
		Clock:    &types.FixedClock{T: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)},
	})
}
