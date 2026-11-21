package tests

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
	"github.com/vectorihub/netctl/validate"
)

// The nodes and links of the three-site network.
const (
	hub     = types.NodeID("aws-hub")
	builder = types.NodeID("builder-1")
	dgx     = types.NodeID("dgx-spark")
	direct  = types.LinkID("builder-1~dgx-spark")
	hubB    = types.LinkID("aws-hub~builder-1")
	hubD    = types.LinkID("aws-hub~dgx-spark")
)

// pfx parses a prefix, panicking on bad text because test data is fixed.
var pfx = netip.MustParsePrefix

// cleanPlan builds the three-site plan as the planner would, with the direct link
// healthy (directUp) or not yet proven, so that a rule can be tested on a plan that is
// right and on the same plan with one fault planted in it. Each call builds a new plan
// that shares nothing with another.
func cleanPlan(t testing.TB, directUp bool) *validate.Plan {
	t.Helper()
	topo := fixture.ThreeSiteTopology()
	registry, err := fixture.ThreeSitePlan()
	if err != nil {
		t.Fatal(err)
	}
	var unusable []types.LinkID
	if !directUp {
		unusable = []types.LinkID{direct}
	}
	graph := fixture.FromTopology(topology.BuildGraph(topo, func(id types.LinkID) bool { return directUp || id != direct }))
	input := fixture.ThreeSiteInput()
	computed, err := route.Compute(graph, input)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := config.FromPlan(topo, registry, computed.Tables)
	if err != nil {
		t.Fatal(err)
	}
	configs, err := config.Generate(inputs)
	if err != nil {
		t.Fatal(err)
	}
	flows, err := sim.Flows(topo, registry, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return &validate.Plan{
		Topology: topo, IPAM: registry.Snapshot(), Unusable: unusable, Graph: graph, Input: input,
		Tables: computed.Tables, Configs: configs, Flows: flows,
	}
}

// findings returns the findings of one rule on the plan, with no suppressions.
func findings(p *validate.Plan, rule string) []validate.Finding {
	var out []validate.Finding
	for _, f := range validate.Validate(p, validate.Options{}).Findings {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

// quiet fails the test if the rule fires on the clean plan, healthy or not yet proven:
// every rule must also keep silent where nothing is wrong.
func quiet(t *testing.T, rule string) {
	t.Helper()
	for _, up := range []bool{true, false} {
		if got := findings(cleanPlan(t, up), rule); len(got) != 0 {
			t.Errorf("%s fires on the clean plan (directUp=%v): %v", rule, up, got)
		}
	}
}

// fires fails the test unless the rule fires on the plan, and returns what it found.
func fires(t *testing.T, p *validate.Plan, rule string) []validate.Finding {
	t.Helper()
	got := findings(p, rule)
	if len(got) == 0 {
		t.Fatalf("%s did not fire", rule)
	}
	return got
}

// configOf returns the plan's config for a node.
func configOf(p *validate.Plan, node types.NodeID) *config.NodeConfig {
	for _, c := range p.Configs {
		if c.Node == node {
			return c
		}
	}
	return nil
}

// peerIndex returns the index of the config's peer for a node, or -1.
func peerIndex(c *config.NodeConfig, node types.NodeID) int {
	return slices.IndexFunc(c.Peers, func(p config.Peer) bool { return p.Node == node })
}

// withoutAllocation returns the snapshot without the allocations for which drop is true.
func withoutAllocation(s ipam.Snapshot, drop func(ipam.Allocation) bool) ipam.Snapshot {
	s.Allocations = slices.DeleteFunc(slices.Clone(s.Allocations), drop)
	return s
}

// findings0 returns every finding on the plan, with no suppressions.
func findings0(p *validate.Plan) []validate.Finding {
	return validate.Validate(p, validate.Options{}).Findings
}
