package plan

import (
	"slices"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// evaluate runs the failover guard for a plan. It starts from the failover state it was
// given, or from the links of the health evaluator when there is none, folds the signals
// in at the plan's clock, and returns what the guard decided. The network it evaluates has
// every link present: which ones are down is the guard's business.
func evaluate(in Inputs, t *topology.Topology, from *health.Evaluator, input route.Input, flows []sim.Flow) (failover.Result, error) {
	var prev failover.State
	if in.Failover != nil {
		prev = *in.Failover
		prev.Links = nil
		for _, l := range in.Failover.Links {
			prev.Links = append(prev.Links, copyLink(l))
		}
	} else {
		for _, id := range sortedLinkIDs(from) {
			prev.Links = append(prev.Links, from.Links[id].Clone())
		}
	}
	required := map[types.LinkID]bool{}
	for _, l := range t.Links {
		if probed(l) {
			required[types.LinkIDOf(l.A, l.B)] = true
		}
	}
	// Whether a link must be proven is the topology's word today, not the saved state's
	// from before: a link changed from assumed to required must prove itself. The links
	// are copies, so the caller's state is left as it was.
	for _, l := range prev.Links {
		if wellFormed(l) {
			l.Required = required[l.ID]
		}
	}
	network := failover.Network{
		Graph:    adapt(topology.BuildGraph(t, func(types.LinkID) bool { return true })),
		Input:    input,
		Required: required,
		Flows:    flows,
		Health:   from.Cfg,
	}
	return failover.Evaluate(network, prev, in.Signals, in.Clock.Now())
}

// sortedLinkIDs returns the IDs of the evaluator's links in order.
func sortedLinkIDs(e *health.Evaluator) []types.LinkID {
	ids := make([]types.LinkID, 0, len(e.Links))
	for id := range e.Links {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// unusable lists the links the failover state says are not in use, sorted.
func unusable(s failover.State, cfg health.Config) []types.LinkID {
	e, err := health.NewEvaluator(cfg, s.Links...)
	if err != nil {
		return nil
	}
	return e.UnusableLinks()
}

// wellFormed reports whether a saved link can be copied and edited safely: it exists and has
// both directions, each with a state. Anything else is left for failover.Evaluate to refuse
// with INVALID_TOPOLOGY, which reports every problem of a saved state at once.
func wellFormed(l *health.Link) bool {
	if l == nil || len(l.Dirs) != 2 {
		return false
	}
	for _, d := range l.Dirs {
		if d == nil {
			return false
		}
	}
	return true
}

// copyLink returns a copy of a saved link so the planner can edit it, or the link itself when
// it is malformed, since the guard refuses it before anything reads it.
func copyLink(l *health.Link) *health.Link {
	if !wellFormed(l) {
		return l
	}
	return l.Clone()
}
