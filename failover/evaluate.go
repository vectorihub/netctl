package failover

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// Network is the network Evaluate works on: the graph with every link as the
// plan has it, what each node advertises, and the flows the simulation guard
// checks before it lets a failover in. It holds no health: health is the State.
//
// Example:
//
//	failover.Network{
//		Graph:    fixture.ThreeSite(true),
//		Input:    fixture.ThreeSiteInput(),
//		Required: map[types.LinkID]bool{"builder-1~dgx-spark": true},
//	}
type Network struct {
	// Graph is the network with every link present. A link the graph already
	// marks down stays down whatever its health.
	Graph route.Graph
	// Input is what the routing engine needs: who owns which prefixes, and any
	// static overrides.
	Input route.Input
	// Required lists the links that must be proven before they carry traffic,
	// which are the direct ones; RequiredLinks reads it from a topology. Any
	// other link is assumed to work until a failure arrives. It only matters the
	// first time Evaluate sees a link: a link already in the State keeps the
	// choice it was made with.
	Required map[types.LinkID]bool
	// Flows are simulated over the new tables before they are installed. With
	// none, the guard has nothing to check and lets every failover in.
	Flows []sim.Flow
	// Health holds the thresholds and times. The zero value means
	// health.DefaultConfig.
	Health health.Config
	// Compute is the routing computation; nil means route.Compute. It exists so
	// a test can count the calls. It is not a way to substitute another routing
	// algorithm.
	Compute sim.ComputeFunc
}

// RequiredLinks returns the links of the topology whose health must be proven by
// independent signals (health: required), ready for Network.Required.
//
// Example:
//
//	network.Required = failover.RequiredLinks(topo)
func RequiredLinks(t *topology.Topology) map[types.LinkID]bool {
	required := map[types.LinkID]bool{}
	for _, link := range t.Links {
		if link.Health == topology.HealthRequired {
			required[types.LinkIDOf(link.A, link.B)] = true
		}
	}
	return required
}

// NodeRoute is a route and the node whose table it is in.
//
// Example:
//
//	NodeRoute{Node: "builder-1", Route: r} is the route r in builder-1's table.
type NodeRoute struct {
	// Node is the node whose table holds the route.
	Node types.NodeID `json:"node"`
	// Route is the route.
	route.Route
}

// Decision records a change in a link's health and what it did to the routes.
//
// Example:
//
//	when the direct link goes Down, one Decision names it, says Healthy to Down and
//	why, and lists the backups that became active on builder-1 and dgx-spark.
type Decision struct {
	// Link is the link that changed.
	Link types.LinkID `json:"link"`
	// Before and After are the link's health on either side of the change.
	Before health.LinkHealth `json:"before"`
	// After is the health the link has now.
	After health.LinkHealth `json:"after"`
	// Activated lists the routes that became active because of this change on
	// any node: backups promoted when the link went away, primaries when it
	// came back.
	Activated []NodeRoute `json:"activated,omitempty"`
	// Withdrawn lists the active routes that stopped being active because of
	// this change: primaries that crossed a link that went away, backups that
	// gave way when it came back.
	Withdrawn []NodeRoute `json:"withdrawn,omitempty"`
	// Installed says the route changes are in force. It is false when the
	// guard rejected the failover: the health change is real, but the old
	// tables stayed, so Activated and Withdrawn are empty.
	Installed bool `json:"installed"`
	// Reason says in words what caused it and what it did to the routes.
	Reason string `json:"reason"`
	// At is when the change happened.
	At time.Time `json:"at"`
}

// Failure is a flow the guard found broken in the new tables.
//
// Example:
//
//	Failure{Flow: "builder-1→dgx-spark (overlay)", Verdict: sim.VerdictLoop, Detail: "..."}
type Failure struct {
	// Flow is the flow's label.
	Flow string `json:"flow"`
	// Verdict is what the simulation made of it.
	Verdict sim.Verdict `json:"verdict"`
	// Detail says what went wrong.
	Detail string `json:"detail"`
}

// FailoverRejected says the guard refused to install the new tables because the
// simulation showed they made things worse: flows that worked before are broken
// by them. The tables from before stay in force.
//
// Example:
//
//	a failover that makes builder-1 to dgx-spark loop is rejected, with that flow listed.
type FailoverRejected struct {
	// Reason says it in words.
	Reason string `json:"reason"`
	// Failures are the flows the new tables break that the old ones did not.
	Failures []Failure `json:"failures"`
}

// Result is what Evaluate returns.
//
// Example:
//
//	result, err := failover.Evaluate(network, state, signals, now)
//	save(result.State); install(result.Tables)
type Result struct {
	// State is the state to carry into the next evaluation and to save.
	State State
	// Tables are the routing tables to install: the new ones, or, when
	// Rejected is set, the ones from before.
	Tables route.Tables
	// Decisions lists each link whose health changed, in order, with the
	// routes it moved.
	Decisions []Decision
	// Changes is every difference between the tables from before and Tables,
	// whether or not a decision accounts for it.
	Changes []route.TableDiff
	// Report is the simulation of the new tables over the network as health
	// now has it, whether or not they were installed.
	Report sim.Report
	// Unreachable lists the pairs with no healthy path in Tables.
	Unreachable []route.Pair
	// RejectedOverrides lists static overrides the routing engine refused.
	RejectedOverrides []route.Rejection
	// Skipped lists the signals that could not be used, with the reason.
	Skipped []health.Skipped
	// Rejected is set when the guard refused the failover; nil otherwise.
	Rejected *FailoverRejected
}

// marked is a graph with some links marked down.
type marked struct {
	// base is the network.
	base route.Graph
	// down holds the links marked down on top of what base says.
	down map[types.LinkID]bool
}

// Nodes returns the nodes of the underlying graph.
//
// Example:
//
//	the nodes of a marked graph are those of the graph it marks.
func (g marked) Nodes() []types.NodeID { return g.base.Nodes() }

// Role returns the role of the node in the underlying graph.
//
// Example:
//
//	marking a link down does not change any node's role.
func (g marked) Role(node types.NodeID) types.Role { return g.base.Role(node) }

// Edges returns the node's edges with the marked links down.
//
// Example:
//
//	with the direct link marked, both builder-1's edge to dgx-spark and the
//	reverse edge report Down, and the hub edges do not.
func (g marked) Edges(node types.NodeID) []route.Edge {
	edges := g.base.Edges(node)
	out := make([]route.Edge, len(edges))
	for i, edge := range edges {
		edge.Down = edge.Down || g.down[edge.Link]
		out[i] = edge
	}
	return out
}

// downSet returns the links as a set.
func downSet(links []types.LinkID) map[types.LinkID]bool {
	set := make(map[types.LinkID]bool, len(links))
	for _, link := range links {
		set[link] = true
	}
	return set
}

// linkEnds returns the two nodes of every link of the graph, sorted, keyed by
// link ID.
func linkEnds(graph route.Graph) map[types.LinkID][2]types.NodeID {
	ends := map[types.LinkID][2]types.NodeID{}
	for _, node := range graph.Nodes() {
		for _, edge := range graph.Edges(node) {
			a, b := node, edge.To
			if b < a {
				a, b = b, a
			}
			if _, seen := ends[edge.Link]; !seen {
				ends[edge.Link] = [2]types.NodeID{a, b}
			}
		}
	}
	return ends
}

// links returns the links Evaluate works on: every link of the graph, taken from
// the previous state if it is there and made new (Unknown) if not. A link in the
// state that the graph no longer has is dropped: its timers are about something
// that is gone. The links are copies; the state is not touched.
func (n Network) links(prev State, ends map[types.LinkID][2]types.NodeID) ([]*health.Link, error) {
	ids := make([]types.LinkID, 0, len(ends))
	for id := range ends {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]*health.Link, 0, len(ids))
	for _, id := range ids {
		if l, ok := prev.Link(id); ok {
			out = append(out, l.Clone())
			continue
		}
		l, err := health.NewLink(id, ends[id][0], ends[id][1], n.Required[id])
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// simulate runs every flow over the tables on the graph and reports.
func (n Network) simulate(graph route.Graph, tables route.Tables, unreachable []route.Pair) sim.Report {
	net := sim.NewNet(graph, tables, n.Input.Owned, n.Input.Local)
	cutOff := map[[2]types.NodeID]bool{}
	for _, pair := range unreachable {
		cutOff[[2]types.NodeID{pair.From, pair.To}] = true
	}
	results := make([]sim.FlowResult, 0, len(n.Flows))
	for _, flow := range n.Flows {
		results = append(results, net.Run(flow, cutOff[[2]types.NodeID{flow.From, flow.To}]))
	}
	return sim.NewReport(results)
}

// failing returns the flows of the report that fail the gate, by label.
func failing(report sim.Report) map[string]sim.FlowResult {
	out := map[string]sim.FlowResult{}
	for _, result := range report.Flows {
		if result.Verdict.FailsGate() {
			out[result.Flow.Label] = result
		}
	}
	return out
}

// Evaluate folds new signals into the links' health, recomputes every node's
// routing table through the real routing engine, and says what changed and why.
// One call is one evaluation: the signals are folded in as one batch, the tables
// are recomputed once, and the result is the same however the signals were
// split, as long as they are in time order.
//
// The steps are:
//
//  1. Fold the signals into each link's state machine, using only the times in
//     the signals and now (the package never reads the clock).
//  2. Mark down every link that health says is not usable.
//  3. Compute all the tables over that graph, with route.Compute.
//  4. Compare them with the tables from before and turn each link's change into a
//     [Decision] with the routes it activated and withdrew.
//  5. Simulate the new tables over the graph as it now is. If they break a flow
//     that worked before, install nothing: keep the old tables and say why in
//     Result.Rejected. A failover is never installed if it makes things worse.
//
// "Worse" means a flow that failed no gate in the previous state, the previous
// tables over the previous graph, and does in the new one. A flow that was
// already broken, or that is cut off because no healthy path exists, is not a
// reason to reject: cutting off a node whose links all failed is the correct
// answer. With no Flows in the Network there is nothing to check.
//
// The first evaluation, from the zero State, has no previous tables, so it
// computes them once from the links as they were, which makes it call the engine
// twice. Every later one calls it once.
//
// Evaluate does not change prev. It returns UNSUPPORTED_SCHEMA for a State of
// another version, INVALID_CONFIG for bad thresholds, INVALID_TOPOLOGY for an
// incoherent State or a missing graph, and the routing engine's error if it
// fails; on any error nothing is returned and nothing has changed.
//
// Example:
//
//	result, err := failover.Evaluate(network, state, signals, now)
//	if err != nil { return err }
//	state = result.State
//	install(result.Tables)
func Evaluate(net Network, prev State, signals []health.Signal, now time.Time) (Result, error) {
	if net.Graph == nil {
		return Result{}, types.Errf(types.ErrInvalidTopology, "failover", "there is no network to evaluate")
	}
	if prev.Schema != "" && prev.Schema != Schema {
		return Result{}, types.Errf(types.ErrUnsupportedSchema, "failover state", "schema %q is not %q", prev.Schema, Schema)
	}
	if err := prev.check(); err != nil {
		return Result{}, err
	}
	cfg := net.Health
	if cfg == (health.Config{}) {
		cfg = health.DefaultConfig()
	}
	compute := net.Compute
	if compute == nil {
		compute = route.Compute
	}
	ends := linkEnds(net.Graph)
	links, err := net.links(prev, ends)
	if err != nil {
		return Result{}, err
	}
	then := make([]*health.Link, len(links))
	for i, l := range links {
		then[i] = l.Clone()
	}
	before, err := health.NewEvaluator(cfg, then...)
	if err != nil {
		return Result{}, err
	}
	after, err := health.NewEvaluator(cfg, links...)
	if err != nil {
		return Result{}, err
	}
	healthDecisions, skipped := after.Apply(signals, now)
	wasUnusable, nowUnusable := before.UnusableLinks(), after.UnusableLinks()

	oldGraph := marked{net.Graph, downSet(wasUnusable)}
	newGraph := marked{net.Graph, downSet(nowUnusable)}
	computed, err := compute(newGraph, net.Input)
	if err != nil {
		return Result{}, fmt.Errorf("failover: recomputing routes: %w", err)
	}
	oldTables, oldUnreachable := prev.Tables, prev.Unreachable
	if oldTables == nil {
		first, err := compute(oldGraph, net.Input)
		if err != nil {
			return Result{}, fmt.Errorf("failover: computing the starting routes: %w", err)
		}
		oldTables, oldUnreachable = first.Tables, first.Unreachable
	}

	result := Result{
		Tables: computed.Tables, Unreachable: computed.Unreachable, RejectedOverrides: computed.Rejected, Skipped: skipped,
		Report: net.simulate(newGraph, computed.Tables, computed.Unreachable),
	}
	// The previous state is only simulated when the new tables fail something:
	// if nothing fails, nothing can have got worse.
	if broken := failing(result.Report); len(broken) > 0 {
		before := failing(net.simulate(oldGraph, oldTables, oldUnreachable))
		if worse := newlyBroken(before, broken); len(worse) > 0 {
			result.Rejected = &FailoverRejected{
				Reason: fmt.Sprintf("the new tables break %d flow(s) that worked before, so the previous tables stay in force: %s",
					len(worse), worse[0].Flow+" is "+worse[0].Verdict.String()),
				Failures: worse,
			}
			result.Tables, result.Unreachable = oldTables, oldUnreachable
		}
	}
	result.Changes = route.Diff(oldTables, result.Tables)
	result.Decisions = explain(healthDecisions, result.Changes, result.Rejected == nil, wasUnusable, nowUnusable, ends, requiredOf(links))

	state := State{Schema: Schema, EvaluatedAt: now, Links: links, Tables: result.Tables, Unreachable: result.Unreachable}
	slices.SortFunc(state.Links, func(a, b *health.Link) int { return strings.Compare(string(a.ID), string(b.ID)) })
	result.State = state
	return result, nil
}

// requiredOf returns which of the links are required, keyed by ID.
func requiredOf(links []*health.Link) map[types.LinkID]bool {
	out := make(map[types.LinkID]bool, len(links))
	for _, l := range links {
		out[l.ID] = l.Required
	}
	return out
}

// newlyBroken returns the flows that fail now and did not fail before, sorted by
// label, each with what went wrong.
func newlyBroken(before, now map[string]sim.FlowResult) []Failure {
	var out []Failure
	for label, result := range now {
		if _, was := before[label]; was {
			continue
		}
		var detail []string
		for _, problem := range result.Problems {
			detail = append(detail, problem.Detail)
		}
		out = append(out, Failure{Flow: label, Verdict: result.Verdict, Detail: strings.Join(detail, "; ")})
	}
	slices.SortFunc(out, func(a, b Failure) int { return strings.Compare(a.Flow, b.Flow) })
	return out
}

// move is one route's change on one node, as the table diff reports it: the
// route as it was and as it is, either of which may be absent.
type move struct {
	// node is the node whose table changed.
	node types.NodeID
	// old and updated are the route before and after; nil when it did not
	// exist on that side.
	old, updated *route.Route
}

// key identifies what a move is about: one prefix on one node.
type key struct {
	// node and prefix say where.
	node   types.NodeID
	prefix string
}

// moves flattens table diffs into one list of moves, in diff order.
func moves(diffs []route.TableDiff) []move {
	var out []move
	for _, diff := range diffs {
		for i := range diff.Added {
			out = append(out, move{node: diff.Node, updated: &diff.Added[i]})
		}
		for i := range diff.Removed {
			out = append(out, move{node: diff.Node, old: &diff.Removed[i]})
		}
		for i := range diff.Changed {
			out = append(out, move{node: diff.Node, old: &diff.Changed[i].Old, updated: &diff.Changed[i].New})
		}
	}
	return out
}

// crosses reports whether the path goes over the link, whose ends are given.
func crosses(path []types.NodeID, ends [2]types.NodeID) bool {
	for i := 0; i+1 < len(path); i++ {
		a, b := path[i], path[i+1]
		if (a == ends[0] && b == ends[1]) || (a == ends[1] && b == ends[0]) {
			return true
		}
	}
	return false
}

// explain turns the health decisions into failover decisions: each link whose
// usability changed gets the routes that crossed it, and the routes that took
// their place, or that gave way to them.
//
// When a link stopped being usable, the routes withdrawn for it are the active
// routes that crossed it and are gone or inactive now; those activated are the
// ones that took over the same prefixes on the same nodes. When a link became
// usable it is the other way round: the routes activated are the active ones that
// cross it, and those withdrawn are the ones they replaced. A change of health
// that does not change usability, such as Healthy to Degraded, moves no routes.
func explain(decisions []health.Decision, changes []route.TableDiff, installed bool, wasUnusable, nowUnusable []types.LinkID, ends map[types.LinkID][2]types.NodeID, required map[types.LinkID]bool) []Decision {
	all := moves(changes)
	var withdrawn, activated []move
	for _, m := range all {
		if m.old != nil && m.old.Active && (m.updated == nil || !m.updated.Active) {
			withdrawn = append(withdrawn, m)
		}
		if m.updated != nil && m.updated.Active && (m.old == nil || !m.old.Active) {
			activated = append(activated, m)
		}
	}
	was, now := downSet(wasUnusable), downSet(nowUnusable)
	// A batch can take one link through several states (Healthy, Degraded, Down).
	// The routes move once, when usability flips, so they belong to the last
	// decision that flipped it the way the link ended up.
	flips := map[types.LinkID]int{}
	for i, d := range decisions {
		if d.Before.Usable(required[d.Link]) != d.After.Usable(required[d.Link]) {
			flips[d.Link] = i
		}
	}
	out := make([]Decision, 0, len(decisions))
	for i, d := range decisions {
		decision := Decision{Link: d.Link, Before: d.Before, After: d.After, Reason: d.Reason, At: d.At, Installed: installed}
		if at, flipped := flips[d.Link]; installed && flipped && at == i {
			switch {
			case now[d.Link] && !was[d.Link]:
				decision.Withdrawn, decision.Activated = pair(withdrawn, activated, ends[d.Link], oldRoute, newRoute)
			case was[d.Link] && !now[d.Link]:
				decision.Activated, decision.Withdrawn = pair(activated, withdrawn, ends[d.Link], newRoute, oldRoute)
			}
			if len(decision.Withdrawn)+len(decision.Activated) > 0 {
				decision.Reason += fmt.Sprintf(": %d active route(s) withdrawn, %d activated", len(decision.Withdrawn), len(decision.Activated))
			}
		}
		out = append(out, decision)
	}
	return out
}

// oldRoute and newRoute pick the route as it was and as it is from a move. They
// are only called for a move that has that side.
func oldRoute(m move) route.Route { return *m.old }

// newRoute is the route as it is now.
func newRoute(m move) route.Route { return *m.updated }

// pair picks, from the lead moves, those whose route (given by leadRoute) crosses
// the link whose ends are given, and from the rest those on the same node and
// prefix as one of them, shown as restRoute gives them. The routes of the first
// list go with lead and those of the second with rest.
func pair(lead, rest []move, ends [2]types.NodeID, leadRoute, restRoute func(move) route.Route) ([]NodeRoute, []NodeRoute) {
	var first, second []NodeRoute
	seen := map[key]bool{}
	for _, m := range lead {
		r := leadRoute(m)
		if !crosses(r.Path, ends) {
			continue
		}
		first = append(first, NodeRoute{Node: m.node, Route: r})
		seen[key{m.node, r.Prefix.String()}] = true
	}
	for _, m := range rest {
		if r := restRoute(m); seen[key{m.node, r.Prefix.String()}] {
			second = append(second, NodeRoute{Node: m.node, Route: r})
		}
	}
	return first, second
}
