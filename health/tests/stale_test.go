package tests

import (
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// P1-FAIL-005
//
// TestSilenceTakesAHealthyLinkDown checks a Healthy link with no signals for 16
// seconds is Down, and that the decision says it went stale, at the moment it
// went quiet plus 15 seconds, not at the moment somebody looked.
func TestSilenceTakesAHealthyLinkDown(t *testing.T) {
	e := baseline(t)
	decisions := apply(t, e, nil, sec(41))
	want(t, e, health.Down)
	if len(decisions) != 1 || !decisions[0].At.Equal(at(sec(40))) || !strings.Contains(decisions[0].Reason, "stale") ||
		decisions[0].Before != health.Healthy || decisions[0].After != health.Down {
		t.Errorf("decisions = %+v, want one: Healthy to Down at 40 s, stale", decisions)
	}
}

// TestStaleBoundary checks silence of exactly 15 seconds is not yet stale, and a
// nanosecond more is.
func TestStaleBoundary(t *testing.T) {
	e := baseline(t)
	apply(t, e, nil, sec(40))
	want(t, e, health.Healthy)
	_, _ = e.Apply(nil, at(sec(40)).Add(1))
	want(t, e, health.Down)
}

// TestStaleHoldDownRunsFromTheMomentItWentStale checks the hold-down of a stale
// link ends 60 seconds after the silence began to count, not 60 seconds after
// the check: the link went stale at 40 s, so a success at 99 s is inside the
// hold-down and one at 100 s is not, whenever the check happened.
func TestStaleHoldDownRunsFromTheMomentItWentStale(t *testing.T) {
	for _, checked := range []int{41, 95} {
		e := baseline(t)
		apply(t, e, nil, sec(checked))
		if got := e.Links[direct].Dirs[b1].HoldDownUntil; !got.Equal(at(sec(100))) {
			t.Errorf("checked at %d s: hold-down until %v, want 100 s", checked, got)
		}
	}
	e := baseline(t)
	apply(t, e, slices.Concat(probes(true, 1, sec(99)), probes(true, 1, sec(100))), sec(101))
	if got := e.Links[direct].Dirs[b1]; got.State != health.Recovering || got.OKRun != 1 {
		t.Errorf("after successes at 99 s and 100 s: %s with %d good, want recovering with 1: the first was ignored", got.State, got.OKRun)
	}
}

// TestSilenceInTheMiddleOfABatch checks a gap between two signals of one batch
// counts: good probes at 5-25 s, then nothing until 60 s, then a good probe,
// handed in together, leave the link Down, not Healthy.
func TestSilenceInTheMiddleOfABatch(t *testing.T) {
	e := newDirect(t)
	signals := slices.Concat(probes(true, 5, sec(5)), probes(true, 1, sec(60)))
	decisions := apply(t, e, signals, sec(61))
	want(t, e, health.Down)
	if len(decisions) != 2 || decisions[1].After != health.Down || !decisions[1].At.Equal(at(sec(40))) {
		t.Errorf("decisions = %+v, want Healthy at 25 s then Down at 40 s", decisions)
	}
	if got := e.Links[direct].Dirs[b1]; got.State != health.Down || got.OKRun != 0 {
		t.Errorf("the late probe at 60 s: %s with %d good; want down, its success ignored in the hold-down", got.State, got.OKRun)
	}
}

// TestOneSilentEndTakesTheLinkDown checks that when only one end goes quiet, the
// link goes Down for both, at the time that end went stale, even though the other
// end keeps reporting good news.
func TestOneSilentEndTakesTheLinkDown(t *testing.T) {
	e := baseline(t)
	apply(t, e, probes(true, 10, sec(30), b1), sec(75))
	want(t, e, health.Down)
	l := e.Links[direct]
	if l.Dirs[b1].State != health.Healthy || l.Dirs[dgx].State != health.Down {
		t.Errorf("builder-1 %s, dgx-spark %s; want healthy and down", l.Dirs[b1].State, l.Dirs[dgx].State)
	}
}

// TestUnknownIsNeverStale checks a link that never had evidence stays Unknown
// however long it is left, and a direction that is already Down is not made to go
// stale again, which would push its hold-down out.
func TestUnknownIsNeverStale(t *testing.T) {
	e := newDirect(t)
	if decisions := apply(t, e, nil, sec(3600)); len(decisions) != 0 {
		t.Errorf("an unknown link made decisions: %+v", decisions)
	}
	want(t, e, health.Unknown)
	down := baseline(t)
	apply(t, down, nil, sec(41))
	before := down.Links[direct].Dirs[b1].HoldDownUntil
	if decisions := apply(t, down, nil, sec(600)); len(decisions) != 0 || !down.Links[direct].Dirs[b1].HoldDownUntil.Equal(before) {
		t.Errorf("a down link went stale again: %+v, hold-down %v then %v", decisions, before, down.Links[direct].Dirs[b1].HoldDownUntil)
	}
}

// TestRecoveringGoesStale checks a Recovering link that falls silent is Down
// again, with a new hold-down from when it went quiet.
func TestRecoveringGoesStale(t *testing.T) {
	e := baseline(t)
	apply(t, e, probes(false, 3, sec(35)), sec(50))
	apply(t, e, probes(true, 2, sec(110)), sec(116))
	want(t, e, health.Recovering)
	apply(t, e, nil, sec(140))
	want(t, e, health.Down)
	if got := e.Links[direct].Dirs[b1].HoldDownUntil; !got.Equal(at(sec(130 + 60))) {
		t.Errorf("hold-down until %v, want 190 s: 60 s from 130 s, when it went quiet (last probe 115 s, plus 15 s)", got)
	}
}

// P1-FAIL-006, P1-FAIL-007
//
// TestRecoveryTimeline replays the plan's recovery on the whole link: Down at 45 s
// with the hold-down to 105 s, five good probes at 75-95 s ignored, four good
// probes from 110 s leave it Recovering, the fifth makes it Healthy and the link
// usable.
func TestRecoveryTimeline(t *testing.T) {
	e := baseline(t)
	apply(t, e, probes(false, 3, sec(35)), sec(50))
	want(t, e, health.Down)
	if got := e.Links[direct].Dirs[b1].HoldDownUntil; !got.Equal(at(sec(105))) {
		t.Fatalf("hold-down until %v, want 105 s", got)
	}
	apply(t, e, probes(true, 5, sec(75)), sec(100))
	want(t, e, health.Down)
	apply(t, e, probes(true, 4, sec(110)), sec(126))
	want(t, e, health.Recovering)
	if e.Links[direct].Usable() {
		t.Error("a Recovering link must not be used yet")
	}
	apply(t, e, probes(true, 1, sec(130)), sec(131))
	want(t, e, health.Healthy)
	if !e.Links[direct].Usable() {
		t.Error("a Healthy link must be usable")
	}
}

// TestSingleLostProbeOnlyDegrades replays the plan's single flap: one lost probe
// at 35 s then good ones leave the link usable throughout, and Healthy again.
func TestSingleLostProbeOnlyDegrades(t *testing.T) {
	e := baseline(t)
	apply(t, e, probes(false, 1, sec(35)), sec(36))
	want(t, e, health.Degraded)
	if !e.Links[direct].Usable() {
		t.Error("a Degraded link must stay usable: routes do not change on one failure")
	}
	apply(t, e, probes(true, 3, sec(40)), sec(55))
	want(t, e, health.Healthy)
}

// TestOneDirectionRecoveryKeepsTheLinkDown replays the plan's one-way recovery:
// after the hold-down builder-1 hears dgx-spark again but dgx-spark still hears
// nothing, and the link stays Down.
func TestOneDirectionRecoveryKeepsTheLinkDown(t *testing.T) {
	e := baseline(t)
	apply(t, e, probes(false, 3, sec(35)), sec(50))
	signals := slices.Concat(probes(true, 5, sec(110), b1), probes(false, 5, sec(110), dgx))
	apply(t, e, signals, sec(135))
	want(t, e, health.Down)
	if e.Links[direct].Dirs[b1].State != health.Healthy {
		t.Errorf("builder-1's direction is %s, want healthy: it did recover", e.Links[direct].Dirs[b1].State)
	}
}

// TestEvaluationNeverReadsTheClock applies the same signals with two different
// values of now that are both inside the stale window, and checks the result is
// the same: only the times in the signals and the time given count.
func TestEvaluationNeverReadsTheClock(t *testing.T) {
	run := func(now int) *health.Evaluator {
		e := baseline(t)
		apply(t, e, probes(true, 3, sec(30)), sec(now))
		return e
	}
	a, b := run(46), run(50)
	if a.Links[direct].State != health.Healthy || b.Links[direct].State != health.Healthy {
		t.Fatalf("both should be Healthy: %s, %s", a.Links[direct].State, b.Links[direct].State)
	}
	if !a.Links[direct].Dirs[b1].LastEvidence.Equal(b.Links[direct].Dirs[b1].LastEvidence) {
		t.Error("the last evidence depends on now")
	}
}

// TestStaleDecisionOrderIsDeterministic checks the decisions for several links
// going stale at once come out in the same order every time, sorted by link, even
// though the links live in a map.
func TestStaleDecisionOrderIsDeterministic(t *testing.T) {
	build := func() *health.Evaluator {
		var links []*health.Link
		var signals []health.Signal
		for _, ends := range [][2]types.NodeID{{"a", "b"}, {"c", "d"}, {"e", "f"}, {"g", "h"}} {
			id := types.LinkIDOf(ends[0], ends[1])
			l, _ := health.NewLink(id, ends[0], ends[1], true)
			links = append(links, l)
			for _, end := range ends {
				for i := 0; i < 5; i++ {
					signals = append(signals, health.Signal{Link: id, From: end, Kind: health.SignalProbe, OK: true, At: at(sec(5 + 5*i))})
				}
			}
		}
		e, err := health.NewEvaluator(cfg, links...)
		if err != nil {
			t.Fatal(err)
		}
		e.Apply(signals, at(sec(30)))
		return e
	}
	var first []types.LinkID
	for run := 0; run < 20; run++ {
		decisions, _ := build().Apply(nil, at(sec(60)))
		var got []types.LinkID
		for _, d := range decisions {
			got = append(got, d.Link)
		}
		if run == 0 {
			first = got
			if len(got) != 4 || !slices.IsSorted(got) {
				t.Fatalf("decisions for %v, want four links in sorted order", got)
			}
		} else if !slices.Equal(got, first) {
			t.Fatalf("run %d gave %v, run 0 gave %v", run, got, first)
		}
	}
}
