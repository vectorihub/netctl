package health

import "time"

// Direction is the state machine for one direction of a link: what one end sees
// of the other. A link has two, and is only as good as the worse of them.
//
// A Direction is plain data and holds every timer it needs (hold-down end,
// last evidence) as a time, never as a running clock, so it can be written out
// and read back without losing its place. It never reads the time itself: each
// method is told when the evidence arrived.
//
// Example:
//
//	a new Direction is Unknown; Succeed five times makes it Healthy, and Fail three
//	times after that makes it Down with a hold-down of 60 seconds.
type Direction struct {
	// State is where the machine is.
	State LinkHealth `json:"state"`
	// OKRun is how many good signals in a row have come in.
	OKRun int `json:"consecutive_ok"`
	// FailRun is how many failures in a row have come in.
	FailRun int `json:"consecutive_fail"`
	// HoldDownUntil is when a Down direction may first start to recover. It is
	// the zero time when the direction is not holding down.
	HoldDownUntil time.Time `json:"hold_down_until,omitzero"`
	// LastEvidence is when the last probe or handshake, good or bad, arrived. A
	// trusted direction that goes quiet is judged against it.
	LastEvidence time.Time `json:"last_evidence,omitzero"`
	// LastSignal is the time of the newest signal of any kind applied to the
	// direction, interface signals included. The evaluator skips a signal older
	// than this, so a late-arriving success cannot erase a failure that came
	// after it.
	LastSignal time.Time `json:"last_signal,omitzero"`
}

// trusted reports whether the direction has been proven and so can be judged
// stale: Healthy, Degraded or Recovering. A direction that was never proven, or
// is already down, has nothing to lose by silence.
func (d *Direction) trusted() bool {
	return d.State == Healthy || d.State == Degraded || d.State == Recovering
}

// goDown moves the direction to Down and starts its hold-down from at.
func (d *Direction) goDown(at time.Time, cfg Config) {
	d.State = Down
	d.OKRun = 0
	d.HoldDownUntil = at.Add(cfg.HoldDown)
}

// Succeed records one good probe or handshake that arrived at the given time,
// and returns the new state and the reason for it.
//
// What a success does depends on where the direction is:
//   - Unknown: counts toward RecoverThreshold, and at the threshold the
//     direction is Healthy.
//   - Healthy: nothing changes.
//   - Degraded: the direction is Healthy again, since it had been proven.
//   - Down, during the hold-down: nothing; the success is ignored and the run of
//     good signals starts again, so that successes during the hold-down cannot
//     add up to a recovery the moment it ends.
//   - Down, once the hold-down has ended: the direction is Recovering, and this
//     success is the first of its run.
//   - Recovering: counts toward RecoverThreshold, and at the threshold the
//     direction is Healthy.
//
// Example:
//
//	with the default settings, a direction that is Unknown becomes Healthy
//	on its fifth good signal in a row, and not before.
func (d *Direction) Succeed(at time.Time, cfg Config) (LinkHealth, string) {
	d.LastEvidence = at
	d.FailRun = 0
	d.OKRun++
	switch d.State {
	case Unknown:
		if d.OKRun >= cfg.RecoverThreshold {
			d.State = Healthy
			return d.State, "enough consecutive good signals"
		}
		return d.State, "good signal, not yet enough to trust"
	case Degraded:
		d.State = Healthy
		return d.State, "success after a single failure"
	case Down:
		if at.Before(d.HoldDownUntil) {
			d.OKRun = 0
			return d.State, "hold-down: success ignored"
		}
		d.State = Recovering
		fallthrough
	case Recovering:
		if d.OKRun >= cfg.RecoverThreshold {
			d.State = Healthy
			return d.State, "enough consecutive good signals after hold-down"
		}
		return d.State, "recovering: good signal after hold-down"
	}
	return d.State, "success"
}

// Fail records one failed probe or handshake that arrived at the given time, and
// returns the new state and the reason for it.
//
// What a failure does depends on where the direction is:
//   - Unknown: Down at once, and the hold-down starts. A link that fails its
//     first test has not earned a second chance quickly.
//   - Healthy: Degraded. Routes do not change yet; if this was the
//     FailThreshold-th failure in a row (a threshold of one) it is Down.
//   - Degraded: Down once FailThreshold failures have come in a row.
//   - Recovering: Down, and the hold-down starts again from this moment.
//   - Down: nothing changes, and the hold-down is not extended.
//
// Example:
//
//	with the default settings, a Healthy direction is Degraded after one
//	failure and Down after the third in a row.
func (d *Direction) Fail(at time.Time, cfg Config) (LinkHealth, string) {
	d.LastEvidence = at
	d.OKRun = 0
	d.FailRun++
	switch d.State {
	case Unknown:
		d.goDown(at, cfg)
		return d.State, "failure before the link was trusted"
	case Recovering:
		d.goDown(at, cfg)
		return d.State, "failure while recovering: hold-down restarts"
	case Healthy, Degraded:
		if d.FailRun >= cfg.FailThreshold {
			d.goDown(at, cfg)
			return d.State, "consecutive failures reached the threshold"
		}
		d.State = Degraded
		return d.State, "failure"
	}
	return d.State, "failure while down"
}

// InterfaceDown records that the operating system reports the interface gone or
// down at the given time. An interface that is gone certainly carries nothing,
// so unlike a failed probe this does not wait for a threshold: a direction that
// is not already Down goes Down at once and its hold-down starts. It is not
// evidence of anything else, so it does not change LastEvidence.
//
// There is no counterpart for an interface that is up. Interface state can lower
// health and can never raise it; see SignalInterface.
//
// Example:
//
//	a Healthy direction becomes Down on the first interface-down signal.
func (d *Direction) InterfaceDown(at time.Time, cfg Config) (LinkHealth, string) {
	d.OKRun = 0
	d.FailRun++
	if d.State == Down {
		return d.State, "interface down while already down"
	}
	d.goDown(at, cfg)
	return d.State, "interface is down"
}

// CheckStale judges the direction's silence at the time now. A trusted direction
// (Healthy, Degraded or Recovering) whose last evidence is more than StaleAfter
// old is Down, and its hold-down starts from the moment it went stale, which is
// LastEvidence plus StaleAfter, not from now, so the result does not depend on
// when somebody happened to look. It returns that moment and true when the
// direction went Down, and false when nothing changed.
//
// A direction that is Unknown or already Down is never stale: it is not trusted,
// so there is nothing to withdraw. Silence of exactly StaleAfter is not yet stale.
//
// Example:
//
//	with the default settings, a Healthy direction last heard from at 25 s
//	is still Healthy when checked at 40 s, and Down, since 40 s, when checked at 41 s.
func (d *Direction) CheckStale(now time.Time, cfg Config) (time.Time, bool) {
	if !d.trusted() || now.Sub(d.LastEvidence) <= cfg.StaleAfter {
		return time.Time{}, false
	}
	wentStale := d.LastEvidence.Add(cfg.StaleAfter)
	d.goDown(wentStale, cfg)
	return wentStale, true
}
