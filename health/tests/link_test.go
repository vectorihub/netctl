package tests

import (
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// TestNewLinkStartsUnknownAndRefusesBadEnds checks a new link is Unknown in both
// directions, and that a link from a node to itself or with an end missing is
// refused with INVALID_TOPOLOGY.
func TestNewLinkStartsUnknownAndRefusesBadEnds(t *testing.T) {
	l, err := health.NewLink(direct, b1, dgx, true)
	if err != nil || l.State != health.Unknown || len(l.Dirs) != 2 || l.Dirs[b1].State != health.Unknown || l.Dirs[dgx].State != health.Unknown {
		t.Fatalf("NewLink = %+v, %v", l, err)
	}
	for name, ends := range map[string][2]types.NodeID{"same node": {b1, b1}, "no first end": {"", dgx}, "no second end": {b1, ""}} {
		if l, err := health.NewLink("x", ends[0], ends[1], true); !types.HasCode(err, types.ErrInvalidTopology) || l != nil {
			t.Errorf("%s: %v, %v; want INVALID_TOPOLOGY and no link", name, l, err)
		}
	}
}

// TestUsable checks which states let routing use a link: Healthy and Degraded
// always, Down and Recovering never, and Unknown only for a link that is assumed
// rather than required.
func TestUsable(t *testing.T) {
	cases := []struct {
		state    health.LinkHealth
		required bool
		usable   bool
	}{
		{health.Healthy, true, true}, {health.Healthy, false, true},
		{health.Degraded, true, true}, {health.Degraded, false, true},
		{health.Down, true, false}, {health.Down, false, false},
		{health.Recovering, true, false}, {health.Recovering, false, false},
		{health.Unknown, true, false}, {health.Unknown, false, true},
	}
	for _, c := range cases {
		l := &health.Link{State: c.state, Required: c.required}
		if got := l.Usable(); got != c.usable {
			t.Errorf("%s, required %v: usable %v, want %v", c.state, c.required, got, c.usable)
		}
	}
}

// P1-FAIL-003
//
// TestInterfaceUpAloneNeverMakesALinkHealthy gives a new direct link ten minutes of
// interface-up signals from both ends, and checks it is still Unknown, still not
// usable, and no decision was made.
func TestInterfaceUpAloneNeverMakesALinkHealthy(t *testing.T) {
	e := newDirect(t)
	var signals []health.Signal
	for i := 0; i < 120; i++ {
		for _, end := range []types.NodeID{b1, dgx} {
			signals = append(signals, health.Signal{Link: direct, From: end, Kind: health.SignalInterface, OK: true, At: at(sec(5 * i))})
		}
	}
	decisions := apply(t, e, signals, sec(600))
	want(t, e, health.Unknown)
	if e.Links[direct].Usable() || len(decisions) != 0 {
		t.Errorf("usable %v, decisions %+v; want an unusable link and none", e.Links[direct].Usable(), decisions)
	}
}

// TestInterfaceUpDoesNotRefreshAHealthyLink checks interface-up signals are not
// evidence: a Healthy link kept company only by interface-up signals still goes
// stale.
func TestInterfaceUpDoesNotRefreshAHealthyLink(t *testing.T) {
	e := baseline(t)
	var signals []health.Signal
	for i := 0; i < 10; i++ {
		for _, end := range []types.NodeID{b1, dgx} {
			signals = append(signals, health.Signal{Link: direct, From: end, Kind: health.SignalInterface, OK: true, At: at(sec(30 + 5*i))})
		}
	}
	apply(t, e, signals, sec(80))
	want(t, e, health.Down)
}

// TestInterfaceDownLowersAtOnce checks an interface-down signal from one end takes
// a Healthy link Down immediately, without waiting for three failures, with a
// reason that says so.
func TestInterfaceDownLowersAtOnce(t *testing.T) {
	e := baseline(t)
	signal := health.Signal{Link: direct, From: dgx, Kind: health.SignalInterface, OK: false, At: at(sec(32))}
	decisions := apply(t, e, []health.Signal{signal}, sec(33))
	want(t, e, health.Down)
	if len(decisions) != 1 || decisions[0].Before != health.Healthy || decisions[0].After != health.Down ||
		!decisions[0].At.Equal(at(sec(32))) || !strings.Contains(decisions[0].Reason, "interface is down") {
		t.Errorf("decisions = %+v", decisions)
	}
	if got := e.Links[direct].Dirs[dgx].HoldDownUntil; !got.Equal(at(sec(92))) {
		t.Errorf("hold-down until %v, want 92 s", got)
	}
}

// TestInterfaceUpCannotEndAHoldDown checks that interface-up signals after the
// hold-down do not start a recovery: only probes and handshakes do.
func TestInterfaceUpCannotEndAHoldDown(t *testing.T) {
	e := baseline(t)
	apply(t, e, probes(false, 3, sec(35)), sec(50))
	want(t, e, health.Down)
	var signals []health.Signal
	for i := 0; i < 20; i++ {
		for _, end := range []types.NodeID{b1, dgx} {
			signals = append(signals, health.Signal{Link: direct, From: end, Kind: health.SignalInterface, OK: true, At: at(sec(110 + 5*i))})
		}
	}
	apply(t, e, signals, sec(210))
	want(t, e, health.Down)
}

// P1-FAIL-010
//
// TestOneDirectionFailingMakesTheLinkDownBothWays checks that when builder-1's
// probes succeed and dgx-spark's fail, the link is Down for routing, and that
// there is one verdict for the link, not one per end: both ends fail over
// together.
func TestOneDirectionFailingMakesTheLinkDownBothWays(t *testing.T) {
	e := baseline(t)
	signals := append(probes(true, 6, sec(35), b1), probes(false, 6, sec(35), dgx)...)
	apply(t, e, signals, sec(65))
	want(t, e, health.Down)
	l := e.Links[direct]
	if l.Usable() || l.Dirs[b1].State == health.Down {
		t.Errorf("usable %v, builder-1's direction %s; want an unusable link whose healthy end is still healthy", l.Usable(), l.Dirs[b1].State)
	}
	if got := e.UnusableLinks(); !slices.Equal(got, []types.LinkID{direct}) {
		t.Errorf("UnusableLinks = %v, want the one link", got)
	}
}

// TestCombinationOfTwoDirections drives the two directions into each pair of
// states with real signals and checks the link takes the worse: Down over
// Recovering over Unknown over Degraded over Healthy.
func TestCombinationOfTwoDirections(t *testing.T) {
	good := func(end types.NodeID, n int, start int) []health.Signal { return probes(true, n, sec(start), end) }
	bad := func(end types.NodeID, n int, start int) []health.Signal { return probes(false, n, sec(start), end) }
	cat := func(parts ...[]health.Signal) []health.Signal { return slices.Concat(parts...) }
	cases := []struct {
		name    string
		signals []health.Signal
		now     int
		link    health.LinkHealth
		builder health.LinkHealth
		dgx     health.LinkHealth
	}{
		{"healthy and healthy", cat(good(b1, 5, 5), good(dgx, 5, 5)), 30, health.Healthy, health.Healthy, health.Healthy},
		{"healthy and unknown", cat(good(b1, 5, 5), good(dgx, 2, 5)), 30, health.Unknown, health.Healthy, health.Unknown},
		{"healthy and degraded", cat(good(b1, 5, 5), good(dgx, 5, 5), bad(dgx, 1, 30)), 31, health.Degraded, health.Healthy, health.Degraded},
		{"degraded and unknown", cat(good(b1, 5, 5), bad(b1, 1, 30)), 31, health.Unknown, health.Degraded, health.Unknown},
		{"down and healthy", cat(good(b1, 8, 5), good(dgx, 5, 5), bad(dgx, 3, 30)), 41, health.Down, health.Healthy, health.Down},
		{"down and unknown", cat(good(b1, 2, 5), bad(dgx, 1, 5)), 15, health.Down, health.Unknown, health.Down},
		{"recovering and healthy", cat(good(b1, 21, 5), good(dgx, 5, 5), bad(dgx, 3, 30), good(dgx, 2, 100)), 108, health.Recovering, health.Healthy, health.Recovering},
		{"recovering and down", cat(good(b1, 5, 5), good(dgx, 5, 5), bad(dgx, 3, 30), bad(b1, 3, 30), good(dgx, 1, 100)), 101, health.Down, health.Down, health.Recovering},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newDirect(t)
			e.Apply(c.signals, at(sec(c.now)))
			l := e.Links[direct]
			if l.State != c.link || l.Dirs[b1].State != c.builder || l.Dirs[dgx].State != c.dgx {
				t.Errorf("link %s, builder-1 %s, dgx-spark %s; want %s, %s, %s", l.State, l.Dirs[b1].State, l.Dirs[dgx].State, c.link, c.builder, c.dgx)
			}
		})
	}
}

// TestAssumedLinkIsUsedUntilItFails checks a hub-spoke link, which is not
// required, is usable before any evidence and stops being usable on the first
// failure.
func TestAssumedLinkIsUsedUntilItFails(t *testing.T) {
	const spoke = types.LinkID("aws-hub~builder-1")
	l, err := health.NewLink(spoke, "aws-hub", b1, false)
	if err != nil {
		t.Fatal(err)
	}
	e, err := health.NewEvaluator(cfg, l)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Usable() || len(e.UnusableLinks()) != 0 {
		t.Fatal("a new hub-spoke link should be assumed usable")
	}
	apply(t, e, []health.Signal{{Link: spoke, From: b1, Kind: health.SignalProbe, OK: false, At: at(sec(5))}}, sec(6))
	if l.Usable() || l.State != health.Down {
		t.Errorf("after a failure: %s, usable %v; want down and unusable", l.State, l.Usable())
	}
}

// TestDecisionsDescribeTheChange checks the decision for a link becoming Healthy
// names the link, both states, the time of the signal that caused it, and says
// why.
func TestDecisionsDescribeTheChange(t *testing.T) {
	e := newDirect(t)
	decisions := apply(t, e, probes(true, 5, sec(5)), sec(30))
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want exactly one: the link went Unknown to Healthy once", decisions)
	}
	d := decisions[0]
	if d.Link != direct || d.Before != health.Unknown || d.After != health.Healthy || !d.At.Equal(at(sec(25))) ||
		!strings.Contains(d.Reason, "probe from dgx-spark") {
		t.Errorf("decision = %+v", d)
	}
}

// TestOnlyChangesAreDecisions checks a signal that does not change the link as a
// whole makes no decision: a good probe on a Healthy link, and a first failure
// moving one end to Degraded when it moves the link too is one decision, not two.
func TestOnlyChangesAreDecisions(t *testing.T) {
	e := baseline(t)
	if decisions := apply(t, e, probes(true, 1, sec(30)), sec(31)); len(decisions) != 0 {
		t.Errorf("a good probe on a Healthy link made decisions: %+v", decisions)
	}
	decisions := apply(t, e, probes(false, 1, sec(35)), sec(36))
	if len(decisions) != 1 || decisions[0].After != health.Degraded {
		t.Errorf("two first failures made %+v, want one decision to Degraded", decisions)
	}
}

// TestSkippedSignals checks every kind of signal that cannot be applied is
// returned with a reason, in order, and the good signals around it are still
// applied.
func TestSkippedSignals(t *testing.T) {
	e := newDirect(t)
	signals := slices.Concat(probes(true, 5, sec(5)), []health.Signal{
		{Link: "nowhere~else", From: b1, Kind: health.SignalProbe, OK: true, At: at(sec(6))},
		{Link: direct, From: "aws-hub", Kind: health.SignalProbe, OK: true, At: at(sec(7))},
		{Link: direct, From: b1, OK: true, At: at(sec(8))},
		{Link: direct, From: b1, Kind: health.SignalAdmin, OK: false, At: at(sec(9))},
	})
	decisions, skipped := e.Apply(signals, at(sec(30)))
	if len(decisions) != 1 || e.Links[direct].State != health.Healthy {
		t.Errorf("the good probes were not applied: %+v, %s", decisions, e.Links[direct].State)
	}
	wantWhy := []string{"is not tracked", "not an end of the link", "no known kind", "admin signals are not supported"}
	if len(skipped) != len(wantWhy) {
		t.Fatalf("skipped %+v, want %d", skipped, len(wantWhy))
	}
	for i, why := range wantWhy {
		if !strings.Contains(skipped[i].Why, why) {
			t.Errorf("skipped[%d] = %q, want it to say %q", i, skipped[i].Why, why)
		}
	}
	if skipped[1].Signal.From != "aws-hub" {
		t.Errorf("the skipped signal is not returned as given: %+v", skipped[1].Signal)
	}
}

// TestApplyDoesNotReorderTheCallersSlice checks signals handed in out of order are
// applied in time order, and the caller's slice is left as it was.
func TestApplyDoesNotReorderTheCallersSlice(t *testing.T) {
	signals := probes(true, 5, sec(5))
	slices.Reverse(signals)
	first := slices.Clone(signals)
	e := newDirect(t)
	apply(t, e, signals, sec(30))
	want(t, e, health.Healthy)
	if !slices.Equal(signals, first) {
		t.Error("Apply reordered the caller's slice")
	}
}

// TestSignalsAtTheSameTimeKeepTheirOrder checks that two signals with one time are
// applied in the order given: a failure then a success is not a success then a
// failure.
func TestSignalsAtTheSameTimeKeepTheirOrder(t *testing.T) {
	failThenOK := func(order ...bool) health.LinkHealth {
		e := baseline(t)
		var signals []health.Signal
		for _, ok := range order {
			signals = append(signals, health.Signal{Link: direct, From: b1, Kind: health.SignalProbe, OK: ok, At: at(sec(30))})
			signals = append(signals, health.Signal{Link: direct, From: dgx, Kind: health.SignalProbe, OK: true, At: at(sec(30))})
		}
		apply(t, e, signals, sec(31))
		return e.Links[direct].Dirs[b1].State
	}
	if got := failThenOK(false, true); got != health.Healthy {
		t.Errorf("failure then success: %s, want healthy", got)
	}
	if got := failThenOK(true, false); got != health.Degraded {
		t.Errorf("success then failure: %s, want degraded", got)
	}
}

// TestNewEvaluatorRefusals checks a bad Config is refused with INVALID_CONFIG and
// a link given twice with INVALID_TOPOLOGY.
func TestNewEvaluatorRefusals(t *testing.T) {
	l, _ := health.NewLink(direct, b1, dgx, true)
	again, _ := health.NewLink(direct, b1, dgx, false)
	if _, err := health.NewEvaluator(health.Config{}, l); !types.HasCode(err, types.ErrInvalidConfig) {
		t.Errorf("a zero Config: %v, want INVALID_CONFIG", err)
	}
	if _, err := health.NewEvaluator(cfg, l, again); !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("a link given twice: %v, want INVALID_TOPOLOGY", err)
	}
	if e, err := health.NewEvaluator(cfg); err != nil || len(e.Links) != 0 {
		t.Errorf("an evaluator of no links: %v, %v; want one with none", e, err)
	}
}

// TestUnusableLinksIsSorted checks the list of links routing must avoid is sorted
// by ID, and holds only the links that are not usable.
func TestUnusableLinksIsSorted(t *testing.T) {
	var links []*health.Link
	for _, id := range []types.LinkID{"c~d", "a~b", "e~f"} {
		ends := strings.Split(string(id), "~")
		l, err := health.NewLink(id, types.NodeID(ends[0]), types.NodeID(ends[1]), true)
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, l)
	}
	e, err := health.NewEvaluator(cfg, links...)
	if err != nil {
		t.Fatal(err)
	}
	var signals []health.Signal
	for _, end := range []types.NodeID{"e", "f"} {
		for i := 0; i < 5; i++ {
			signals = append(signals, health.Signal{Link: "e~f", From: end, Kind: health.SignalProbe, OK: true, At: at(sec(5 + 5*i))})
		}
	}
	apply(t, e, signals, sec(30))
	if got := e.UnusableLinks(); !slices.Equal(got, []types.LinkID{"a~b", "c~d"}) {
		t.Errorf("UnusableLinks = %v, want [a~b c~d]", got)
	}
}
