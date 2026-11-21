package tests

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// nodesOf returns the nodes that appear in the routes, sorted, without repeats.
func nodesOf(routes []failover.NodeRoute) []types.NodeID {
	var nodes []types.NodeID
	for _, r := range routes {
		if !slices.Contains(nodes, r.Node) {
			nodes = append(nodes, r.Node)
		}
	}
	slices.Sort(nodes)
	return nodes
}

// overlayFlow returns the result of the overlay flow between two nodes.
func overlayFlow(t *testing.T, report sim.Report, from, to types.NodeID) sim.FlowResult {
	t.Helper()
	for _, r := range report.Flows {
		if r.Flow.Family == sim.FamilyOverlay && r.Flow.From == from && r.Flow.To == to {
			return r
		}
	}
	t.Fatalf("no overlay flow from %s to %s", from, to)
	return sim.FlowResult{}
}

// TestFirstEvaluation checks the zero State is a start: every link of the graph
// is tracked, the required direct link is not usable until proven and the
// assumed hub links are, the tables are those for that, the engine is called
// twice (the starting tables, then the new ones), and a later evaluation calls it
// once.
func TestFirstEvaluation(t *testing.T) {
	net := threeSite(t)
	c := &counting{}
	net.Compute = c.compute
	result := evaluate(t, net, failover.State{}, nil, 0)
	if len(result.State.Links) != 3 || result.State.Schema != failover.Schema {
		t.Fatalf("state: %d links, schema %q", len(result.State.Links), result.State.Schema)
	}
	if got := linkState(t, result.State, direct); got != health.Unknown {
		t.Errorf("the direct link is %s, want Unknown", got)
	}
	if c.calls != 2 {
		t.Errorf("the first evaluation called the engine %d times, want 2", c.calls)
	}
	// Direct is required and unproven, so builder-1 reaches dgx-spark by the hub.
	for _, r := range result.Tables[builder].FIB() {
		if r.Prefix == netip.MustParsePrefix("10.42.240.3/32") && r.Via != hubB {
			t.Errorf("before the direct link is proven builder-1 reaches dgx-spark via %s, want %s", r.Via, hubB)
		}
	}
	if len(result.Decisions) != 0 || len(result.Changes) != 0 || result.Rejected != nil || !result.Report.Passed {
		t.Errorf("a quiet first evaluation: decisions %v, changes %v, rejected %v, passed %v",
			result.Decisions, result.Changes, result.Rejected, result.Report.Passed)
	}
	c.calls = 0
	evaluate(t, net, result.State, nil, 5)
	if c.calls != 1 {
		t.Errorf("a later evaluation called the engine %d times, want 1", c.calls)
	}
}

// TestDirectLinkDownActivatesBackupsOnBothEnds is P1-FAIL-020: when the direct
// link goes Down the decision lists the backups activated on Builder and on DGX
// Spark, in the same evaluation, and the primaries withdrawn on both.
func TestDirectLinkDownActivatesBackupsOnBothEnds(t *testing.T) {
	net := threeSite(t)
	start := healthy(t, net)
	result := down(t, net, start.State)
	// Healthy to Degraded at 35 s moves no routes; Degraded to Down at 45 s does.
	if len(result.Decisions) != 2 {
		t.Fatalf("decisions %+v, want two", result.Decisions)
	}
	if first := result.Decisions[0]; first.After != health.Degraded || len(first.Activated)+len(first.Withdrawn) != 0 {
		t.Errorf("the first decision %+v, want Degraded with no route moved", first)
	}
	d := result.Decisions[1]
	if d.Link != direct || d.Before != health.Degraded || d.After != health.Down || !d.Installed || !d.At.Equal(at(45)) {
		t.Errorf("decision %+v", d)
	}
	if got := nodesOf(d.Activated); !slices.Equal(got, []types.NodeID{builder, dgx}) {
		t.Errorf("backups activated on %v, want builder-1 and dgx-spark", got)
	}
	if got := nodesOf(d.Withdrawn); !slices.Equal(got, []types.NodeID{builder, dgx}) {
		t.Errorf("primaries withdrawn on %v, want builder-1 and dgx-spark", got)
	}
	for _, r := range d.Activated {
		if r.Via != hubB && r.Via != hubD {
			t.Errorf("activated %s on %s goes via %s, want a hub link", r.Prefix, r.Node, r.Via)
		}
		if !r.Active {
			t.Errorf("activated %s on %s is not active", r.Prefix, r.Node)
		}
	}
	for _, r := range d.Withdrawn {
		if r.Via != direct {
			t.Errorf("withdrawn %s on %s went via %s, want the direct link", r.Prefix, r.Node, r.Via)
		}
	}
	if !strings.Contains(d.Reason, "withdrawn") || !strings.Contains(d.Reason, "activated") {
		t.Errorf("the reason %q does not say what happened to the routes", d.Reason)
	}
}

// TestSimulationAfterFailoverIsSymmetricViaTheHub is P1-FAIL-021: after the direct
// link fails every flow is OK and builder-1 and dgx-spark talk by way of the hub,
// the same way both directions.
func TestSimulationAfterFailoverIsSymmetricViaTheHub(t *testing.T) {
	net := threeSite(t)
	result := down(t, net, healthy(t, net).State)
	if !result.Report.Passed || result.Report.Summary.Failed != 0 {
		t.Fatalf("after failover: passed %v, %+v", result.Report.Passed, result.Report.Summary.Verdicts)
	}
	for _, pair := range [][2]types.NodeID{{builder, dgx}, {dgx, builder}} {
		r := overlayFlow(t, result.Report, pair[0], pair[1])
		if r.Verdict != sim.VerdictOK || !r.Symmetric || r.Forward.Crossings() != 2 {
			t.Errorf("%s to %s: %s, symmetric %v, %d crossings; want OK, symmetric, 2 (via the hub)",
				pair[0], pair[1], r.Verdict, r.Symmetric, r.Forward.Crossings())
		}
		if !slices.Contains(r.Forward.Nodes(), hub) {
			t.Errorf("%s to %s did not pass through the hub: %v", pair[0], pair[1], r.Forward.Nodes())
		}
	}
}

// TestFailbackReactivatesPrimariesOnBothSides is P1-FAIL-022: when the direct link
// recovers fully the primaries come back on both sides in one evaluation, the
// backups give way, and the simulation is OK over the direct link.
func TestFailbackReactivatesPrimariesOnBothSides(t *testing.T) {
	net := threeSite(t)
	failed := down(t, net, healthy(t, net).State)
	result := evaluate(t, net, failed.State, probes(direct, true, 5, 110), 131)
	var back failover.Decision
	for _, d := range result.Decisions {
		if d.After == health.Healthy {
			back = d
		}
	}
	if back.Link != direct || back.Before != health.Recovering {
		t.Fatalf("decisions %+v, want one that makes the direct link Healthy", result.Decisions)
	}
	if got := nodesOf(back.Activated); !slices.Equal(got, []types.NodeID{builder, dgx}) {
		t.Errorf("primaries reactivated on %v, want both builder-1 and dgx-spark", got)
	}
	for _, r := range back.Activated {
		if r.Via != direct {
			t.Errorf("reactivated %s on %s goes via %s, want the direct link", r.Prefix, r.Node, r.Via)
		}
	}
	if got := nodesOf(back.Withdrawn); !slices.Equal(got, []types.NodeID{builder, dgx}) {
		t.Errorf("backups withdrawn on %v, want both", got)
	}
	if r := overlayFlow(t, result.Report, builder, dgx); r.Verdict != sim.VerdictOK || r.Forward.Crossings() != 1 {
		t.Errorf("after failback builder-1 to dgx-spark: %s over %d links, want OK over the direct link", r.Verdict, r.Forward.Crossings())
	}
	if !result.Report.Passed {
		t.Errorf("the simulation after failback failed: %+v", result.Report.Summary.Verdicts)
	}
}

// TestRecoveryInOneDirectionOnlyDoesNotFailBack is P1-FAIL-023: builder-1 hears
// dgx-spark again but not the other way round, so the link stays Down, nothing is
// reactivated, and the tables do not change.
func TestRecoveryInOneDirectionOnlyDoesNotFailBack(t *testing.T) {
	net := threeSite(t)
	failed := down(t, net, healthy(t, net).State)
	signals := join(probes(direct, true, 5, 110, builder), probes(direct, false, 5, 110, dgx))
	result := evaluate(t, net, failed.State, signals, 135)
	if got := linkState(t, result.State, direct); got != health.Down {
		t.Fatalf("the link is %s, want Down", got)
	}
	if len(result.Changes) != 0 {
		t.Errorf("the tables changed: %+v", result.Changes)
	}
	for _, d := range result.Decisions {
		if len(d.Activated) != 0 {
			t.Errorf("a decision reactivated routes: %+v", d)
		}
	}
	if r := overlayFlow(t, result.Report, builder, dgx); r.Forward.Crossings() != 2 {
		t.Errorf("builder-1 to dgx-spark crosses %d links, want 2: still by way of the hub", r.Forward.Crossings())
	}
}

// TestHubLinkDownLeavesTheDirectPath is P1-FAIL-024: the link between builder-1
// and the hub goes Down while the direct link is Healthy. builder-1 still reaches
// dgx-spark directly, flows from builder-1 to the hub have no path, and the
// decision explains what it withdrew.
func TestHubLinkDownLeavesTheDirectPath(t *testing.T) {
	net := threeSite(t)
	start := healthy(t, net)
	signals := join(probes(hubB, false, 1, 35), probes(direct, true, 3, 35))
	result := evaluate(t, net, start.State, signals, 45)
	if got := linkState(t, result.State, hubB); got != health.Down {
		t.Fatalf("the hub link is %s, want Down", got)
	}
	if got := linkState(t, result.State, direct); got != health.Healthy {
		t.Fatalf("the direct link is %s, want Healthy", got)
	}
	if r := overlayFlow(t, result.Report, builder, dgx); r.Verdict != sim.VerdictOK || r.Forward.Crossings() != 1 {
		t.Errorf("builder-1 to dgx-spark: %s over %d links, want OK over the direct link", r.Verdict, r.Forward.Crossings())
	}
	if r := overlayFlow(t, result.Report, builder, hub); r.Verdict != sim.VerdictNoHealthyPath {
		t.Errorf("builder-1 to the hub: %s, want NO_HEALTHY_PATH", r.Verdict)
	}
	var d failover.Decision
	for _, decision := range result.Decisions {
		if decision.Link == hubB {
			d = decision
		}
	}
	if d.After != health.Down || len(d.Withdrawn) == 0 || !strings.Contains(d.Reason, "probe") || !strings.Contains(d.Reason, "withdrawn") {
		t.Errorf("the decision does not explain itself: %+v", d)
	}
	for _, r := range d.Withdrawn {
		if r.Via != hubB {
			t.Errorf("withdrew %s on %s via %s, want only routes over the failed link", r.Prefix, r.Node, r.Via)
		}
	}
	if !result.Report.Passed {
		t.Errorf("no flow should fail the gate: %+v", result.Report.Summary.Verdicts)
	}
}

// TestBuilderIsolated is P1-FAIL-025: both links of builder-1 are Down. It is cut
// off; every flow that starts or ends there is reported as having no healthy
// path, and nothing loops or black-holes for the other pairs.
func TestBuilderIsolated(t *testing.T) {
	net := threeSite(t)
	start := healthy(t, net)
	signals := join(probes(direct, false, 3, 35), probes(hubB, false, 1, 35), probes(hubD, true, 3, 35))
	result := evaluate(t, net, start.State, signals, 50)
	if result.Rejected != nil {
		t.Fatalf("isolating a node is the right answer, not a reason to reject: %+v", result.Rejected)
	}
	involved := 0
	for _, r := range result.Report.Flows {
		if r.Flow.From == builder || r.Flow.To == builder {
			involved++
			if r.Verdict != sim.VerdictNoHealthyPath && r.Verdict != sim.VerdictUnreachableExpected {
				t.Errorf("%s: %s, want the flow reported as cut off", r.Flow.Label, r.Verdict)
			}
			continue
		}
		if r.Verdict.FailsGate() {
			t.Errorf("%s: %s: a flow between the other nodes failed", r.Flow.Label, r.Verdict)
		}
		if r.Verdict == sim.VerdictLoop || r.Verdict == sim.VerdictBlackHole {
			t.Errorf("%s: %s", r.Flow.Label, r.Verdict)
		}
	}
	if involved == 0 {
		t.Fatal("no flow involves builder-1")
	}
	if !result.Report.Passed {
		t.Errorf("the report failed: %+v", result.Report.Summary.Verdicts)
	}
	pairs := 0
	for _, p := range result.Unreachable {
		if p.From == builder || p.To == builder {
			pairs++
		}
	}
	if pairs != 4 {
		t.Errorf("%d unreachable pairs involve builder-1, want 4 (to and from each of two nodes)", pairs)
	}
	if routes := result.Tables[builder].FIB(); len(routes) != 3 {
		t.Errorf("builder-1 keeps %d active routes, want its 3 connected ones", len(routes))
	}
}

// TestTwoLinksFailInOneEvaluation is P1-FAIL-028: two links fail in one batch.
// There is one evaluation and one recompute, each link gets its own decision, and
// the tables are the ones the engine makes for a graph with both links down.
func TestTwoLinksFailInOneEvaluation(t *testing.T) {
	net := threeSite(t)
	c := &counting{}
	net.Compute = c.compute
	start := healthy(t, net)
	c.calls = 0
	signals := join(probes(direct, false, 3, 35), probes(hubD, false, 1, 35), probes(hubB, true, 3, 35))
	result := evaluate(t, net, start.State, signals, 50)
	if c.calls != 1 {
		t.Errorf("the engine was called %d times, want once", c.calls)
	}
	var links []types.LinkID
	for _, d := range result.Decisions {
		if len(d.Activated)+len(d.Withdrawn) > 0 && !slices.Contains(links, d.Link) {
			links = append(links, d.Link)
		}
	}
	slices.Sort(links)
	if !slices.Equal(links, []types.LinkID{hubD, direct}) {
		t.Errorf("the decisions that moved routes are for %v, want the direct link and the dgx-spark hub link", links)
	}
	want, err := route.Compute(fixture.ThreeSite(true).Down(direct).Down(hubD), fixture.ThreeSiteInput())
	if err != nil {
		t.Fatal(err)
	}
	if diff := route.Diff(want.Tables, result.Tables); len(diff) != 0 {
		t.Errorf("the tables differ from the engine's for both links down: %+v", diff)
	}
	// The same signals fed in two batches, one link at a time, end in the same place.
	first := evaluate(t, net, start.State, join(probes(direct, false, 3, 35), probes(hubB, true, 3, 35)), 50)
	second := evaluate(t, net, first.State, probes(hubD, false, 1, 35), 50)
	if diff := route.Diff(second.Tables, result.Tables); len(diff) != 0 {
		t.Errorf("one batch and two batches give different tables: %+v", diff)
	}
}

// TestEvaluateDoesNotChangeItsInput checks the previous state is not touched
// by Evaluate: its JSON is the same before and after.
func TestEvaluateDoesNotChangeItsInput(t *testing.T) {
	net := threeSite(t)
	start := healthy(t, net)
	before, err := start.State.JSON()
	if err != nil {
		t.Fatal(err)
	}
	down(t, net, start.State)
	after, err := start.State.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("Evaluate changed the state it was given")
	}
}

// TestSkippedSignalsAreReported checks a signal Evaluate cannot use comes back in
// the result, and the rest of the batch is still used.
func TestSkippedSignalsAreReported(t *testing.T) {
	net := threeSite(t)
	bad := health.Signal{Link: "nowhere~else", From: builder, Kind: health.SignalProbe, OK: true, At: at(6)}
	result := evaluate(t, net, failover.State{}, join(probes(direct, true, 5, 5), []health.Signal{bad}), 30)
	if len(result.Skipped) != 1 || result.Skipped[0].Signal.Link != "nowhere~else" {
		t.Errorf("skipped %+v, want the signal for the link that is not there", result.Skipped)
	}
	if got := linkState(t, result.State, direct); got != health.Healthy {
		t.Errorf("the rest of the batch was not used: the direct link is %s", got)
	}
}

// TestEvaluateRefusesBadInput checks the errors Evaluate returns carry their codes.
func TestEvaluateRefusesBadInput(t *testing.T) {
	net := threeSite(t)
	if _, err := failover.Evaluate(failover.Network{}, failover.State{}, nil, at(0)); !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("no graph: %v, want INVALID_TOPOLOGY", err)
	}
	if _, err := failover.Evaluate(net, failover.State{Schema: "beaver.failover/v0"}, nil, at(0)); !types.HasCode(err, types.ErrUnsupportedSchema) {
		t.Errorf("old schema: %v, want UNSUPPORTED_SCHEMA", err)
	}
	net.Health = health.DefaultConfig()
	net.Health.FailThreshold = 0
	if _, err := failover.Evaluate(net, failover.State{}, nil, at(0)); !types.HasCode(err, types.ErrInvalidConfig) {
		t.Errorf("a zero threshold: %v, want INVALID_CONFIG", err)
	}
	net.Health = health.Config{}
	twice := failover.State{Links: []*health.Link{mustLink(t, direct), mustLink(t, direct)}}
	if _, err := failover.Evaluate(net, twice, nil, at(0)); !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("a link listed twice: %v, want INVALID_TOPOLOGY", err)
	}
}

// mustLink returns a new required link between builder-1 and dgx-spark.
func mustLink(t *testing.T, id types.LinkID) *health.Link {
	t.Helper()
	l, err := health.NewLink(id, builder, dgx, true)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestRemovedLinksAreDropped checks a link the graph no longer has is not carried
// in the state, and a link the state does not know is added.
func TestRemovedLinksAreDropped(t *testing.T) {
	net := threeSite(t)
	start := healthy(t, net)
	smaller := net
	smaller.Graph = fixture.ThreeSite(false)
	smaller.Flows = nil
	result := evaluate(t, smaller, start.State, nil, 31)
	if _, ok := result.State.Link(direct); ok || len(result.State.Links) != 2 {
		t.Errorf("state links after the direct link left the graph: %d, direct present %v", len(result.State.Links), ok)
	}
	back := evaluate(t, net, result.State, nil, 32)
	if got := linkState(t, back.State, direct); got != health.Unknown {
		t.Errorf("a link that returns starts from nothing: %s, want Unknown", got)
	}
}
