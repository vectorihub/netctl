package tests

import (
	"github.com/vectorihub/netctl/failover"
	"net/netip"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// hubLink is the link between the hub and builder-1, the way builder-1 leaves when the
// direct link is not in use.
const hubLink = types.LinkID("aws-hub~builder-1")

// probeSignals returns n probes from each end of the direct link, five seconds apart from
// the given second, all reporting ok.
func probeSignals(ok bool, n, from int) []health.Signal {
	var out []health.Signal
	for i := 0; i < n; i++ {
		for _, end := range []types.NodeID{builder, dgx} {
			out = append(out, health.Signal{Link: direct, From: end, Kind: health.SignalProbe, OK: ok, At: at(from + 5*i)})
		}
	}
	return out
}

// planAt plans with the clock standing n seconds after t0.
func planAt(t testing.TB, in plan.Inputs, n int) plan.Result {
	t.Helper()
	in.Clock = &types.FixedClock{T: at(n)}
	return mustPlan(t, in)
}

// viaOf returns the next hop of the route for the prefix in a node's table.
func viaOf(t testing.TB, r plan.Result, node types.NodeID, prefix string) string {
	t.Helper()
	for _, rt := range r.Tables[node].FIB() {
		if rt.Prefix == netip.MustParsePrefix(prefix) {
			return string(rt.Via)
		}
	}
	t.Fatalf("%s has no route to %s", node, prefix)
	return ""
}

// TestSignalsPromoteTheDirectLinkThroughTheGuard is the failover path of the planner: a
// first plan with probes proves the direct link and the two machines talk directly, and the
// plan carries the failover state to save.
func TestSignalsPromoteTheDirectLinkThroughTheGuard(t *testing.T) {
	in := fresh(t)
	in.Signals = probeSignals(true, 5, 5)
	r := planAt(t, in, 30)
	if r.Failover == nil || len(r.Failover.State.Links) != 3 {
		t.Fatalf("the plan has no failover state: %+v", r.Failover)
	}
	if len(r.Unusable) != 0 {
		t.Errorf("unusable %v, want the direct link proven", r.Unusable)
	}
	if via := viaOf(t, r, builder, "10.42.240.3/32"); via != string(direct) {
		t.Errorf("builder-1 reaches dgx-spark via %s, want the direct link", via)
	}
}

// TestADownLinkFailsOverAndTheStateCarriesOn is P1-FAIL-020 through the planner: the plan
// that proved the direct link is followed by one that hears it fail, and the traffic moves
// to the hub, with the state of the first plan as the starting point of the second.
func TestADownLinkFailsOverAndTheStateCarriesOn(t *testing.T) {
	in := fresh(t)
	in.Signals = probeSignals(true, 5, 5)
	first := planAt(t, in, 30)

	next := fresh(t)
	next.IPAM, next.Previous = first.IPAM, &first
	next.Failover = &first.Failover.State
	next.Signals = probeSignals(false, 3, 35)
	second := planAt(t, next, 50)

	if len(second.Unusable) != 1 || second.Unusable[0] != direct {
		t.Fatalf("unusable %v, want the direct link", second.Unusable)
	}
	if via := viaOf(t, second, builder, "10.42.240.3/32"); via != string(hubLink) {
		t.Errorf("builder-1 reaches dgx-spark via %s, want the hub", via)
	}
	if !second.Validation.OK || len(second.Configs) != 3 {
		t.Errorf("the failed-over plan: ok %v, %d configs", second.Validation.OK, len(second.Configs))
	}
	if second.PlanID == first.PlanID {
		t.Errorf("the plan ID did not change although the routes did: %s", second.PlanID)
	}
}

// TestWithoutSignalsThePlannerReadsHealthAlone checks the old path is untouched: no failover
// state in the result when the guard was not asked for.
func TestWithoutSignalsThePlannerReadsHealthAlone(t *testing.T) {
	in := fresh(t)
	in.Health = evaluator(t, true)
	if r := mustPlan(t, in); r.Failover != nil {
		t.Errorf("the guard ran without signals or a state: %+v", r.Failover)
	}
}

// TestAFailoverThatBreaksAFlowIsRejectedAndTheRoutesStay is P1-FAIL-026 through the planner:
// a static override added in the same plan as the failure would make traffic loop once the
// direct link is gone, so the guard keeps the routes of the plan in force, the plan still
// succeeds, and a FailoverRejected decision says why.
func TestAFailoverThatBreaksAFlowIsRejectedAndTheRoutesStay(t *testing.T) {
	override := []route.Override{{Node: hub, Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: builder}}
	in := fresh(t)
	in.Signals = probeSignals(true, 5, 5)
	first := planAt(t, in, 30)
	if first.Failover == nil || !first.Validation.OK {
		t.Fatalf("the baseline must be a good plan: %+v", first.Validation.Errors())
	}

	next := fresh(t)
	next.Overrides = override
	next.IPAM, next.Previous, next.Failover = first.IPAM, &first, &first.Failover.State
	next.Signals = probeSignals(false, 3, 35)
	next.Clock = &types.FixedClock{T: at(50)}
	second, err := plan.Plan(next)
	var rejected bool
	for _, d := range second.Decisions {
		rejected = rejected || (d.Kind == "FailoverRejected" && strings.Contains(d.Reason, "previous tables stay"))
	}
	if err != nil || second.Failover == nil || second.Failover.Rejected == nil || !rejected {
		t.Fatalf("no FailoverRejected decision (err %v, decisions %+v)", err, second.Decisions)
	}
	if via := viaOf(t, second, builder, "10.42.240.3/32"); via != string(direct) {
		t.Errorf("the rejected failover still moved traffic: builder-1 reaches dgx-spark via %s", via)
	}
}

// TestALinkMadeRequiredMustProveItselfInASavedState checks the topology's word beats the
// saved state's: a state saved when the direct link was only assumed (and so usable while
// Unknown) must not let the planner route through the link once the topology requires it to
// be proven, and the caller's state is left untouched.
func TestALinkMadeRequiredMustProveItselfInASavedState(t *testing.T) {
	start := fresh(t)
	start.Signals = probeSignals(true, 1, 5) // one probe is not proof
	first := planAt(t, start, 30)
	if first.Failover == nil {
		t.Fatal("no failover state")
	}
	saved := first.Failover.State
	for _, l := range saved.Links {
		if l.ID == direct {
			l.Required = false // as saved before the topology asked for proof
		}
	}
	in := fresh(t)
	in.IPAM, in.Failover = first.IPAM, &saved
	r := mustPlan(t, in)
	if len(r.Unusable) != 1 || r.Unusable[0] != direct {
		t.Errorf("unusable %v: an unproven link the topology requires must not be used", r.Unusable)
	}
	for _, l := range saved.Links {
		if l.ID == direct && l.Required {
			t.Error("the caller's state was changed")
		}
	}
}

// TestAMalformedSavedStateIsRefusedNotACrash checks a state built by hand with a missing link
// or a missing direction fails the plan with INVALID_TOPOLOGY instead of panicking.
func TestAMalformedSavedStateIsRefusedNotACrash(t *testing.T) {
	for name, links := range map[string][]*health.Link{
		"nil link":  {nil},
		"no sides":  {{ID: direct}},
		"nil sides": {{ID: direct, Dirs: map[types.NodeID]*health.Direction{builder: nil, dgx: nil}}},
	} {
		in := fresh(t)
		in.Failover = &failover.State{Links: links}
		if _, err := plan.Plan(in); !types.HasCode(err, types.ErrInvalidTopology) {
			t.Errorf("%s: want INVALID_TOPOLOGY, got %v", name, err)
		}
	}
}
