package health

import (
	"cmp"
	"slices"
	"time"

	"github.com/vectorihub/netctl/types"
)

// Link is the health of one link: the state machine of each of its two
// directions, and the state the link has as a whole.
//
// A link is usable for routing only when it is Healthy in both directions. If
// builder-1 hears dgx-spark but dgx-spark does not hear builder-1, the link is
// Down for both, so both ends fail over together and the paths stay symmetric.
//
// Example:
//
//	NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true) is a required
//	direct link; Dirs holds one Direction for builder-1 and one for dgx-spark.
type Link struct {
	// ID is the link's ID.
	ID types.LinkID `json:"link"`
	// Required says the link must be proven before it is used. A direct link is
	// required: it carries traffic only once probes show it works. A hub-spoke
	// link is not: it is assumed to work until a failure arrives, so a new network
	// can route through its hub before any probe has run.
	Required bool `json:"required"`
	// State is the health of the link as a whole, worked out from both
	// directions each time evidence arrives.
	State LinkHealth `json:"state"`
	// Dirs holds the state machine of each end, keyed by the node that observes:
	// Dirs["builder-1"] is what builder-1 sees of dgx-spark.
	Dirs map[types.NodeID]*Direction `json:"directions"`
	// Drained says an operator took the link out of service. A drained link is
	// Down whatever the evidence says, until an operator undrains it.
	//
	// Example:
	//
	//	with a drain applied, Drained is true and the link is Down until an admin signal that is OK releases it.
	Drained bool `json:"drained,omitempty"`
	// Pinned says the link flapped too often and is held Down until PinnedUntil
	// or until an operator clears it.
	//
	// Example:
	//
	//	after a fifth Down within ten minutes, Pinned is true and the link stays Down whatever the probes say.
	Pinned bool `json:"pinned,omitempty"`
	// PinnedUntil is when a flap pin ends by itself. It is zero for a link that
	// is not pinned.
	//
	// Example:
	//
	//	a link pinned at 10:00:45 with a 30 minute pin has PinnedUntil 10:30:45.
	PinnedUntil time.Time `json:"pinned_until,omitzero"`
	// LastAdmin is the time of the newest admin signal applied to the link. An
	// admin signal older than it is skipped, so a late release cannot undo a
	// newer drain. It is zero until an admin signal has been applied.
	//
	// Example:
	//
	//	after a drain stamped 10:01:40, a release stamped 10:00:50 is skipped as outdated.
	LastAdmin time.Time `json:"last_admin,omitzero"`
	// Flaps holds the times the link went Down within the flap window, oldest
	// first. More than the flap limit of them pins the link.
	//
	// Example:
	//
	//	a link that went Down at 10:00:45 and 10:02:25 has those two times in Flaps.
	Flaps []time.Time `json:"flaps,omitempty"`
}

// Clone returns a copy of the link that shares nothing with it, so a caller can
// change the copy, folding signals into it for instance, and leave the original
// as it was.
//
// Example:
//
//	copy := link.Clone(); changing copy.Dirs["builder-1"] does not change link.
func (l *Link) Clone() *Link {
	out := *l
	out.Dirs = make(map[types.NodeID]*Direction, len(l.Dirs))
	for node, d := range l.Dirs {
		dir := *d
		out.Dirs[node] = &dir
	}
	out.Flaps = slices.Clone(l.Flaps)
	return &out
}

// NewLink returns a link between two nodes, Unknown in both directions. It
// refuses a link from a node to itself, or with an end missing, with
// INVALID_TOPOLOGY.
//
// Example:
//
//	NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true) is a
//	required direct link nothing is known about yet, which Usable reports false.
func NewLink(id types.LinkID, a, b types.NodeID, required bool) (*Link, error) {
	if a == "" || b == "" || a == b {
		return nil, types.Errf(types.ErrInvalidTopology, "link "+string(id), "a link needs two different ends, got %q and %q", a, b)
	}
	return &Link{
		ID: id, Required: required, State: Unknown,
		Dirs: map[types.NodeID]*Direction{a: {State: Unknown}, b: {State: Unknown}},
	}, nil
}

// Usable reports whether routing may use the link now.
//
// Healthy and Degraded links are usable: a single lost probe must not move
// traffic. Down and Recovering are not. Unknown depends on the link: a required
// link is not usable until proven, and an assumed link is usable until a failure
// arrives.
//
// Example:
//
//	a new direct link is not usable and a new hub-spoke link is.
func (l *Link) Usable() bool { return l.State.Usable(l.Required) }

// combine works out the health of the link as a whole from its two directions:
// Down while an operator's drain or a flap pin holds it, otherwise as bad as the
// worse of them. Down beats everything, then Recovering, then
// Unknown, then Degraded; Healthy only when both are.
func (l *Link) combine() LinkHealth {
	if l.held() {
		return Down
	}
	seen := map[LinkHealth]bool{}
	for _, d := range l.Dirs {
		seen[d.State] = true
	}
	switch {
	case seen[Down]:
		return Down
	case seen[Recovering]:
		return Recovering
	case seen[Unknown]:
		return Unknown
	case seen[Degraded]:
		return Degraded
	}
	return Healthy
}

// ends returns the nodes that observe the link, sorted, so that anything done to
// both directions is done in a fixed order.
func (l *Link) ends() []types.NodeID {
	nodes := make([]types.NodeID, 0, len(l.Dirs))
	for node := range l.Dirs {
		nodes = append(nodes, node)
	}
	slices.Sort(nodes)
	return nodes
}

// Decision records one change in a link's health, and why.
//
// Example:
//
//	a link going from Healthy to Down at 45 s because three probes failed is one
//	Decision with Before Healthy, After Down and a Reason that says so.
type Decision struct {
	// Link is the link that changed.
	Link types.LinkID `json:"link"`
	// Before and After are the link's health on either side of the change.
	Before LinkHealth `json:"before"`
	// After is the health the link has now.
	After LinkHealth `json:"after"`
	// Reason says in words what caused it.
	Reason string `json:"reason"`
	// At is when it happened: the time of the signal, or for silence the moment
	// the link went stale.
	At time.Time `json:"at"`
}

// Skipped is a signal the evaluator did not apply, with the reason. Nothing is
// dropped silently: the caller sees what was ignored and why.
//
// Example:
//
//	a signal for a link the evaluator does not track is returned as a Skipped with
//	Why "the link is not tracked".
type Skipped struct {
	// Signal is the signal as given.
	Signal Signal `json:"signal"`
	// Why says what was wrong with it.
	Why string `json:"why"`
}

// Evaluator folds signals into the health of a set of links.
//
// It holds state but not a clock: every method is told the time. Give it the same
// links and the same signals and it gives the same answer.
//
// Example:
//
//	NewEvaluator(DefaultConfig(), direct) tracks one link; Apply folds signals into
//	it and UnusableLinks lists the links routing must avoid.
type Evaluator struct {
	// Cfg is the thresholds and times in use.
	Cfg Config
	// Links holds the links, keyed by ID. A signal about a link not in the map
	// is skipped.
	Links map[types.LinkID]*Link
}

// NewEvaluator returns an evaluator for the links. It refuses a Config that does
// not pass Validate (INVALID_CONFIG) and two links with one ID
// (INVALID_TOPOLOGY).
//
// Example:
//
//	NewEvaluator(DefaultConfig(), direct) tracks one link.
func NewEvaluator(cfg Config, links ...*Link) (*Evaluator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	e := &Evaluator{Cfg: cfg, Links: make(map[types.LinkID]*Link, len(links))}
	var problems types.List
	for _, l := range links {
		if _, dup := e.Links[l.ID]; dup {
			problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(l.ID), "it is given more than once"))
		}
		e.Links[l.ID] = l
	}
	if err := problems.Err(); err != nil {
		return nil, err
	}
	return e, nil
}

// linkIDs returns the IDs of the links, sorted.
func (e *Evaluator) linkIDs() []types.LinkID {
	ids := make([]types.LinkID, 0, len(e.Links))
	for id := range e.Links {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// settle recomputes a link's health after one of its directions changed and, if
// the link as a whole is now different, appends a Decision. A change to Down is
// counted as a flap and may pin the link.
func (e *Evaluator) settle(l *Link, at time.Time, reason string, decisions []Decision) []Decision {
	return e.move(l, at, reason, true, decisions)
}

// move is settle with the choice of whether a change to Down counts as a flap:
// an operator's drain does not.
func (e *Evaluator) move(l *Link, at time.Time, reason string, countFlap bool, decisions []Decision) []Decision {
	before, after := l.State, l.combine()
	if before == after {
		return decisions
	}
	l.State = after
	if after == Down && countFlap {
		reason = l.recordFlap(at, e.Cfg, reason)
	}
	return append(decisions, Decision{Link: l.ID, Before: before, After: after, Reason: reason, At: at})
}

// expire first lifts a flap pin that has run its course, then applies the
// staleness rule to both directions of a link at the time
// now, and returns the decisions it caused. When both directions have gone quiet
// the one that went stale first is applied first, whatever the nodes are called,
// so the decision's time and reason describe the real outage and not the order
// of the names.
func (e *Evaluator) expire(l *Link, now time.Time, decisions []Decision) []Decision {
	e.release(l, now)
	type quiet struct {
		node types.NodeID
		at   time.Time
	}
	var stale []quiet
	for _, node := range l.ends() {
		if wentStale, isStale := l.Dirs[node].CheckStale(now, e.Cfg); isStale {
			stale = append(stale, quiet{node, wentStale})
		}
	}
	slices.SortFunc(stale, func(a, b quiet) int { return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.node, b.node)) })
	for _, q := range stale {
		decisions = e.settle(l, q.at, "stale: no probe or handshake from "+string(q.node)+" for more than "+e.Cfg.StaleAfter.String(), decisions)
	}
	return decisions
}

// Apply folds signals into the links' health and returns every change it caused,
// in order, and every signal it could not apply.
//
// The signals are applied in time order, and in the order given for equal times;
// the caller's slice is not reordered. Before each one, silence is judged at the
// time of that signal, so a gap in the middle of a batch counts as much as one at
// the end. After the last, silence is judged at now. The evaluator never reads
// the clock, so now is the only "present" it knows.
//
// What each kind does:
//   - Probe and handshake, good or bad, are evidence and move the state machine
//     of the direction the signal's From end observes.
//   - Interface down takes that direction Down at once. Interface up is valid and
//     does nothing: it is never evidence of health, and a link that has only ever
//     had interface-up signals stays Unknown for as long as you like.
//   - An admin signal that is not OK drains the link: Down at once, and Down
//     until released. An admin signal that is OK releases it, lifting a drain
//     and a flap pin; the link then restarts its hold-down and must prove itself
//     again, through Recovering. An admin signal belongs to the link, not to one
//     end, so its From is not looked at.
//   - A link that goes Down more than flap_limit times within flap_window is
//     pinned Down for pin_for or until an operator releases it. While it is
//     drained or pinned evidence is still recorded but cannot make it usable.
//
// A signal is skipped, not applied, when its link is not tracked, its From is not
// one of the link's ends, its kind is unknown, an admin signal is older than the newest admin signal for the link or asks for nothing
// that can be done (draining a drained link, releasing a link that is neither
// drained nor pinned), or it is
// older than the newest signal already applied to the same direction, in this
// batch or an earlier one: a success that arrives late must not erase a failure
// that came after it.
//
// Example:
//
//	five good probes from each end of a new direct link make it Healthy;
//	three failed ones from each then make it Down.
func (e *Evaluator) Apply(signals []Signal, now time.Time) ([]Decision, []Skipped) {
	ordered := slices.Clone(signals)
	slices.SortStableFunc(ordered, func(a, b Signal) int { return a.At.Compare(b.At) })
	var decisions []Decision
	var skipped []Skipped
	for _, s := range ordered {
		l := e.Links[s.Link]
		if l == nil {
			skipped = append(skipped, Skipped{s, "the link is not tracked"})
			continue
		}
		if s.Kind == SignalAdmin {
			var why string
			if s.At.Before(l.LastAdmin) {
				skipped = append(skipped, Skipped{s, "outdated: older than the newest admin signal already applied to this link"})
				continue
			}
			l.LastAdmin = s.At
			decisions = e.expire(l, s.At, decisions)
			if decisions, why = e.admin(l, s, decisions); why != "" {
				skipped = append(skipped, Skipped{s, why})
			}
			continue
		}
		d := l.Dirs[s.From]
		if d == nil {
			skipped = append(skipped, Skipped{s, string(s.From) + " is not an end of the link"})
			continue
		}
		// An unsupported kind is refused before anything changes, so it can
		// neither advance LastSignal (which would make a real, slightly older
		// signal look outdated) nor trigger an expiry check.
		switch s.Kind {
		case SignalProbe, SignalHandshake, SignalInterface:
		default:
			skipped = append(skipped, Skipped{s, "the signal has no known kind"})
			continue
		}
		if s.At.Before(d.LastSignal) {
			skipped = append(skipped, Skipped{s, "outdated: older than the newest signal already applied to this direction"})
			continue
		}
		decisions = e.expire(l, s.At, decisions)
		d.LastSignal = s.At
		var reason string
		switch s.Kind {
		case SignalProbe, SignalHandshake:
			if s.OK {
				_, reason = d.Succeed(s.At, e.Cfg)
			} else {
				_, reason = d.Fail(s.At, e.Cfg)
			}
			reason = s.Kind.String() + " from " + string(s.From) + ": " + reason
		case SignalInterface:
			if s.OK {
				continue
			}
			_, reason = d.InterfaceDown(s.At, e.Cfg)
			reason = string(s.From) + ": " + reason
		}
		decisions = e.settle(l, s.At, reason, decisions)
	}
	for _, id := range e.linkIDs() {
		decisions = e.expire(e.Links[id], now, decisions)
	}
	return decisions, skipped
}

// UnusableLinks returns the IDs of the tracked links routing must not use right
// now, sorted: the links for which Usable is false. It is the list to mark Down
// in the routing graph, or to hand to a simulation scenario as links_down.
//
// Example:
//
//	with the direct link Down and the hub links assumed, it returns just
//	the direct link's ID.
func (e *Evaluator) UnusableLinks() []types.LinkID {
	var out []types.LinkID
	for _, id := range e.linkIDs() {
		if !e.Links[id].Usable() {
			out = append(out, id)
		}
	}
	return out
}
