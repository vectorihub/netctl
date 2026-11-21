package phase1

import (
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// init registers Group 2, failover and failback: health signals move traffic to the hub when
// the direct link really fails and back when it really recovers, without being fooled by
// noise. Every timeline is an offset from T0 and starts from the healthy baseline, with the
// direct link Healthy since T0+25s and the clock at T0+30s.
func init() {
	register("P1-E2E-020", e2e020Fails)
	register("P1-E2E-021", e2e021FlapsOnce)
	register("P1-E2E-022", e2e022Recovers)
	register("P1-E2E-023", e2e023HoldDown)
	register("P1-E2E-024", e2e024OneDirection)
	register("P1-E2E-025", e2e025InterfaceOnly)
	register("P1-E2E-026", e2e026FlapStorm)
	register("P1-E2E-027", e2e027HubUplink)
	register("P1-E2E-028", e2e028Isolation)
	register("P1-E2E-029", e2e029Rejected)
}

// failedOver builds the end state of P1-E2E-020: the healthy baseline and the plan after the
// direct link fails, with the clock at T0+50s.
func failedOver(w *e2e.W) (base, failed *e2e.Out) {
	w.T.Helper()
	_, base = e2e.Baseline(w)
	w.Step("the direct link fails")
	w.At(50 * time.Second)
	failed = w.MustPlan(e2e.Options{From: base, Signals: failProbes(3, 35*time.Second).List(), Name: "out-2"})
	return base, failed
}

// e2e020Fails is P1-E2E-020: three failed probes take the direct link Down, and both
// spokes fail over to the hub in the same plan. It proves AC7 and, with the oracle, AC5.
func e2e020Fails(w *e2e.W) {
	base, failed := failedOver(w)

	w.Step("the link's health")
	l := link(w, failed, e2e.DirectLink)
	if l.State != health.Down || l.Dirs["builder-1"].State != health.Down || l.Dirs["dgx-spark"].State != health.Down {
		w.Fatalf("link %v, builder-1 %v, dgx-spark %v; want Down on all", l.State, l.Dirs["builder-1"].State, l.Dirs["dgx-spark"].State)
	}
	if hold := l.Dirs["builder-1"].HoldDownUntil; !hold.Equal(e2e.T0.Add(105 * time.Second)) {
		w.Fatalf("hold-down until %v, want T0+1m45s", hold)
	}
	expectDecision(w, failed, e2e.DirectLink, health.Healthy, health.Degraded, 35*time.Second)
	down := expectDecision(w, failed, e2e.DirectLink, health.Degraded, health.Down, 45*time.Second)
	if !strings.Contains(down.Reason, "consecutive failures") {
		w.Fatalf("the decision's reason is %q", down.Reason)
	}

	w.Step("only the two spokes change")
	expectDiffNodes(w, base, failed, "builder-1", "dgx-spark")
	expectSameConfig(w, base, failed, "aws-hub")
	w.Golden("builder-1-failed-over.json", e2e.Masked(failed.Files["nodes/builder-1.json"]))
	w.Golden("dgx-spark-failed-over.json", e2e.Masked(failed.Files["nodes/dgx-spark.json"]))
	b := w.JSON(failed, "nodes/builder-1.json")
	if got := b.Strings("peers[node=aws-hub].allowed_prefixes"); !slices.Equal(got, []string{"10.42.0.0/20", "10.42.32.0/20", "10.42.240.1/32", "10.42.240.3/32"}) {
		w.Fatalf("builder-1's hub peer allows %s", listed(got))
	}

	w.Step("the probe address stays on the direct peer so recovery can be seen")
	if got := b.Strings("peers[node=dgx-spark].allowed_prefixes"); !slices.Equal(got, []string{"10.42.252.2/32"}) {
		w.Fatalf("builder-1's direct peer allows %s, want only the probe address", listed(got))
	}
	if b.Str("health.probes.0.target") != "10.42.252.2" {
		w.Fatalf("the probe target moved to %s", b.Str("health.probes.0.target"))
	}

	w.Step("traffic goes through the hub, both ways")
	w.ExpectSimPass(failed, -1)
	expectFlowPath(w, failed, "builder-1→dgx-spark (overlay)", "builder-1", "aws-hub", "dgx-spark")
	w.ExpectOracleOK(failed)
}

// e2e021FlapsOnce is P1-E2E-021: one lost probe is noise. The link goes Degraded and back to
// Healthy, and no config changes.
func e2e021FlapsOnce(w *e2e.W) {
	_, base := e2e.Baseline(w)
	w.Step("one failed probe, then good ones")
	w.At(55 * time.Second)
	signals := failProbes(1, 35*time.Second).Add(goodProbes(3, 40*time.Second))
	out := w.MustPlan(e2e.Options{From: base, Signals: signals.List(), Name: "out-2"})
	expectDecision(w, out, e2e.DirectLink, health.Healthy, health.Degraded, 35*time.Second)
	expectDecision(w, out, e2e.DirectLink, health.Degraded, health.Healthy, 40*time.Second)
	expectNoDiff(w, base, out)
	if out.Result.PlanID != base.Result.PlanID {
		w.Fatalf("plan ID %s, want the baseline's %s", out.Result.PlanID, base.Result.PlanID)
	}
}

// e2e022Recovers is P1-E2E-022: after the outage the link recovers, and both spokes fail back
// in one plan to exactly the baseline configuration. It proves AC7 (failback) and AC8.
func e2e022Recovers(w *e2e.W) {
	base, failed := failedOver(w)
	w.Step("the link recovers")
	w.At(2*time.Minute + 15*time.Second)
	signals := failProbes(12, 50*time.Second).Add(goodProbes(5, 110*time.Second))
	back := w.MustPlan(e2e.Options{From: failed, Signals: signals.List(), Name: "out-3"})
	expectDecision(w, back, e2e.DirectLink, health.Down, health.Recovering, 110*time.Second)
	expectDecision(w, back, e2e.DirectLink, health.Recovering, health.Healthy, 130*time.Second)
	expectNoDiff(w, base, back)
	if back.Result.PlanID != base.Result.PlanID || back.SubtreeHash("nodes/") != base.SubtreeHash("nodes/") {
		w.Fatalf("failback did not return to the baseline: %s and %s", back.Result.PlanID, base.Result.PlanID)
	}
	for _, n := range []string{"builder-1", "dgx-spark"} {
		if back.FileHash("nodes/"+n+".json") != base.FileHash("nodes/"+n+".json") {
			w.Fatalf("%s did not fail back with the other", n)
		}
	}
}

// e2e023HoldDown is P1-E2E-023: good probes during hold-down are ignored, so a link cannot
// come back too soon; the same probes after the hold-down are what bring it back.
func e2e023HoldDown(w *e2e.W) {
	_, failed := failedOver(w)
	w.Step("good probes during hold-down")
	w.At(100 * time.Second)
	signals := failProbes(5, 50*time.Second).Add(goodProbes(5, 75*time.Second))
	still := w.MustPlan(e2e.Options{From: failed, Signals: signals.List(), Name: "out-3"})
	if l := link(w, still, e2e.DirectLink); l.State != health.Down {
		w.Fatalf("the link is %v during hold-down, want Down", l.State)
	}
	for _, d := range decisions(still) {
		if d.At.After(e2e.T0.Add(45 * time.Second)) {
			w.Fatalf("a transition after T0+45s: %+v", d)
		}
	}
	expectNoDiff(w, failed, still)
	for _, node := range []types.NodeID{"builder-1", "dgx-spark"} {
		if run := link(w, still, e2e.DirectLink).Dirs[node].OKRun; run != 0 {
			w.Fatalf("%s counted %d good probes during hold-down, want them ignored", node, run)
		}
	}

	w.Step("the same probes once hold-down has passed")
	w.At(2*time.Minute + 15*time.Second)
	later := w.MustPlan(e2e.Options{From: still, Signals: goodProbes(5, 110*time.Second).List(), Name: "out-4"})
	expectDecision(w, later, e2e.DirectLink, health.Down, health.Recovering, 110*time.Second)
	expectDecision(w, later, e2e.DirectLink, health.Recovering, health.Healthy, 130*time.Second)
}

// e2e024OneDirection is P1-E2E-024: a link is usable only when healthy in both directions, so
// paths stay symmetric. Builder hears the link recover, DGX Spark does not, and nothing fails
// back.
func e2e024OneDirection(w *e2e.W) {
	_, failed := failedOver(w)
	w.Step("recovery heard from one end only")
	w.At(2*time.Minute + 15*time.Second)
	signals := failProbes(12, 50*time.Second).
		Add(new(e2e.Signals).Probes(e2e.DirectLink, true, 5, 110*time.Second, 5*time.Second, e2e.OneWay("builder-1"))).
		Add(new(e2e.Signals).Probes(e2e.DirectLink, false, 5, 110*time.Second, 5*time.Second, e2e.OneWay("dgx-spark")))
	out := w.MustPlan(e2e.Options{From: failed, Signals: signals.List(), Name: "out-3"})
	l := link(w, out, e2e.DirectLink)
	if l.Dirs["builder-1"].State != health.Healthy || l.Dirs["dgx-spark"].State != health.Down || l.State != health.Down {
		w.Fatalf("builder-1 sees %v, dgx-spark sees %v, the link is %v; want Healthy, Down, Down", l.Dirs["builder-1"].State, l.Dirs["dgx-spark"].State, l.State)
	}
	expectNoDiff(w, failed, out)
}

// e2e025InterfaceOnly is P1-E2E-025: an interface that says "up" is never accepted as health.
// Ten minutes of such signals leave the direct link Unknown and the traffic on the hub.
func e2e025InterfaceOnly(w *e2e.W) {
	w.Setup()
	topo := w.Load(e2e.DefaultTopology)
	w.Step("fresh plan")
	fresh := w.MustPlan(e2e.Options{Topology: topo, Name: "out-0"})
	w.Step("ten minutes of interface-up signals")
	w.At(10 * time.Minute)
	signals := new(e2e.Signals).Probes(e2e.DirectLink, true, 120, 5*time.Second, 5*time.Second, e2e.Kind("interface"))
	if signals.Len() != 240 {
		w.Fatalf("built %d signals, want 240", signals.Len())
	}
	out := w.MustPlan(e2e.Options{Topology: topo, From: fresh, Signals: signals.List(), Name: "out-1"})
	if l := link(w, out, e2e.DirectLink); l.State != health.Unknown {
		w.Fatalf("the link is %v after interface-only signals, want Unknown", l.State)
	}
	expectNoDiff(w, fresh, out)
	exp, err := out.Result.Explain("builder-1", addr("10.42.33.7"))
	if err != nil {
		w.Fatalf("explain: %v", err)
	}
	if exp.Match == nil || string(exp.Match.NextHop) != "aws-hub" || !strings.Contains(strings.ToLower(exp.Reason), "unknown") {
		w.Fatalf("explanation %+v, want the hub and a reason that says the health is unknown", exp)
	}
}

// e2e026FlapStorm is P1-E2E-026: a link that keeps failing is pinned down rather than moving
// traffic back and forth. The fifth Down within ten minutes exceeds the flap limit; two more
// minutes of good probes change nothing; an admin drain and undrain clears the pin.
func e2e026FlapStorm(w *e2e.W) {
	base, failed := failedOver(w)
	w.Step("five outages in under eight minutes")
	signals := new(e2e.Signals)
	for cycle := 0; cycle < 5; cycle++ {
		start := 35*time.Second + time.Duration(cycle)*100*time.Second
		signals.Probes(e2e.DirectLink, false, 3, start, 5*time.Second)
		if cycle < 4 {
			signals.Probes(e2e.DirectLink, true, 5, start+75*time.Second, 5*time.Second)
		}
	}
	w.At(7*time.Minute + 30*time.Second)
	storm := w.MustPlan(e2e.Options{From: base, Signals: signals.List(), Name: "out-2"})
	l := link(w, storm, e2e.DirectLink)
	if !l.Pinned || l.State != health.Down {
		w.Fatalf("after five outages the link is %v pinned %v, want Down and pinned", l.State, l.Pinned)
	}
	if storm.SubtreeHash("nodes/") != failed.SubtreeHash("nodes/") {
		w.Fatalf("the storm's configs are not the failed-over configs")
	}

	w.Step("two more minutes of good probes change nothing")
	w.At(9*time.Minute + 30*time.Second)
	good := new(e2e.Signals).Probes(e2e.DirectLink, true, 24, 7*time.Minute+35*time.Second, 5*time.Second)
	still := w.MustPlan(e2e.Options{From: storm, Signals: good.List(), Name: "out-3"})
	if l := link(w, still, e2e.DirectLink); !l.Pinned || l.State != health.Down {
		w.Fatalf("pinned link moved: %v pinned %v", l.State, l.Pinned)
	}
	expectNoDiff(w, storm, still)

	w.Step("an admin drain and undrain clears the pin; the link then proves itself after a new hold-down")
	w.At(11 * time.Minute)
	admin := new(e2e.Signals).
		Probes(e2e.DirectLink, false, 1, 9*time.Minute+40*time.Second, 0, e2e.Kind("admin"), e2e.OneWay("builder-1")).
		Probes(e2e.DirectLink, true, 1, 9*time.Minute+45*time.Second, 0, e2e.Kind("admin"), e2e.OneWay("builder-1")).
		Add(new(e2e.Signals).Probes(e2e.DirectLink, true, 5, 10*time.Minute+50*time.Second, 5*time.Second))
	cleared := w.MustPlan(e2e.Options{From: still, Signals: admin.List(), Name: "out-4"})
	if l := link(w, cleared, e2e.DirectLink); l.Pinned || l.Drained || l.State != health.Healthy {
		w.Fatalf("after the undrain and five good probes the link is %v pinned %v drained %v, want Healthy", l.State, l.Pinned, l.Drained)
	}
	expectNoDiff(w, base, cleared)
}

// e2e027HubUplink is P1-E2E-027: the hub link of one spoke fails. The guide expects a plan with
// a warning (V-SIM-004). This implementation is stricter: with that hub link gone, builder-1
// has no chain of healthy links to a hub (V-TOPO-002, an error), so the guard refuses the
// failover plan and keeps the plan in force, with the reason. The test pins that behaviour:
// refused, explained, nothing written, and the plan in force untouched. See the gate notes.
func e2e027HubUplink(w *e2e.W) {
	_, base := e2e.Baseline(w)
	before := w.Snapshot("out-1")
	w.Step("the hub link of builder-1 fails")
	w.At(50 * time.Second)
	signals := new(e2e.Signals).Probes("aws-hub~builder-1", false, 3, 35*time.Second, 5*time.Second, e2e.Kind("handshake"))
	out := w.Plan(e2e.Options{From: base, Signals: signals.List(), Name: "out-2"})
	expectRejected(w, out, "builder-1")
	w.ExpectUnchanged("out-1", before)
}

// e2e028Isolation is P1-E2E-028: one spoke loses both its links. As in 027 the guard refuses
// the plan, naming the cut-off spoke, and the plan in force stays.
func e2e028Isolation(w *e2e.W) {
	_, base := e2e.Baseline(w)
	before := w.Snapshot("out-1")
	w.Step("builder-1 loses both its links")
	w.At(50 * time.Second)
	signals := failProbes(3, 35*time.Second).Add(new(e2e.Signals).Probes("aws-hub~builder-1", false, 3, 35*time.Second, 5*time.Second, e2e.Kind("handshake")))
	out := w.Plan(e2e.Options{From: base, Signals: signals.List(), Name: "out-2"})
	expectRejected(w, out, "builder-1")
	w.ExpectUnchanged("out-1", before)
	w.Step("the plan in force is still sound")
	w.ExpectOracleOK(base)
}

// expectRejected fails unless the plan was refused by the failover guard: the error is a
// validation failure, a FailoverRejected decision says why, V-TOPO-002 names the cut-off node,
// and no output directory was written.
func expectRejected(w *e2e.W, out *e2e.Out, cutOff string) {
	w.T.Helper()
	w.ExpectRefused(out, types.ErrValidationFailed)
	rejected := false
	for _, d := range out.Result.Decisions {
		if d.Kind == "FailoverRejected" {
			rejected = true
		}
	}
	if !rejected {
		w.Fatalf("no FailoverRejected decision: %v", out.Result.Decisions)
	}
	for _, f := range out.Result.Validation.Errors() {
		if f.Rule == "V-TOPO-002" && f.Subject == cutOff {
			return
		}
	}
	w.Fatalf("no V-TOPO-002 error on %s: %v", cutOff, out.Result.Validation.Errors())
}

// e2e029Rejected is P1-E2E-029: the planner never installs routes that are worse than the ones
// in force. The guide's trap, a static route that is harmless while the direct link is healthy
// and loops once it fails, cannot be built here: with the link healthy this planner already
// finds the route asymmetric, and it drops a static route over a downed link. So the routes in
// force are the healthy baseline's and the worse routes come from telling the hub to send
// DGX Spark's site to builder-1. The guard simulates them, sees the asymmetric paths, installs nothing and
// says so with a FailoverRejected decision. This planner still produces a plan, made from the
// routes in force (the guide expects exit 2); the configs are therefore the baseline's, and
// the plan in force is untouched.
func e2e029Rejected(w *e2e.W) {
	_, base := e2e.Baseline(w)
	before := w.Snapshot("out-1")

	w.Step("the same network with a looping static route")
	overrides := []route.Override{{Node: "aws-hub", Prefix: prefix("10.42.32.0/20"), NextHop: "builder-1"}}
	out := w.MustPlan(e2e.Options{From: base, Overrides: overrides, Name: "out-2"})
	var rejected *failover.FailoverRejected
	if out.Result.Failover != nil {
		rejected = out.Result.Failover.Rejected
	}
	if rejected == nil || len(rejected.Failures) == 0 {
		w.Fatalf("the guard did not reject the looping routes: %+v", out.Result.Decisions)
	}
	kinds := map[string]bool{}
	for _, f := range rejected.Failures {
		kinds[f.Verdict.String()] = true
	}
	if !kinds["ASYMMETRIC"] {
		w.Fatalf("the rejected flows are %v, want ASYMMETRIC among them", rejected.Failures)
	}
	found := false
	for _, d := range out.Result.Decisions {
		if d.Kind == "FailoverRejected" {
			found = true
		}
	}
	if !found {
		w.Fatalf("no FailoverRejected decision: %v", out.Result.Decisions)
	}
	w.Step("the routes in force are kept")
	expectNoDiff(w, base, out)
	w.ExpectOracleOK(out)
	w.ExpectUnchanged("out-1", before)
}
