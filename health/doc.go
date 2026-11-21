// Package health decides which links may carry traffic. It never trusts an
// interface because the interface says it is up. A link is trusted only on
// independent evidence that packets really flow end to end, and the evidence
// has to keep arriving.
//
// # Evidence
//
// Evidence is a [Signal]: one end of a link saw something of the other. A probe
// that was answered, a recent WireGuard handshake, an interface that went away,
// an operator's instruction. Signals can be made up by a test or come from real
// probes and WireGuard handshakes; the package cannot tell the difference, so
// they arrive here unchanged. Nothing in this package reads the
// clock or a file: every method is told the time, so a test can replay a
// timeline second for second and get the same answer every run.
//
// # One direction at a time
//
// Each end of a link has its own state machine, a [Direction], with five states:
//
//	Unknown     nothing proven yet
//	Healthy     enough good signals in a row, and nothing bad since
//	Degraded    one failure after Healthy; routes do not change yet
//	Down        failed, or silent for too long; stays down for the hold-down time
//	Recovering  a success after the hold-down; not yet trusted
//
// Unknown becomes Healthy after [Config.RecoverThreshold] good signals in a row,
// and any failure from Unknown is Down. Healthy becomes Degraded on one failure
// and Down on [Config.FailThreshold] in a row; a success from Degraded makes it
// Healthy again. Down waits out the hold-down: successes before it ends are
// ignored and do not add up. After it a success makes the direction Recovering,
// RecoverThreshold in a row make it Healthy, and any failure while Recovering is
// Down again with the hold-down restarting from that moment.
//
// # Both directions together
//
// A [Link] is usable for routing only when it is healthy in both directions. If
// builder-1 hears dgx-spark but dgx-spark does not hear builder-1, the link is
// down for both, so the two ends fail over together and paths stay symmetric.
// A required link (a direct one) is not used until proven; an assumed one (a
// hub-spoke link) is used until a failure arrives.
//
// An interface signal can lower health, since an interface that is gone carries
// nothing, but it can never raise it: an interface that is up says nothing about
// the path. Only probes and handshakes make a link Healthy.
//
// # Silence
//
// A trusted direction that hears nothing for [Config.StaleAfter] is judged
// failed, from the moment it went quiet and not the moment someone looked. The
// check runs before each signal in a batch as well as at the end, so a gap in
// the middle counts.
//
// # Flapping, drains and pins
//
// A link that keeps failing and recovering does more harm than one that stays
// down, so a link that goes Down more than [Config.FlapLimit] times within
// [Config.FlapWindow] is pinned Down for [Config.PinFor], whatever the evidence
// says. The decision that pins it says "flap limit". A pin ends by itself at the
// moment it was due to, or sooner when an operator releases it.
//
// An operator can also drain a link: an admin [Signal] that is not OK takes it
// Down at once and keeps it there until an admin signal that is OK. A drain is
// not a flap. A release, of a drain or a pin, does not hand the link its trust
// back: the hold-down starts again from that moment, and the link must show good
// signals after it and pass through Recovering, however well it behaved while it
// was held. Evidence is still recorded while a link is held, so the record stays
// true, but only a release can make the link usable.
//
// # Using it
//
// Build an [Evaluator] from a [Config] (start with [DefaultConfig]) and the
// links, feed it signals with [Evaluator.Apply], and use
// [Evaluator.UnusableLinks] as the set of links to mark down in the routing
// graph. Apply returns a [Decision] for each change, with the reason, and a
// [Skipped] for each signal it could not use.
package health
