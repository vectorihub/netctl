// Package validate is the last gate before a configuration can leave the
// controller. It checks the whole plan, from the address plan and the topology to
// the routes and the generated configs, and refuses anything that would not work.
// If [Validate] returns an error, no configuration is produced; from Phase 2, nothing
// is applied.
//
// It catches what no single component can see on its own: an allocation that is fine
// for the address plan but routes to nothing, a route that is fine for the engine but
// has no matching WireGuard allowed prefix, a config that is fine on one node but
// contradicts its peer.
//
// # Levels
//
// A [Finding] is an Error (the network would not work, or would be unsafe: the plan
// is refused), a Warning (it works, but is risky, wasteful or not yet supported: the
// plan is produced and the warning recorded) or Info (an observation, recorded only).
//
// # Rules
//
// Every rule has a stable ID such as V-CFG-004 so that results can be tracked over
// time and a warning can be accepted by ID. The catalogue is in [Rules]; each rule is
// one function with its own test that plants the fault and checks the rule fires, and
// one that checks it stays quiet on the clean three-site plan. A meta-test fails if a
// rule has no firing test.
//
// # Suppressions
//
// A warning can be suppressed in the topology file, by rule and optionally subject,
// with a reason that is mandatory. An error can never be suppressed: the attempt is
// ignored and adds a finding of its own. So does a suppression with no reason or for
// a rule that does not exist. A suppression that matched nothing is reported as Info.
//
// # Same plan, same findings
//
// Findings are sorted by level, rule, subject and message, so the same plan always
// gives the same report, as text or as JSON.
package validate
