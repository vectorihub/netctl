package plan

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
	"github.com/vectorihub/netctl/validate"
)

// Pool names of the standard address plan: the pools sites and machines are numbered from.
const (
	// SitesPool is the pool site blocks come from.
	//
	// Example:
	//
	//	plan.SitesPool is "sites".
	SitesPool ipam.PoolName = "sites"
	// OverlayPool is the pool machine overlay addresses come from.
	//
	// Example:
	//
	//	plan.OverlayPool is "overlay".
	OverlayPool ipam.PoolName = "overlay"
)

// Inputs is everything Plan needs. It holds the state of the network (the address plan,
// the health of the links, the plan in force) and the clock, and reads nothing else.
//
// Example:
//
//	result, err := plan.Plan(plan.Inputs{Topology: topo, IPAM: snapshot, Clock: clock})
type Inputs struct {
	// Topology is the network as described. Plan does not change it.
	//
	// Example:
	//
	//	the decoded three-site file.
	Topology *topology.Topology
	// IPAM is the address plan as last saved. A zero snapshot starts a new plan from
	// the topology's own addressing section.
	//
	// Example:
	//
	//	the Result.IPAM of the last plan.
	IPAM ipam.Snapshot
	// Health is the health of the links. Nil on a first plan: every link starts
	// Unknown, so a direct link that must be proven is not used and the hub carries
	// the traffic. Links are carried over by ID; one the evaluator does not know starts
	// Unknown. Plan does not change it.
	//
	// Example:
	//
	//	the evaluator after a day of probes.
	Health *health.Evaluator
	// Previous is the plan in force, or nil. A new plan that fails validation when
	// Previous passed is rejected, and Previous stays in force.
	//
	// Example:
	//
	//	the Result of the last plan.
	Previous *Result
	// Clock supplies the time for the address plan's quarantine. It is required.
	//
	// Example:
	//
	//	&types.FixedClock{T: t0}.
	Clock types.Clock
	// Quarantine is how long a released address stays unusable. Zero means
	// ipam.DefaultQuarantine.
	//
	// Example:
	//
	//	24 * time.Hour.
	Quarantine time.Duration
	// Overrides are static routes to apply on top of the computed ones.
	//
	// Example:
	//
	//	route.Override{Node: "aws-hub", Prefix: p, NextHop: "builder-1"}.
	Overrides []route.Override
	// Scenarios are what-if runs whose expectations must hold, for the validator.
	//
	// Example:
	//
	//	a scenario that fails the direct link and expects every flow OK.
	Scenarios []sim.Scenario
	// Validate changes how the validator runs: extra suppressions, or no sweep.
	//
	// Example:
	//
	//	validate.Options{SkipSweep: true}.
	Validate validate.Options
}

// Change is something a plan did to the address plan.
//
// Example:
//
//	Change{Kind: "allocated", Subject: "node:builder-1", Prefix: 10.42.240.2/32}.
type Change struct {
	// Kind is "allocated" for a new allocation, "released" for one given back (it is
	// quarantined, not free) and "expired" for a quarantine that has ended. An address never
	// moves: a rename is a "released" and an "allocated".
	//
	// Example:
	//
	//	"released".
	Kind string `json:"kind"`
	// Subject is the owner: "node:builder-1", "site:aws", "link:builder-1~dgx-spark".
	//
	// Example:
	//
	//	"node:builder-1".
	Subject string `json:"subject"`
	// Prefix is the block concerned.
	//
	// Example:
	//
	//	10.42.240.2/32.
	Prefix netip.Prefix `json:"prefix"`
}

// Decision is something the planner decided that the caller must know.
//
// Example:
//
//	Decision{Kind: "FailoverRejected", Reason: "the new plan fails validation; keeping three-site@7f3c9e1a"}.
type Decision struct {
	// Kind names the decision. "FailoverRejected" means the new plan failed validation
	// when the plan in force passed, and the plan in force is kept. "AddressNotCarried"
	// says a machine that left and one that joined in the same plan are two machines to
	// netctl: the old address is quarantined and the new name gets a new one.
	//
	// Example:
	//
	//	"FailoverRejected".
	Kind string `json:"kind"`
	// Reason says why in words.
	//
	// Example:
	//
	//	"the new plan fails validation; keeping three-site@7f3c9e1a".
	Reason string `json:"reason"`
	// Findings are the validation errors behind it.
	//
	// Example:
	//
	//	the V-SIM-001 error for builder-1 to dgx-spark.
	Findings []validate.Finding `json:"findings,omitempty"`
}

// Result is a plan.
//
// Example:
//
//	result.Configs holds one config for each node; result.Validation.OK says the plan stands.
type Result struct {
	// PlanID names the plan: the topology's name and eight hex digits of a hash of every
	// node's content. Empty when there are no configs.
	//
	// Example:
	//
	//	"three-site@7f3c9e1a".
	PlanID string `json:"plan_id"`
	// IPAM is the address plan to save. After a failure in the allocation steps it is
	// the snapshot the plan was given, unchanged.
	//
	// Example:
	//
	//	ipam.Marshal(result.IPAM).
	IPAM ipam.Snapshot `json:"ipam"`
	// Tables are the routing tables of every node.
	//
	// Example:
	//
	//	result.Tables["builder-1"].FIB().
	Tables route.Tables `json:"-"`
	// Unreachable lists the pairs with no healthy path.
	//
	// Example:
	//
	//	builder-1 to dgx-spark while both hub links are down.
	Unreachable []route.Pair `json:"unreachable"`
	// Unusable lists the links health says are not in use, sorted.
	//
	// Example:
	//
	//	["builder-1~dgx-spark"] on a fresh plan.
	Unusable []types.LinkID `json:"unusable"`
	// Configs are the configs, one for each node, sorted by node. They are empty when
	// validation failed and there was no plan in force; when there was, they are the
	// plan in force's.
	//
	// Example:
	//
	//	three configs for the three-site network.
	Configs []*config.NodeConfig `json:"-"`
	// Validation is what the validator found.
	//
	// Example:
	//
	//	result.Validation.OK.
	Validation validate.Result `json:"validation"`
	// Simulation is every flow sent over the tables.
	//
	// Example:
	//
	//	result.Simulation.Passed.
	Simulation sim.Report `json:"simulation"`
	// Changes are the changes made to the address plan, sorted by prefix.
	//
	// Example:
	//
	//	every allocation of a first plan.
	Changes []Change `json:"changes"`
	// Decisions are what the planner decided that the caller must know.
	//
	// Example:
	//
	//	a FailoverRejected when the new plan was refused.
	Decisions []Decision `json:"decisions"`
}

// probed reports whether a link is a direct link whose health must be proven, which is
// the kind that gets a transit /30 and probes.
func probed(l topology.Link) bool {
	return l.Kind == types.LinkDirect && (l.Health == topology.HealthRequired || l.Health == topology.HealthUnset)
}

// cloneTopology returns a copy of the topology that can be normalised without changing the
// original: the top-level lists are copied, which is what Normalize reorders and edits, and
// so are the peers of each node, which importing them clears.
func cloneTopology(t *topology.Topology) (*topology.Topology, error) {
	out := *t
	out.Sites = slices.Clone(t.Sites)
	out.Links = slices.Clone(t.Links)
	out.Nodes = make([]topology.Node, len(t.Nodes))
	for i, n := range t.Nodes {
		n.Peers = slices.Clone(n.Peers)
		out.Nodes[i] = n
	}
	out.Intents = slices.Clone(t.Intents)
	if err := out.ImportPeers(); err != nil {
		return nil, err
	}
	out.Normalize()
	return &out, nil
}

// snapshotFrom builds an empty address plan from the topology's addressing section. It
// refuses a section that is only a reference, since netctl reads no file.
func snapshotFrom(a topology.Addressing) (ipam.Snapshot, error) {
	if len(a.Pools) == 0 || !a.Supernet.IsValid() {
		return ipam.Snapshot{}, types.Errf(types.ErrNotFound, "addressing", "there is no address plan: the snapshot is empty and the topology's addressing has no inline supernet and pools (a reference %q is not read)", a.Ref)
	}
	s := ipam.Snapshot{Schema: ipam.Schema, Supernet: a.Supernet}
	for _, p := range a.Pools {
		s.Pools = append(s.Pools, ipam.Pool{Name: ipam.PoolName(p.Name), Prefix: p.Prefix, ChildBits: p.ChildBits, Reserved: p.Reserved, SkipFirst: p.SkipFirst})
	}
	return s, nil
}

// problem turns any error into a coded one, so it can go in a types.List.
func problem(err error) *types.Error {
	var coded *types.Error
	if errors.As(err, &coded) {
		return coded
	}
	return types.Errf(types.ErrInvalidValue, "plan", "%v", err)
}

// healthLinks builds the health links for every link of the topology, taking the state of
// each from the evaluator by ID and starting the rest Unknown. The evaluator is not changed.
func healthLinks(t *topology.Topology, from *health.Evaluator) (*health.Evaluator, error) {
	cfg := health.DefaultConfig()
	if from != nil {
		cfg = from.Cfg
	}
	var links []*health.Link
	for _, l := range t.Links {
		id := types.LinkIDOf(l.A, l.B)
		if from != nil {
			if old, ok := from.Links[id]; ok {
				// The observations are carried over; whether the link must be proven is
				// the topology's word today, not the evaluator's from before. A link
				// changed from assumed to required must prove itself.
				kept := old.Clone()
				kept.Required = probed(l)
				links = append(links, kept)
				continue
			}
		}
		fresh, err := health.NewLink(id, l.A, l.B, probed(l))
		if err != nil {
			return nil, err
		}
		links = append(links, fresh)
	}
	return health.NewEvaluator(cfg, links...)
}

// Plan turns the inputs into a plan, running the pipeline in the order the package
// documentation gives.
//
// It returns an error with the result in three cases. A failure in the allocation steps
// (a damaged snapshot, a pool that is full, an overlap) is returned with every problem
// at once and a Result that holds only the snapshot it was given. A config that cannot
// be generated is returned as it is. A plan the validator refuses is returned with its
// full validation report, no configs, and VALIDATION_FAILED; if the plan in force passed,
// the result keeps its configs and tables and carries a FailoverRejected decision.
//
// It does not change its inputs.
//
// Example:
//
//	result, err := plan.Plan(plan.Inputs{Topology: topo, IPAM: snapshot, Clock: clock})
//	if err != nil { return err }
//	for _, c := range result.Configs { write(c) }
func Plan(in Inputs) (Result, error) {
	if in.Topology == nil || in.Clock == nil {
		return Result{IPAM: in.IPAM}, types.Errf(types.ErrInvalidValue, "Plan", "it needs a topology and a clock, got nil")
	}
	t, err := cloneTopology(in.Topology)
	if err != nil {
		return Result{IPAM: in.IPAM}, err
	}
	snapshot := in.IPAM
	if snapshot.Schema == "" && len(snapshot.Allocations) == 0 && len(snapshot.Pools) == 0 {
		if snapshot, err = snapshotFrom(t.Addressing); err != nil {
			return Result{IPAM: in.IPAM}, err
		}
	}
	quarantine := in.Quarantine
	if quarantine == 0 {
		quarantine = ipam.DefaultQuarantine
	}

	registry, err := ipam.New(snapshot, in.Clock, quarantine) // 1
	if err != nil {
		return Result{IPAM: in.IPAM}, err
	}
	if err := reconcileAddresses(registry, t); err != nil { // 2 to 5
		return Result{IPAM: in.IPAM}, err
	}
	result := Result{IPAM: registry.Snapshot(), Changes: diff(snapshot, registry.Snapshot())}
	result.Decisions = renames(result.Changes, quarantine)

	evaluator, err := healthLinks(t, in.Health) // 6
	if err != nil {
		return failed(in, result, err)
	}
	result.Unusable = evaluator.UnusableLinks()
	down := map[types.LinkID]bool{}
	for _, id := range result.Unusable {
		down[id] = true
	}
	graph := adapt(topology.BuildGraph(t, func(id types.LinkID) bool { return !down[id] })) // 7

	announcers := make([]route.Announcer, 0, len(t.Nodes))
	for _, n := range t.Nodes {
		a := route.Announcer{Node: n.ID, Site: n.Site}
		for _, advert := range n.Advertises {
			if advert.Site {
				a.AdvertiseSite = true
			} else if advert.Prefix.IsValid() {
				a.Prefixes = append(a.Prefixes, advert.Prefix)
			}
		}
		announcers = append(announcers, a)
	}
	var probedLinks []types.LinkID
	for _, l := range t.Links {
		if probed(l) {
			probedLinks = append(probedLinks, types.LinkIDOf(l.A, l.B))
		}
	}
	owned, local, err := route.Collect(registry, announcers, probedLinks)
	if err != nil {
		return failed(in, result, err)
	}
	input := route.Input{Owned: owned, Local: local, Overrides: in.Overrides}
	computed, err := route.Compute(graph, input) // 8
	if err != nil {
		return failed(in, result, err)
	}
	result.Tables, result.Unreachable = computed.Tables, computed.Unreachable

	generate, err := config.FromPlan(t, registry, computed.Tables) // 9
	if err != nil {
		return failed(in, result, err)
	}
	generate.Health = evaluator.Cfg
	configs, err := config.Generate(generate)
	if err != nil {
		return failed(in, result, err)
	}
	flows, err := sim.Flows(t, registry, sim.Options{})
	if err != nil {
		return failed(in, result, err)
	}
	result.Simulation = sim.Simulate(graph, computed.Tables, input, computed.Unreachable, flows)

	result.Validation = validate.Validate(&validate.Plan{ // 10
		Topology: t, IPAM: result.IPAM, Unusable: result.Unusable, Graph: graph, Input: input, Tables: computed.Tables,
		Unreachable: computed.Unreachable, Configs: configs, Flows: flows, Scenarios: in.Scenarios,
	}, in.Validate)

	if result.Validation.OK {
		result.Configs = configs
		if len(configs) > 0 {
			result.PlanID = configs[0].PlanID
		}
		return result, nil
	}
	return guard(in.Previous, result) // 11
}

// failed returns the result of a plan that could not get as far as the validator: the
// address changes it made, and the error. No configs were made.
func failed(in Inputs, result Result, err error) (Result, error) {
	result.Changes = nil
	result.IPAM = in.IPAM
	return result, err
}

// String describes a change on one line.
//
// Example:
//
//	"allocated node:builder-1 10.42.240.2/32".
func (c Change) String() string { return fmt.Sprintf("%s %s %s", c.Kind, c.Subject, c.Prefix) }
