package tests

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
)

// loopyNetwork returns the three-site network with a bad static override planted
// on the hub: traffic for dgx-spark's site is sent to builder-1. While the direct
// link is up that is harmless for builder-1, which sends it straight to
// dgx-spark. Once the direct link fails builder-1 sends it to the hub, which
// sends it back: a loop that only failover creates.
func loopyNetwork(t *testing.T) failover.Network {
	t.Helper()
	net := threeSite(t)
	net.Input.Overrides = []route.Override{{Node: hub, Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: builder}}
	return net
}

// TestFailoverThatWouldLoopIsRejected is P1-FAIL-026: the direct link fails, the
// new tables would make traffic to dgx-spark's site loop, so nothing is installed.
// The previous tables stay, the rejection names the broken flow, the health change
// is still recorded, and the decision says it was not installed.
func TestFailoverThatWouldLoopIsRejected(t *testing.T) {
	net := loopyNetwork(t)
	start := healthy(t, net)
	if len(start.RejectedOverrides) != 0 {
		t.Fatalf("the planted override was refused by the engine: %+v", start.RejectedOverrides)
	}
	result := down(t, net, start.State)
	if result.Rejected == nil {
		t.Fatalf("the failover was installed; the simulation says %+v", result.Report.Summary.Verdicts)
	}
	if len(result.Rejected.Failures) == 0 || !strings.Contains(result.Rejected.Reason, "previous tables stay") {
		t.Errorf("rejection %+v", result.Rejected)
	}
	loops := 0
	for _, f := range result.Rejected.Failures {
		if f.Verdict == sim.VerdictLoop {
			loops++
		}
	}
	if loops == 0 {
		t.Errorf("no failure is a loop: %+v", result.Rejected.Failures)
	}
	if diff := route.Diff(start.Tables, result.Tables); len(diff) != 0 {
		t.Errorf("the tables changed although the failover was rejected: %+v", diff)
	}
	if diff := route.Diff(start.Tables, result.State.Tables); len(diff) != 0 {
		t.Errorf("the state's tables are not the previous ones: %+v", diff)
	}
	if len(result.Changes) != 0 {
		t.Errorf("changes %+v, want none", result.Changes)
	}
	if got := linkState(t, result.State, direct); got != health.Down {
		t.Errorf("the direct link is %s: the health change is real and must be recorded", got)
	}
	for _, d := range result.Decisions {
		if d.Installed || len(d.Activated)+len(d.Withdrawn) != 0 {
			t.Errorf("decision %+v claims routes moved", d)
		}
	}
}

// TestRejectionIsNotStuck checks the guard judges each evaluation afresh: once
// the bad override is gone from the network the same health state installs.
func TestRejectionIsNotStuck(t *testing.T) {
	bad := loopyNetwork(t)
	start := healthy(t, bad)
	rejected := down(t, bad, start.State)
	if rejected.Rejected == nil {
		t.Fatal("expected a rejection")
	}
	fixed := threeSite(t)
	result := evaluate(t, fixed, rejected.State, nil, 55)
	if result.Rejected != nil {
		t.Fatalf("still rejected after the override was removed: %+v", result.Rejected)
	}
	if len(result.Changes) == 0 || !result.Report.Passed {
		t.Errorf("the failover was not installed: %d changes, passed %v", len(result.Changes), result.Report.Passed)
	}
	if r := overlayFlow(t, result.Report, builder, dgx); r.Forward.Crossings() != 2 {
		t.Errorf("builder-1 to dgx-spark crosses %d links, want 2 after the failover is installed", r.Forward.Crossings())
	}
}

// TestFlowsThatWereAlreadyBrokenDoNotBlockAFailover checks the guard compares
// with the previous state, not with perfection: the planted override already
// makes two flows fail before anything fails (the hub reaches dgx-spark's site
// through builder-1 and the answer comes back another way), and those are not
// held against a failover that breaks nothing new.
func TestFlowsThatWereAlreadyBrokenDoNotBlockAFailover(t *testing.T) {
	net := loopyNetwork(t)
	start := healthy(t, net)
	if start.Report.Summary.Failed != 2 {
		t.Fatalf("before the failure %d flows fail, want the 2 asymmetric site flows: %+v", start.Report.Summary.Failed, start.Report.Summary.Verdicts)
	}
	result := evaluate(t, net, start.State, probes(hubD, false, 1, 35), 40)
	if result.Rejected != nil {
		t.Errorf("rejected although nothing new broke: %+v", result.Rejected)
	}
	if got := linkState(t, result.State, hubD); got != health.Down {
		t.Errorf("the hub link is %s, want Down", got)
	}
	if len(result.Changes) == 0 {
		t.Error("the failover was not installed")
	}
}
