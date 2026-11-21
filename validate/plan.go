package validate

import (
	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// Plan is everything the rules look at: the final plan, as plain data. The planner
// builds one from what it has made, and a test builds one and plants a fault in it,
// which is how every rule is tested. The validator never changes a Plan.
//
// Example:
//
//	plan := &validate.Plan{Topology: topo, IPAM: registry.Snapshot(), Graph: graph,
//		Input: input, Tables: result.Tables, Configs: configs, Flows: flows}
//	res := validate.Validate(plan, validate.Options{})
type Plan struct {
	// Topology is the network as described.
	//
	// Example:
	//
	//	the decoded three-site file.
	Topology *topology.Topology
	// IPAM is the address plan as saved. The rules read the allocations and the
	// pools from it, and rebuild a registry from it to check the accounting.
	//
	// Example:
	//
	//	registry.Snapshot().
	IPAM ipam.Snapshot
	// Unusable lists the links health says are not usable, which the graph has
	// marked down. A link in this list is a link whose routes were not computed.
	//
	// Example:
	//
	//	[]types.LinkID{"builder-1~dgx-spark"} on a fresh plan.
	Unusable []types.LinkID
	// Graph is the network the routes were computed on, with the unusable links
	// marked down. The sweep and the simulation use it.
	//
	// Example:
	//
	//	the graph handed to route.Compute.
	Graph route.Graph
	// Input is what the routing engine was given: who owns which prefixes, and any
	// static overrides.
	//
	// Example:
	//
	//	route.Input{Owned: owned, Local: local}.
	Input route.Input
	// Tables are the routing tables of every node.
	//
	// Example:
	//
	//	the Tables of route.Compute.
	Tables route.Tables
	// Unreachable lists the pairs of nodes the routing engine found no path between
	// (its Result.Unreachable), so a flow between two cut-off nodes is judged cut
	// off and not a black hole.
	//
	// Example:
	//
	//	the Unreachable of route.Compute.
	Unreachable []route.Pair
	// Configs are the generated configs, one for each node.
	//
	// Example:
	//
	//	the result of config.Generate.
	Configs []*config.NodeConfig
	// Flows are what to simulate. With none, the simulation rules have nothing to
	// check and pass.
	//
	// Example:
	//
	//	the result of sim.Flows.
	Flows []sim.Flow
	// Scenarios are the declared what-if runs whose expectations must hold.
	//
	// Example:
	//
	//	a scenario that fails the direct link and expects every flow OK.
	Scenarios []sim.Scenario
}
