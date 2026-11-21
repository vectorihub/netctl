package tests

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// flapRound drives the Healthy direct link through one outage that starts at
// start seconds: three failed probes from each end take it Down at start+10 s
// with a hold-down to start+70 s. If recover is true, five good probes from
// each end from start+75 s then bring it Recovering and Healthy by start+95 s.
// It returns every decision the round made.
func flapRound(t *testing.T, e *health.Evaluator, start int, recoverAfter bool) []health.Decision {
	t.Helper()
	decisions := apply(t, e, probes(false, 3, sec(start)), sec(start+15))
	if recoverAfter {
		decisions = append(decisions, apply(t, e, probes(true, 5, sec(start+75)), sec(start+96))...)
	}
	return decisions
}

// TestFlapLimitPinsTheLink is P1-FAIL-009: the link goes Down five times in under
// ten minutes, which is more than the limit of four, so the fifth time pins it.
// The decision says "flap limit", and good evidence cannot lift the pin.
func TestFlapLimitPinsTheLink(t *testing.T) {
	e := baseline(t)
	l := e.Links[direct]
	for i, start := range []int{35, 135, 235, 335} {
		flapRound(t, e, start, true)
		if l.Pinned || len(l.Flaps) != i+1 {
			t.Fatalf("after %d outages: pinned %v with %d flaps, want not pinned and %d", i+1, l.Pinned, len(l.Flaps), i+1)
		}
		want(t, e, health.Healthy)
	}
	decisions := flapRound(t, e, 435, false)
	if !l.Pinned || l.State != health.Down || len(l.Flaps) != 5 {
		t.Fatalf("after the fifth outage: pinned %v, state %s, %d flaps", l.Pinned, l.State, len(l.Flaps))
	}
	last := decisions[len(decisions)-1]
	if last.After != health.Down || !strings.Contains(last.Reason, "flap limit") {
		t.Errorf("the pinning decision is %+v, want Down with a reason that says flap limit", last)
	}
	if want := at(sec(445)).Add(cfg.PinFor); !l.PinnedUntil.Equal(want) {
		t.Errorf("pinned until %v, want %v: 30 minutes from the fifth Down", l.PinnedUntil, want)
	}
	// Ten minutes of perfect probes, long past the hold-down, change nothing.
	apply(t, e, probes(true, 120, sec(520)), sec(1121))
	want(t, e, health.Down)
	if got := e.UnusableLinks(); len(got) != 1 {
		t.Errorf("unusable links %v, want the pinned link", got)
	}
}

// TestPinEndsByItselfAndThenTheLinkMustProveItself checks that a pin runs out
// after pin_for, at the moment it was due and not when someone looked, that the
// flap count starts again, and that the link then goes through Recovering to
// Healthy like any link that was Down.
func TestPinEndsByItselfAndThenTheLinkMustProveItself(t *testing.T) {
	e := baseline(t)
	for _, start := range []int{35, 135, 235, 335} {
		flapRound(t, e, start, true)
	}
	flapRound(t, e, 435, false)
	l := e.Links[direct]
	until := l.PinnedUntil
	apply(t, e, nil, sec(2244))
	if !l.Pinned {
		t.Fatal("the pin ended a second early")
	}
	apply(t, e, nil, sec(2290))
	if l.Pinned || len(l.Flaps) != 0 || l.State != health.Down || !l.PinnedUntil.IsZero() {
		t.Fatalf("after the pin: pinned %v until %v, %d flaps, state %s; want released, zero, none, Down", l.Pinned, l.PinnedUntil, len(l.Flaps), l.State)
	}
	for node, d := range l.Dirs {
		if want := until.Add(cfg.HoldDown); !d.HoldDownUntil.Equal(want) {
			t.Errorf("%s hold-down until %v, want %v: from the moment the pin ended, not from when it was noticed", node, d.HoldDownUntil, want)
		}
	}
	apply(t, e, probes(true, 1, sec(2295)), sec(2296))
	want(t, e, health.Down)
	apply(t, e, probes(true, 1, sec(2310)), sec(2311))
	want(t, e, health.Recovering)
	apply(t, e, probes(true, 4, sec(2315)), sec(2336))
	want(t, e, health.Healthy)
}

// TestOperatorClearsAPin checks an OK admin signal lifts a flap pin at once, and
// that the link is Down, with a new hold-down, until it proves itself.
func TestOperatorClearsAPin(t *testing.T) {
	e := baseline(t)
	for _, start := range []int{35, 135, 235, 335} {
		flapRound(t, e, start, true)
	}
	flapRound(t, e, 435, false)
	clear := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: true, At: at(sec(500))}
	decisions, skipped := e.Apply([]health.Signal{clear}, at(sec(501)))
	if len(skipped) != 0 {
		t.Fatalf("skipped %+v", skipped)
	}
	l := e.Links[direct]
	if l.Pinned || len(l.Flaps) != 0 || l.State != health.Down || len(decisions) != 0 {
		t.Fatalf("after the clear: pinned %v, %d flaps, state %s, decisions %+v", l.Pinned, len(l.Flaps), l.State, decisions)
	}
	apply(t, e, probes(true, 1, sec(530)), sec(531))
	want(t, e, health.Down)
	apply(t, e, probes(true, 5, sec(570)), sec(591))
	want(t, e, health.Healthy)
}

// TestFlapsOutsideTheWindowDoNotCount checks outages more than ten minutes apart
// never add up to a pin: seven outages, 11 minutes apart, leave one flap on the
// record each time.
func TestFlapsOutsideTheWindowDoNotCount(t *testing.T) {
	e := baseline(t)
	l := e.Links[direct]
	start := 35
	for i := 0; i < 7; i++ {
		flapRound(t, e, start, true)
		if l.Pinned || len(l.Flaps) != 1 {
			t.Fatalf("outage %d: pinned %v with %d flaps, want one flap and no pin", i+1, l.Pinned, len(l.Flaps))
		}
		// Keep the link talking until the next outage: silence would itself
		// be a flap, the link going stale.
		next := start + 660
		apply(t, e, probes(true, (next-start-100)/5-1, sec(start+100)), sec(next-5))
		start = next
	}
}

// TestFlapAtTheEdgeOfTheWindow checks a flap exactly one window old still
// counts and one a nanosecond older does not.
func TestFlapAtTheEdgeOfTheWindow(t *testing.T) {
	for _, tc := range []struct {
		name string
		gap  time.Duration
		want int
	}{{"exactly the window", cfg.FlapWindow, 2}, {"a nanosecond more", cfg.FlapWindow + 1, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			e := baseline(t)
			l := e.Links[direct]
			l.Flaps = append(l.Flaps, at(sec(45)).Add(-tc.gap))
			flapRound(t, e, 35, false)
			if len(l.Flaps) != tc.want {
				t.Errorf("%d flaps on the record, want %d", len(l.Flaps), tc.want)
			}
		})
	}
}

// TestAdminDrain is P1-FAIL-011: draining a Healthy link makes it Down at once,
// good evidence cannot bring it back while it is drained, and undraining it
// allows Recovering, not Healthy: it must show good signals after a new
// hold-down, however well it behaved while drained.
func TestAdminDrain(t *testing.T) {
	e := baseline(t)
	drain := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: false, At: at(sec(40))}
	decisions, skipped := e.Apply([]health.Signal{drain}, at(sec(40)))
	if len(skipped) != 0 || len(decisions) != 1 || decisions[0].Before != health.Healthy || decisions[0].After != health.Down ||
		!strings.Contains(decisions[0].Reason, "drain") || !decisions[0].At.Equal(at(sec(40))) {
		t.Fatalf("drain: decisions %+v, skipped %+v", decisions, skipped)
	}
	l := e.Links[direct]
	if !l.Drained || l.Usable() || len(l.Flaps) != 0 {
		t.Fatalf("drained %v, usable %v, %d flaps: a drain is not a flap and the link is out of service", l.Drained, l.Usable(), len(l.Flaps))
	}
	apply(t, e, probes(true, 20, sec(45)), sec(141))
	want(t, e, health.Down)

	undrain := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: true, At: at(sec(150))}
	apply(t, e, []health.Signal{undrain}, sec(150))
	want(t, e, health.Down)
	apply(t, e, probes(true, 5, sec(155)), sec(181))
	want(t, e, health.Down) // inside the hold-down that began at the undrain: until 210 s
	apply(t, e, probes(true, 1, sec(215)), sec(216))
	want(t, e, health.Recovering)
	apply(t, e, probes(true, 4, sec(220)), sec(236))
	want(t, e, health.Healthy)
}

// TestAdminSignalsThatDoNothingAreSkipped checks draining a drained link, and
// releasing a link that is neither drained nor pinned, are returned as skipped
// with a reason, and change nothing.
func TestAdminSignalsThatDoNothingAreSkipped(t *testing.T) {
	e := baseline(t)
	release := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: true, At: at(sec(40))}
	if _, skipped := e.Apply([]health.Signal{release}, at(sec(40))); len(skipped) != 1 || !strings.Contains(skipped[0].Why, "nothing to release") {
		t.Errorf("releasing a healthy link: skipped %+v", skipped)
	}
	drain := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: false, At: at(sec(41))}
	again := drain
	again.At = at(sec(42))
	_, skipped := e.Apply([]health.Signal{drain, again}, at(sec(43)))
	if len(skipped) != 1 || !strings.Contains(skipped[0].Why, "already drained") {
		t.Errorf("draining twice: skipped %+v", skipped)
	}
	want(t, e, health.Down)
}

// TestDrainOnTheTimeline checks an admin signal in the middle of a batch lands in
// time order with the probes around it: a probe after the drain cannot undo it.
func TestDrainOnTheTimeline(t *testing.T) {
	e := baseline(t)
	signals := append(probes(true, 2, sec(35)), health.Signal{Link: direct, Kind: health.SignalAdmin, OK: false, At: at(sec(37))})
	signals = append(signals, probes(true, 3, sec(40))...)
	apply(t, e, signals, sec(50))
	want(t, e, health.Down)
}

// TestDrainAndPinCanBothHold checks a link that is both drained and pinned stays
// Down until both are lifted, and that one OK admin signal lifts both.
func TestDrainAndPinCanBothHold(t *testing.T) {
	e := baseline(t)
	l := e.Links[direct]
	l.Drained, l.Pinned, l.PinnedUntil = true, true, at(sec(5000))
	apply(t, e, probes(true, 5, sec(35)), sec(60))
	want(t, e, health.Down)
	release := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: true, At: at(sec(70))}
	apply(t, e, []health.Signal{release}, sec(70))
	if l.Drained || l.Pinned {
		t.Errorf("after the release: drained %v, pinned %v", l.Drained, l.Pinned)
	}
}

// TestHeldLinksAreAlwaysDown is a property: whatever evidence and admin signals
// arrive, a link that is drained or pinned is Down and not usable, and a link
// whose state is Healthy is neither.
func TestHeldLinksAreAlwaysDown(t *testing.T) {
	rng := rand.New(rand.NewPCG(51, 1))
	for run := 0; run < 500; run++ {
		e := newDirect(t)
		l := e.Links[direct]
		now := time.Duration(0)
		for step := 0; step < 80; step++ {
			now += time.Duration(1+rng.IntN(40)) * time.Second
			s := health.Signal{Link: direct, From: []types.NodeID{b1, dgx}[rng.IntN(2)], At: at(now), OK: rng.IntN(3) > 0}
			switch rng.IntN(10) {
			case 0:
				s.Kind = health.SignalAdmin
			case 1:
				s.Kind = health.SignalInterface
			default:
				s.Kind = health.SignalProbe
			}
			e.Apply([]health.Signal{s}, at(now))
			if (l.Drained || l.Pinned) && (l.State != health.Down || l.Usable()) {
				t.Fatalf("run %d step %d: drained %v pinned %v but state %s usable %v", run, step, l.Drained, l.Pinned, l.State, l.Usable())
			}
			if l.State == health.Healthy && (l.Drained || l.Pinned) {
				t.Fatalf("run %d step %d: Healthy while held", run, step)
			}
		}
	}
}

// TestLinkCloneSharesNothing checks a clone can be changed without changing the
// original.
func TestLinkCloneSharesNothing(t *testing.T) {
	e := baseline(t)
	l := e.Links[direct]
	l.Flaps = append(l.Flaps, at(sec(1)))
	c := l.Clone()
	c.Dirs[b1].State = health.Down
	c.Flaps[0] = at(sec(2))
	c.Drained = true
	if l.Dirs[b1].State != health.Healthy || !l.Flaps[0].Equal(at(sec(1))) || l.Drained {
		t.Errorf("changing the clone changed the original: %+v", l)
	}
}

// TestLateAdminSignalsAreSkipped checks an admin order older than the newest one
// already applied is skipped, across separate calls: a drain at 100 s cannot be
// undone by a release stamped 50 s that arrives late, and the hold-down is not
// restarted from the older time.
func TestLateAdminSignalsAreSkipped(t *testing.T) {
	e := baseline(t)
	drain := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: false, At: at(sec(100))}
	apply(t, e, probes(true, 1, sec(95)), sec(96))
	apply(t, e, []health.Signal{drain}, sec(100))
	late := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: true, At: at(sec(50))}
	_, skipped := e.Apply([]health.Signal{late}, at(sec(101)))
	if len(skipped) != 1 || !strings.Contains(skipped[0].Why, "outdated") {
		t.Fatalf("skipped %+v, want the late release skipped as outdated", skipped)
	}
	l := e.Links[direct]
	if !l.Drained || l.State != health.Down || !l.LastAdmin.Equal(at(sec(100))) {
		t.Errorf("drained %v, state %s, last admin %v: the late release must change nothing", l.Drained, l.State, l.LastAdmin)
	}
	same := health.Signal{Link: direct, Kind: health.SignalAdmin, OK: true, At: at(sec(100))}
	if _, skipped := e.Apply([]health.Signal{same}, at(sec(102))); len(skipped) != 0 {
		t.Errorf("a release at the same instant as the newest order was skipped: %+v", skipped)
	}
}
