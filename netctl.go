package netctl

import (
	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/plan"
)

// Inputs is everything [Plan] needs: the network as described, the address plan and link
// health as last saved, the plan in force, and the clock. It is [plan.Inputs].
//
// Example:
//
//	netctl.Inputs{Topology: topo, IPAM: snapshot, Clock: clock}
type Inputs = plan.Inputs

// Result is a plan: the address plan to save, the routing tables, one config for every node,
// the validator's verdict, the simulation, the changes made and the decisions the planner
// took. It is [plan.Result]. Its Render method gives the bytes of every output file.
//
// Example:
//
//	files, err := result.Render()
type Result = plan.Result

// Change is something a plan did to the address plan. It is [plan.Change].
//
// Example:
//
//	for _, c := range result.Changes { fmt.Println(c) }
type Change = plan.Change

// Decision is something the planner decided that the caller must know. It is
// [plan.Decision].
//
// Example:
//
//	for _, d := range result.Decisions { fmt.Println(d.Kind, d.Reason) }
type Decision = plan.Decision

// ConfigDiff is the difference between the configs of two plans, node by node. It is
// [config.PlanDiff].
//
// Example:
//
//	d := netctl.Diff(before, after); if !d.Empty() { fmt.Print(d.Text()) }
type ConfigDiff = config.PlanDiff

// Plan turns a description of the network and its state into a plan, running the whole
// pipeline: addresses, routes, a config for every node, validation and the failover guard.
// It reads no file and asks no clock but the one it is given, so the same inputs give the
// same bytes. See [plan.Plan] for the order of operations and the three ways it can fail.
//
// Example:
//
//	result, err := netctl.Plan(netctl.Inputs{Topology: topo, IPAM: snapshot, Clock: clock})
//	if err != nil { return err }
//	files, _ := result.Render()
func Plan(in Inputs) (Result, error) { return plan.Plan(in) }

// Diff compares what two plans would put on the machines, node by node. An empty diff means
// the machines would see no change.
//
// Example:
//
//	d := netctl.Diff(before, after)
//	if d.Empty() { fmt.Println("no change") }
func Diff(old, updated Result) ConfigDiff { return plan.Diff(old, updated) }
