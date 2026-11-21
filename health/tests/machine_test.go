package tests

import (
	"encoding/json"
	"testing"

	"github.com/vectorihub/netctl/health"
)

// healthy returns a direction that has been proven: five good signals, ending at
// 25 seconds.
func healthy(t *testing.T) *health.Direction {
	t.Helper()
	d := &health.Direction{}
	feed(d, true, 5, sec(5))
	if d.State != health.Healthy {
		t.Fatalf("the direction is %s after five good signals, want healthy", d.State)
	}
	return d
}

// wasDown returns a direction that went Down at 45 seconds, after three failures
// at 35, 40 and 45, so its hold-down ends at 105 seconds.
func wasDown(t *testing.T) *health.Direction {
	t.Helper()
	d := healthy(t)
	feed(d, false, 3, sec(35))
	if d.State != health.Down || !d.HoldDownUntil.Equal(at(sec(105))) {
		t.Fatalf("the direction is %s with hold-down until %v, want down until 105 s", d.State, d.HoldDownUntil)
	}
	return d
}

// P1-FAIL-001
//
// TestUnknownBecomesHealthyOnFifthGoodSignal checks four good signals leave the
// direction Unknown and the fifth makes it Healthy.
func TestUnknownBecomesHealthyOnFifthGoodSignal(t *testing.T) {
	d := &health.Direction{}
	feed(d, true, 4, sec(5))
	if d.State != health.Unknown {
		t.Fatalf("after four good signals: %s, want unknown", d.State)
	}
	state, reason := d.Succeed(at(sec(25)), cfg)
	if state != health.Healthy || d.State != health.Healthy || reason == "" {
		t.Errorf("after five: %s (%q), want healthy with a reason", d.State, reason)
	}
}

// TestUnknownFailureIsDown checks any failure from Unknown is Down at once, with
// the hold-down started from that moment.
func TestUnknownFailureIsDown(t *testing.T) {
	d := &health.Direction{}
	feed(d, true, 3, sec(5))
	d.Fail(at(sec(20)), cfg)
	if d.State != health.Down || !d.HoldDownUntil.Equal(at(sec(80))) {
		t.Errorf("%s with hold-down until %v, want down until 80 s", d.State, d.HoldDownUntil)
	}
	if d.OKRun != 0 {
		t.Errorf("the run of good signals is %d after a failure, want 0", d.OKRun)
	}
}

// P1-FAIL-002
//
// TestHealthyOneFailureIsDegraded checks a single failure makes a Healthy
// direction Degraded and no more, and that a success then makes it Healthy again.
func TestHealthyOneFailureIsDegraded(t *testing.T) {
	d := healthy(t)
	if state, _ := d.Fail(at(sec(35)), cfg); state != health.Degraded {
		t.Fatalf("after one failure: %s, want degraded", state)
	}
	if state, _ := d.Succeed(at(sec(40)), cfg); state != health.Healthy {
		t.Errorf("after a success: %s, want healthy again", state)
	}
	if d.FailRun != 0 {
		t.Errorf("the run of failures is %d after a success, want 0", d.FailRun)
	}
}

// P1-FAIL-004
//
// TestHealthyThreeFailuresIsDown checks the third failure in a row takes a Healthy
// direction Down, after Degraded for the first two, and that a success in
// between starts the count again.
func TestHealthyThreeFailuresIsDown(t *testing.T) {
	d := healthy(t)
	for i, want := range []health.LinkHealth{health.Degraded, health.Degraded, health.Down} {
		if state, _ := d.Fail(at(sec(35+5*i)), cfg); state != want {
			t.Fatalf("after failure %d: %s, want %s", i+1, state, want)
		}
	}
	if !d.HoldDownUntil.Equal(at(sec(105))) {
		t.Errorf("hold-down until %v, want 105 s: 60 s from the third failure", d.HoldDownUntil)
	}
	again := healthy(t)
	again.Fail(at(sec(35)), cfg)
	again.Fail(at(sec(40)), cfg)
	again.Succeed(at(sec(45)), cfg)
	if state, _ := again.Fail(at(sec(50)), cfg); state != health.Degraded {
		t.Errorf("a failure after a success: %s, want degraded: the count starts again", state)
	}
}

// P1-FAIL-006
//
// TestSuccessDuringHoldDownIsIgnored checks a success 30 seconds into a 60 second
// hold-down leaves the direction Down, and does not count toward recovery.
func TestSuccessDuringHoldDownIsIgnored(t *testing.T) {
	d := wasDown(t)
	state, reason := d.Succeed(at(sec(75)), cfg)
	if state != health.Down || d.OKRun != 0 || reason == "" {
		t.Errorf("%s with %d good signals (%q), want down with none", state, d.OKRun, reason)
	}
}

// TestHoldDownSuccessesDoNotAddUp checks five good signals during the hold-down
// followed by one after it make the direction Recovering, not Healthy: hold-down
// exists to stop exactly that jump.
func TestHoldDownSuccessesDoNotAddUp(t *testing.T) {
	d := wasDown(t)
	feed(d, true, 5, sec(60))
	if d.State != health.Down {
		t.Fatalf("after successes in the hold-down: %s, want down", d.State)
	}
	if state, _ := d.Succeed(at(sec(110)), cfg); state != health.Recovering {
		t.Errorf("the first success after the hold-down: %s, want recovering", state)
	}
}

// TestHoldDownEndsExactly checks a success at the very instant the hold-down ends
// is allowed, and one nanosecond before is not.
func TestHoldDownEndsExactly(t *testing.T) {
	early := wasDown(t)
	if state, _ := early.Succeed(at(sec(105)).Add(-1), cfg); state != health.Down {
		t.Errorf("a success just before the end: %s, want down", state)
	}
	onTime := wasDown(t)
	if state, _ := onTime.Succeed(at(sec(105)), cfg); state != health.Recovering {
		t.Errorf("a success at the end: %s, want recovering", state)
	}
}

// P1-FAIL-007
//
// TestRecoveryNeedsFiveInARow checks that after the hold-down one success makes
// the direction Recovering, four more make it Healthy, and three more are not
// enough.
func TestRecoveryNeedsFiveInARow(t *testing.T) {
	short := wasDown(t)
	feed(short, true, 4, sec(110))
	if short.State != health.Recovering {
		t.Fatalf("after four good signals: %s, want recovering", short.State)
	}
	if state, _ := short.Succeed(at(sec(130)), cfg); state != health.Healthy {
		t.Errorf("after the fifth: %s, want healthy", state)
	}
	if state, _ := wasDown(t).Succeed(at(sec(61+45)), cfg); state != health.Recovering {
		t.Errorf("a success 61 s after the failure: %s, want recovering", state)
	}
}

// P1-FAIL-008
//
// TestRecoveringFailureIsDownAndHoldDownRestarts checks one failure while
// Recovering sends the direction Down, with a new hold-down counted from the
// failure, not from the first time it went Down.
func TestRecoveringFailureIsDownAndHoldDownRestarts(t *testing.T) {
	d := wasDown(t)
	feed(d, true, 3, sec(110))
	if d.State != health.Recovering {
		t.Fatalf("setup: %s, want recovering", d.State)
	}
	state, _ := d.Fail(at(sec(125)), cfg)
	if state != health.Down || !d.HoldDownUntil.Equal(at(sec(185))) {
		t.Errorf("%s with hold-down until %v, want down until 185 s: 60 s from the failure", state, d.HoldDownUntil)
	}
	if d.OKRun != 0 {
		t.Errorf("the run of good signals is %d, want 0", d.OKRun)
	}
}

// TestFailureWhileDownDoesNotExtendHoldDown checks more failures on a Down
// direction change nothing: it is not kept down longer for failing more.
func TestFailureWhileDownDoesNotExtendHoldDown(t *testing.T) {
	d := wasDown(t)
	state, _ := d.Fail(at(sec(80)), cfg)
	if state != health.Down || !d.HoldDownUntil.Equal(at(sec(105))) {
		t.Errorf("%s with hold-down until %v, want down until 105 s unchanged", state, d.HoldDownUntil)
	}
}

// TestDegradedFailuresReachTheThreshold checks a Degraded direction goes Down when
// the failures in a row, counting the one that made it Degraded, reach the
// threshold, and a changed threshold changes when.
func TestDegradedFailuresReachTheThreshold(t *testing.T) {
	custom := cfg
	custom.FailThreshold = 2
	d := &health.Direction{}
	for i := 0; i < 5; i++ {
		d.Succeed(at(sec(5*i)), custom)
	}
	if state, _ := d.Fail(at(sec(30)), custom); state != health.Degraded {
		t.Fatalf("first failure: %s, want degraded", state)
	}
	if state, _ := d.Fail(at(sec(35)), custom); state != health.Down {
		t.Errorf("second failure with a threshold of 2: %s, want down", state)
	}
}

// TestThresholdsOfOne checks the smallest thresholds: one failure takes a Healthy
// direction straight to Down, one success proves an Unknown one, and after a
// hold-down one success goes Down to Healthy without stopping at Recovering for
// longer than the call.
func TestThresholdsOfOne(t *testing.T) {
	one := cfg
	one.FailThreshold, one.RecoverThreshold = 1, 1
	d := &health.Direction{}
	if state, _ := d.Succeed(at(sec(5)), one); state != health.Healthy {
		t.Fatalf("one success with a threshold of 1: %s, want healthy", state)
	}
	if state, _ := d.Fail(at(sec(10)), one); state != health.Down {
		t.Fatalf("one failure with a threshold of 1: %s, want down", state)
	}
	if state, _ := d.Succeed(at(sec(80)), one); state != health.Healthy {
		t.Errorf("a success after the hold-down with a threshold of 1: %s, want healthy", state)
	}
}

// TestDirectionJSONKeepsItsPlace checks a direction written as JSON and read back
// is the same, hold-down end and all, which is what lets a restart carry on a
// hold-down instead of starting it again.
func TestDirectionJSONKeepsItsPlace(t *testing.T) {
	d := wasDown(t)
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var back health.Direction
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.HoldDownUntil.Equal(d.HoldDownUntil) || back.State != d.State || back.FailRun != d.FailRun || !back.LastEvidence.Equal(d.LastEvidence) {
		t.Errorf("read back %+v from %s, want %+v", back, data, *d)
	}
	if state, _ := back.Succeed(at(sec(75)), cfg); state != health.Down {
		t.Errorf("after the reload a success inside the hold-down: %s, want still down", state)
	}
}
