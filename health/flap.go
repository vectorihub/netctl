package health

import (
	"fmt"
	"time"
)

// held reports whether something other than evidence is keeping the link Down:
// an operator's drain or a flap pin. Evidence is still folded into the two
// directions while a link is held, so the record stays true, but it cannot make
// the link usable: only a release can.
func (l *Link) held() bool { return l.Drained || l.Pinned }

// recordFlap notes that the link has just gone Down at the time at, forgets
// the flaps that are older than the window, and pins the link if it has now gone
// Down more than flap_limit times within the window. It returns the reason to
// give for the decision, which says so when the link was pinned.
//
// A link that is already pinned is Down and cannot go Down again, so this is
// never called for one. Going Down because of an operator's drain is not a flap
// and is not counted.
func (l *Link) recordFlap(at time.Time, cfg Config, reason string) string {
	kept := l.Flaps[:0:0]
	for _, flap := range l.Flaps {
		if at.Sub(flap) <= cfg.FlapWindow {
			kept = append(kept, flap)
		}
	}
	l.Flaps = append(kept, at)
	if len(l.Flaps) <= cfg.FlapLimit {
		return reason
	}
	l.Pinned = true
	l.PinnedUntil = at.Add(cfg.PinFor)
	return fmt.Sprintf("flap limit: down %d times within %v (limit %d), pinned down until %s; last cause: %s",
		len(l.Flaps), cfg.FlapWindow, cfg.FlapLimit, l.PinnedUntil.UTC().Format(time.RFC3339), reason)
}

// restart takes both directions of the link Down, with a hold-down that starts
// at the time at, and forgets the evidence gathered while the link was held. It
// is what a release does: a link that was drained or pinned is never trusted
// again on the strength of what it did meanwhile. It must pass the hold-down and
// then prove itself with fresh good signals, which takes it through Recovering.
func (l *Link) restart(at time.Time, cfg Config) {
	for _, node := range l.ends() {
		l.Dirs[node].goDown(at, cfg)
		l.Dirs[node].FailRun = 0
	}
}

// release lifts a flap pin that has run its course at the time now. The pin ends
// at the moment it was due to end, whenever someone looked, so the hold-down
// that follows does not depend on when the next signal happened to arrive. The
// link stays Down until it has proven itself again, and its flap count starts
// over.
func (e *Evaluator) release(l *Link, now time.Time) {
	if !l.Pinned || now.Before(l.PinnedUntil) {
		return
	}
	l.Pinned = false
	l.Flaps = nil
	l.restart(l.PinnedUntil, e.Cfg)
}

// admin applies an operator's signal to a link and returns the decisions it
// caused and, if the signal cannot be used, why.
//
// A signal that is not OK drains the link: it is Down at once and stays Down
// until an operator undrains it. A signal that is OK releases the link: it lifts
// a drain and clears a flap pin. Either way the link is not trusted again
// straight away: the two directions go Down with a fresh hold-down, so it must
// show good signals after the hold-down and pass through Recovering first.
func (e *Evaluator) admin(l *Link, s Signal, decisions []Decision) ([]Decision, string) {
	if !s.OK {
		if l.Drained {
			return decisions, "the link is already drained"
		}
		l.Drained = true
		return e.move(l, s.At, "admin drain: an operator took the link out of service", false, decisions), ""
	}
	if !l.Drained && !l.Pinned {
		return decisions, "the link is neither drained nor pinned, so there is nothing to release"
	}
	cleared := "admin undrain"
	if l.Pinned {
		cleared = "admin clear: an operator lifted the flap pin"
	}
	l.Drained, l.Pinned = false, false
	l.PinnedUntil = time.Time{}
	l.Flaps = nil
	l.restart(s.At, e.Cfg)
	return e.move(l, s.At, cleared+"; the link must now prove itself after the hold-down", false, decisions), ""
}
