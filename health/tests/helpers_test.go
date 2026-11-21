package tests

import (
	"testing"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// t0 is the instant every timeline starts from. It is fixed, so a test replays
// the same seconds on every run.
var t0 = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// at returns the instant d after t0.
func at(d time.Duration) time.Time { return t0.Add(d) }

// sec is a duration of n seconds, to keep the timelines short to read.
func sec(n int) time.Duration { return time.Duration(n) * time.Second }

// The direct link of the three-site network and its two ends.
const (
	direct = types.LinkID("builder-1~dgx-spark")
	b1     = types.NodeID("builder-1")
	dgx    = types.NodeID("dgx-spark")
)

// cfg is the default configuration, which the plan's timelines assume.
var cfg = health.DefaultConfig()

// feed gives a direction n signals, ok or not, every 5 seconds from start, and
// returns the time of the last. It drives a single Direction through the same
// timeline a probe every 5 seconds would produce.
func feed(d *health.Direction, ok bool, n int, start time.Duration) time.Duration {
	last := start
	for i := 0; i < n; i++ {
		last = start + sec(5*i)
		if ok {
			d.Succeed(at(last), cfg)
		} else {
			d.Fail(at(last), cfg)
		}
	}
	return last
}

// probes returns n probe signals on the direct link from both ends, or only from
// the ends in only, one every 5 seconds from start.
func probes(ok bool, n int, start time.Duration, only ...types.NodeID) []health.Signal {
	ends := []types.NodeID{b1, dgx}
	if len(only) > 0 {
		ends = only
	}
	var out []health.Signal
	for i := 0; i < n; i++ {
		for _, end := range ends {
			out = append(out, health.Signal{Link: direct, From: end, Kind: health.SignalProbe, OK: ok, At: at(start + sec(5*i))})
		}
	}
	return out
}

// newDirect returns an evaluator tracking one required direct link, Unknown.
func newDirect(t *testing.T) *health.Evaluator {
	t.Helper()
	link, err := health.NewLink(direct, b1, dgx, true)
	if err != nil {
		t.Fatal(err)
	}
	e, err := health.NewEvaluator(cfg, link)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// apply applies the signals at now and fails the test if any was skipped.
func apply(t *testing.T, e *health.Evaluator, signals []health.Signal, now time.Duration) []health.Decision {
	t.Helper()
	decisions, skipped := e.Apply(signals, at(now))
	if len(skipped) != 0 {
		t.Fatalf("signals were skipped: %+v", skipped)
	}
	return decisions
}

// baseline returns an evaluator whose direct link is Healthy: five good probes
// from each end at 5, 10, 15, 20 and 25 seconds, checked at 30.
func baseline(t *testing.T) *health.Evaluator {
	t.Helper()
	e := newDirect(t)
	apply(t, e, probes(true, 5, sec(5)), sec(30))
	want(t, e, health.Healthy)
	return e
}

// want checks the state of the direct link.
func want(t *testing.T, e *health.Evaluator, state health.LinkHealth) {
	t.Helper()
	if got := e.Links[direct].State; got != state {
		t.Fatalf("the link is %s, want %s", got, state)
	}
}
