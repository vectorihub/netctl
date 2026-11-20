package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// TestSymmetricIsTheExactReverse checks symmetry: the same nodes and the same
// links in the opposite order. Matching nodes with different links is not
// symmetric, and neither is anything that did not get delivered.
func TestSymmetricIsTheExactReverse(t *testing.T) {
	forward := journey(sim.Delivered, "builder-1", "aws-hub", "dgx-spark")
	if !sim.Symmetric(forward, journey(sim.Delivered, "dgx-spark", "aws-hub", "builder-1")) {
		t.Error("the exact reverse is not symmetric")
	}
	if sim.Symmetric(forward, journey(sim.Delivered, "dgx-spark", "builder-1")) {
		t.Error("a different path is symmetric")
	}
	if sim.Symmetric(forward, forward) {
		t.Error("a path is symmetric with itself, not with its reverse")
	}
	// Same nodes, but the return crosses a different link between aws-hub and
	// builder-1.
	other := journey(sim.Delivered, "dgx-spark", "aws-hub", "builder-1")
	other.Hops[1].Route.Via = "aws-hub~builder-1~second"
	if sim.Symmetric(forward, other) {
		t.Error("the same nodes over a different link are symmetric")
	}
	if sim.Symmetric(forward, journey(sim.BlackHole, "dgx-spark", "aws-hub", "builder-1")) {
		t.Error("a return that failed is symmetric")
	}
	if sim.Symmetric(journey(sim.BlackHole, "builder-1"), journey(sim.Delivered, "dgx-spark")) {
		t.Error("a forward that failed is symmetric")
	}
	local := journey(sim.Delivered, "a")
	if !sim.Symmetric(local, local) {
		t.Error("two packets delivered where they started are symmetric")
	}
}

// TestJudgeTable checks Judge on every case, one row each, including the order of
// precedence: a loop beats a down link, a down link beats a black hole, and a
// partitioned pair beats them all.
func TestJudgeTable(t *testing.T) {
	okForward := journey(sim.Delivered, "a", "hub", "b")
	okReturn := journey(sim.Delivered, "b", "hub", "a")
	otherReturn := journey(sim.Delivered, "b", "a")
	none := sim.Trace{}
	cases := []struct {
		name         string
		forward, ret sim.Trace
		c            sim.Case
		want         sim.Verdict
	}{
		{"delivered both ways, symmetric", okForward, okReturn, sim.Case{}, sim.VerdictOK},
		{"asymmetric, not allowed", okForward, otherReturn, sim.Case{}, sim.VerdictAsymmetric},
		{"asymmetric, allowed", okForward, otherReturn, sim.Case{AllowAsymmetric: true}, sim.VerdictOKAsymmetricAllowed},
		{"allowing asymmetry changes nothing when symmetric", okForward, okReturn, sim.Case{AllowAsymmetric: true}, sim.VerdictOK},
		{"return dropped", okForward, journey(sim.BlackHole, "b", "hub"), sim.Case{}, sim.VerdictNoReturn},
		{"forward dropped", journey(sim.BlackHole, "a", "hub"), none, sim.Case{}, sim.VerdictBlackHole},
		{"forward loops", journey(sim.LoopDetected, "a", "hub", "a"), none, sim.Case{}, sim.VerdictLoop},
		{"return loops", okForward, journey(sim.LoopDetected, "b", "hub", "b"), sim.Case{}, sim.VerdictLoop},
		{"forward over a down link", journey(sim.LinkDown, "a"), none, sim.Case{}, sim.VerdictLinkDown},
		{"return over a down link", okForward, journey(sim.LinkDown, "b"), sim.Case{}, sim.VerdictLinkDown},
		{"forward to a non-neighbour", journey(sim.WrongNeighbour, "a"), none, sim.Case{}, sim.VerdictWrongNeighbour},
		{"return to a non-neighbour", okForward, journey(sim.WrongNeighbour, "b"), sim.Case{}, sim.VerdictWrongNeighbour},
		{"forward out of hops", journey(sim.TTLExceeded, "a", "hub"), none, sim.Case{}, sim.VerdictTTLExceeded},
		{"return out of hops", okForward, journey(sim.TTLExceeded, "b", "hub"), sim.Case{}, sim.VerdictTTLExceeded},
		{"negative, dropped", journey(sim.BlackHole, "a"), none, sim.Case{Negative: true}, sim.VerdictUnreachableExpected},
		{"negative, delivered", okForward, none, sim.Case{Negative: true}, sim.VerdictUnexpectedDelivery},
		{"negative, looping is a real fault", journey(sim.LoopDetected, "a", "hub", "a"), none, sim.Case{Negative: true}, sim.VerdictLoop},
		{"negative, down link is a real fault", journey(sim.LinkDown, "a"), none, sim.Case{Negative: true}, sim.VerdictLinkDown},
		{"partitioned, dropped", journey(sim.BlackHole, "a"), none, sim.Case{Partitioned: true}, sim.VerdictNoHealthyPath},
		{"partitioned, looping", journey(sim.LoopDetected, "a", "hub", "a"), none, sim.Case{Partitioned: true}, sim.VerdictNoHealthyPath},
		{"partitioned, return dropped", okForward, journey(sim.BlackHole, "b"), sim.Case{Partitioned: true}, sim.VerdictNoHealthyPath},
		{"partitioned but delivered both ways", okForward, okReturn, sim.Case{Partitioned: true}, sim.VerdictOK},
		{"loop beats a down link", journey(sim.LoopDetected, "a", "hub", "a"), journey(sim.LinkDown, "b"), sim.Case{}, sim.VerdictLoop},
		{"a down link beats a black hole", okForward, journey(sim.LinkDown, "b"), sim.Case{}, sim.VerdictLinkDown},
	}
	for _, c := range cases {
		if got := sim.Judge(c.forward, c.ret, c.c); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// TestProblemsOfNamesTheFault checks the problems listed for a flow: each failed
// trace gives one with its direction, the node to blame and a reason in words; a
// path that is not symmetric gives one; a flow with nothing wrong has none.
func TestProblemsOfNamesTheFault(t *testing.T) {
	forward := journey(sim.Delivered, "builder-1", "aws-hub", "dgx-spark")
	forward.Packet = sim.Packet{Src: b1Addr, Dst: dgxAddr}
	ret := journey(sim.BlackHole, "dgx-spark", "aws-hub")
	ret.Packet = sim.Packet{Src: dgxAddr, Dst: b1Addr}
	problems := sim.ProblemsOf(forward, &ret, sim.VerdictNoReturn)
	if len(problems) != 1 || problems[0].Kind != sim.ProblemBlackHole || problems[0].Direction != "return" ||
		problems[0].Node != "aws-hub" || problems[0].Detail != "aws-hub has no route for 10.42.240.2" {
		t.Errorf("a dropped reply: %+v", problems)
	}

	loop := journey(sim.LoopDetected, "builder-1", "aws-hub", "builder-1")
	loop.Packet = sim.Packet{Dst: dgxAddr}
	problems = sim.ProblemsOf(loop, nil, sim.VerdictLoop)
	if len(problems) != 1 || problems[0].Kind != sim.ProblemLoop || problems[0].Direction != "forward" ||
		!strings.Contains(problems[0].Detail, "builder-1 > aws-hub > builder-1") {
		t.Errorf("a loop: %+v", problems)
	}

	for final, kind := range map[sim.HopResult]sim.ProblemKind{
		sim.LinkDown: sim.ProblemLinkDown, sim.WrongNeighbour: sim.ProblemWrongNeighbour, sim.TTLExceeded: sim.ProblemTTLExceeded,
	} {
		problems = sim.ProblemsOf(journey(final, "builder-1"), nil, sim.VerdictLoop)
		if len(problems) != 1 || problems[0].Kind != kind || problems[0].Detail == "" {
			t.Errorf("%s: %+v", final, problems)
		}
	}

	back := journey(sim.Delivered, "dgx-spark", "builder-1")
	problems = sim.ProblemsOf(forward, &back, sim.VerdictAsymmetric)
	if len(problems) != 1 || problems[0].Kind != sim.ProblemAsymmetric ||
		problems[0].Detail != "forward builder-1 > aws-hub > dgx-spark, return dgx-spark > builder-1" {
		t.Errorf("asymmetry: %+v", problems)
	}
	if allowed := sim.ProblemsOf(forward, &back, sim.VerdictOKAsymmetricAllowed); len(allowed) != 1 {
		t.Errorf("an allowed asymmetry should still be listed as a warning: %+v", allowed)
	}
	for _, passing := range []sim.Verdict{sim.VerdictOK, sim.VerdictUnreachableExpected, sim.VerdictNoHealthyPath} {
		if got := sim.ProblemsOf(journey(sim.BlackHole, "a"), nil, passing); len(got) != 0 {
			t.Errorf("%s lists problems: %+v", passing, got)
		}
	}
	unexpected := sim.ProblemsOf(journey(sim.Delivered, "builder-1", "dgx-spark"), nil, sim.VerdictUnexpectedDelivery)
	if len(unexpected) != 1 || unexpected[0].Kind != sim.ProblemUnexpectedDelivery || unexpected[0].Node != "dgx-spark" {
		t.Errorf("an unexpected delivery: %+v", unexpected)
	}
}

// TestHealthyFlowIsOKBothWays checks the plain case: the overlay flow between
// the two spokes with the direct link up is OK, goes straight across both ways,
// is symmetric and has no problems. With the direct link down it goes through
// the hub both ways and is still OK.
func TestHealthyFlowIsOKBothWays(t *testing.T) {
	up := computePlan(t, fixture.ThreeSite(true))
	got := verdictOf(up.net(), "builder-1", "dgx-spark")
	if got.Verdict != sim.VerdictOK || !got.Symmetric || got.Return == nil || len(got.Problems) != 0 ||
		got.Forward.Crossings() != 1 || got.Return.Crossings() != 1 {
		t.Errorf("direct link up: %+v", got)
	}
	down := computePlan(t, fixture.ThreeSiteDirectDown())
	got = verdictOf(down.net(), "builder-1", "dgx-spark")
	if got.Verdict != sim.VerdictOK || !got.Symmetric || got.Forward.Crossings() != 2 || got.Return.Crossings() != 2 {
		t.Errorf("direct link down: %+v", got)
	}
	if want := []types.NodeID{"dgx-spark", "aws-hub", "builder-1"}; strings.Join(nodesText(*got.Return), ",") != "dgx-spark,aws-hub,builder-1" {
		t.Errorf("the reply went %v, want %v", nodesText(*got.Return), want)
	}
}

// P1-SIM-002
//
// TestPlantedBlackHoleOnTheHub checks that a missing route on the hub, for
// builder-1's addresses, makes builder-1 to dgx-spark a NO_RETURN: the packet gets
// there, the reply is dropped at the hub, and the return trace ends there with a
// black hole.
func TestPlantedBlackHoleOnTheHub(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(false))
	dropRoutes(p.tables, "aws-hub", "10.42.16.0/20", "10.42.240.2/32")
	got := verdictOf(p.net(), "builder-1", "dgx-spark")
	if got.Verdict != sim.VerdictNoReturn || !got.Forward.Delivered() {
		t.Fatalf("verdict %s, forward delivered %v", got.Verdict, got.Forward.Delivered())
	}
	if got.Return.Final != sim.BlackHole || got.Return.Hops[len(got.Return.Hops)-1].Node != "aws-hub" {
		t.Errorf("the reply ended %s at %v, want a black hole at aws-hub", got.Return.Final, nodesText(*got.Return))
	}
	if len(got.Problems) != 1 || got.Problems[0].Direction != "return" || got.Problems[0].Node != "aws-hub" {
		t.Errorf("problems = %+v", got.Problems)
	}
}

// P1-SIM-003
//
// TestPlantedMissingRouteOnTheHub checks that removing the hub's routes for
// dgx-spark makes the forward packet a BLACK_HOLE at aws-hub, and that the reply
// is not attempted.
func TestPlantedMissingRouteOnTheHub(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(false))
	dropRoutes(p.tables, "aws-hub", "10.42.32.0/20", "10.42.240.3/32")
	got := verdictOf(p.net(), "builder-1", "dgx-spark")
	if got.Verdict != sim.VerdictBlackHole || got.Forward.Hops[len(got.Forward.Hops)-1].Node != "aws-hub" || got.Return != nil {
		t.Errorf("verdict %s, forward %v, return %v", got.Verdict, nodesText(got.Forward), got.Return)
	}
}

// P1-SIM-004
//
// TestNegativeFlowIsUnreachable checks that a packet to 192.168.1.1, outside the
// supernet, is dropped and the flow is UNREACHABLE_EXPECTED, which passes the gate.
func TestNegativeFlowIsUnreachable(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	flow := sim.Flow{Label: "outside", Family: sim.FamilyNegative, Src: b1Addr, Dst: addrOf("192.168.1.1"), From: "builder-1"}
	got := p.net().Run(flow, false)
	if got.Verdict != sim.VerdictUnreachableExpected || got.Verdict.FailsGate() || got.Return != nil || len(got.Problems) != 0 {
		t.Errorf("verdict %s, return %v, problems %v", got.Verdict, got.Return, got.Problems)
	}
	addRoute(p.tables, "builder-1", "0.0.0.0/0", "aws-hub")
	addRoute(p.tables, "aws-hub", "192.168.0.0/16", "builder-1")
	if got := p.net().Run(flow, false); got.Verdict != sim.VerdictLoop {
		t.Errorf("a negative flow that loops got %s, want LOOP", got.Verdict)
	}
}

// TestNegativeFlowThatIsDelivered checks that an address outside the supernet
// that some node accepts as its own is UNEXPECTED_DELIVERY, a failure.
func TestNegativeFlowThatIsDelivered(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	p.owned["aws-hub"] = append(p.owned["aws-hub"], prefixOf("192.168.1.0/24"))
	addRoute(p.tables, "builder-1", "192.168.1.0/24", "aws-hub")
	flow := sim.Flow{Label: "outside", Family: sim.FamilyNegative, Src: b1Addr, Dst: addrOf("192.168.1.1"), From: "builder-1"}
	got := p.net().Run(flow, false)
	if got.Verdict != sim.VerdictUnexpectedDelivery || !got.Verdict.FailsGate() || len(got.Problems) != 1 {
		t.Errorf("verdict %s problems %+v, want UNEXPECTED_DELIVERY with one problem", got.Verdict, got.Problems)
	}
}

// P1-SIM-020
//
// TestOneSidedDirectLink checks that builder-1 sending to dgx-spark's site over the
// direct link while dgx-spark has no route back, neither over the direct link nor
// through the hub, is NO_RETURN.
func TestOneSidedDirectLink(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	dropRoutes(p.tables, "dgx-spark", "10.42.16.0/20", "10.42.240.2/32")
	got := verdictOf(p.net(), "builder-1", "dgx-spark")
	if got.Verdict != sim.VerdictNoReturn || got.Forward.Crossings() != 1 || got.Return.Final != sim.BlackHole {
		t.Errorf("verdict %s, forward crossings %d, return %s", got.Verdict, got.Forward.Crossings(), got.Return.Final)
	}
	if got.Return.Hops[0].Node != "dgx-spark" || len(got.Return.Hops) != 1 {
		t.Errorf("the reply should be dropped at dgx-spark itself: %v", nodesText(*got.Return))
	}
}

// P1-SIM-022, P1-SIM-023
//
// TestAsymmetricPathAndWhenItIsAllowed checks a return path over a different
// route. builder-1 is made to reach dgx-spark through the hub while dgx-spark's
// reply still takes the direct link. Delivered both ways but along different
// paths is ASYMMETRIC and fails the gate; the same pair marked allow_asymmetric
// is OK_ASYMMETRIC_ALLOWED, a warning, and the report passes with it.
func TestAsymmetricPathAndWhenItIsAllowed(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	for i := range p.tables["builder-1"].Routes {
		r := &p.tables["builder-1"].Routes[i]
		if r.Prefix == prefixOf("10.42.240.3/32") {
			r.Active = r.Source == route.SourceBackup
		}
	}
	flow := overlayFlow("builder-1", "dgx-spark")
	got := p.net().Run(flow, false)
	if got.Verdict != sim.VerdictAsymmetric || got.Symmetric || !got.Verdict.FailsGate() {
		t.Fatalf("verdict %s symmetric %v, want ASYMMETRIC", got.Verdict, got.Symmetric)
	}
	if got.Forward.Crossings() != 2 || got.Return.Crossings() != 1 {
		t.Errorf("forward %d crossings, return %d; want 2 and 1", got.Forward.Crossings(), got.Return.Crossings())
	}
	if len(got.Problems) != 1 || got.Problems[0].Kind != sim.ProblemAsymmetric {
		t.Errorf("problems = %+v", got.Problems)
	}
	if report := sim.NewReport([]sim.FlowResult{got}); report.Passed {
		t.Error("an asymmetric flow passed the gate")
	}

	flow.AllowAsymmetric = true
	allowed := p.net().Run(flow, false)
	if allowed.Verdict != sim.VerdictOKAsymmetricAllowed || allowed.Verdict.FailsGate() {
		t.Fatalf("verdict %s, want OK_ASYMMETRIC_ALLOWED", allowed.Verdict)
	}
	report := sim.NewReport([]sim.FlowResult{allowed})
	if !report.Passed || report.Summary.Warnings != 1 {
		t.Errorf("passed %v with %d warnings, want a pass with 1 warning", report.Passed, report.Summary.Warnings)
	}
}

// TestJudgementOfDetectedFaults checks the verdicts end to end for the faults the
// plan lists, through Run: a route to a node that is not a neighbour, and an
// active route over a link that is down.
func TestJudgementOfDetectedFaults(t *testing.T) {
	wrong := computePlan(t, fixture.ThreeSite(false))
	retarget(wrong.tables, "builder-1", "10.42.240.3/32", "dgx-spark", "aws-hub~builder-1")
	if got := verdictOf(wrong.net(), "builder-1", "dgx-spark"); got.Verdict != sim.VerdictWrongNeighbour ||
		len(got.Problems) != 1 || got.Problems[0].Kind != sim.ProblemWrongNeighbour {
		t.Errorf("wrong neighbour: %s %+v", got.Verdict, got.Problems)
	}
	healthy := computePlan(t, fixture.ThreeSite(true))
	got := verdictOf(healthy.netOver(fixture.ThreeSiteDirectDown()), "builder-1", "dgx-spark")
	if got.Verdict != sim.VerdictLinkDown || len(got.Problems) != 1 || got.Problems[0].Kind != sim.ProblemLinkDown {
		t.Errorf("link down: %s %+v", got.Verdict, got.Problems)
	}
}

// TestPartitionedFlow checks that a flow the engine reported unreachable is
// judged NO_HEALTHY_PATH, a result that passes the gate: nothing is wrong with
// the plan, the network is cut.
func TestPartitionedFlow(t *testing.T) {
	g := fixture.ThreeSite(true)
	g.Down(types.LinkIDOf("aws-hub", "dgx-spark")).Down(types.LinkIDOf("builder-1", "dgx-spark"))
	p := computePlan(t, g)
	got := p.net().Run(overlayFlow("builder-1", "dgx-spark"), true)
	if got.Verdict != sim.VerdictNoHealthyPath || got.Verdict.FailsGate() {
		t.Errorf("verdict %s, want NO_HEALTHY_PATH, passing", got.Verdict)
	}
	if without := p.net().Run(overlayFlow("builder-1", "dgx-spark"), false); without.Verdict != sim.VerdictBlackHole {
		t.Errorf("the same flow not marked partitioned got %s, want BLACK_HOLE", without.Verdict)
	}
}
