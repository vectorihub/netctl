package validate

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// Level says how serious a finding is.
//
// Example:
//
//	validate.Error means the plan is refused; validate.Warning is recorded and the
//	plan is produced.
type Level string

const (
	// Error means the network would not work, or would be unsafe. The plan is
	// refused.
	//
	// Example:
	//
	//	validate.Error is "error".
	Error Level = "error"
	// Warning means it works, but is risky, wasteful or not yet supported. The plan is
	// produced and the warning recorded.
	//
	// Example:
	//
	//	validate.Warning is "warning".
	Warning Level = "warning"
	// Info is an observation useful when reading a plan. It is recorded only.
	//
	// Example:
	//
	//	validate.Info is "info".
	Info Level = "info"
)

// rank orders levels, most serious first. An unknown level sorts last.
func (level Level) rank() int {
	switch level {
	case Error:
		return 0
	case Warning:
		return 1
	case Info:
		return 2
	}
	return 3
}

// Finding is one thing a rule found.
//
// Example:
//
//	Finding{Rule: "V-CFG-004", Level: Error, Subject: "builder-1",
//		Message: "10.42.32.0/20 is allowed for both aws-hub and dgx-spark",
//		Hint: "a prefix must belong to exactly one peer"}.
type Finding struct {
	// Rule is the ID of the rule that found it.
	//
	// Example:
	//
	//	"V-CFG-004".
	Rule string `json:"rule"`
	// Level is how serious it is. It is always the level of the rule.
	//
	// Example:
	//
	//	validate.Error.
	Level Level `json:"level"`
	// Subject is the node, link or prefix it is about.
	//
	// Example:
	//
	//	"builder-1".
	Subject string `json:"subject"`
	// Message says what is wrong.
	//
	// Example:
	//
	//	"10.42.32.0/20 is allowed for both aws-hub and dgx-spark".
	Message string `json:"message"`
	// Hint says what to change.
	//
	// Example:
	//
	//	"a prefix must belong to exactly one peer".
	Hint string `json:"hint,omitempty"`
	// Detail is the evidence, when there is more than a line of it: the journey of a
	// packet that was lost, hop by hop.
	//
	// Example:
	//
	//	"builder-1 > aws-hub: BLACK_HOLE at aws-hub".
	Detail string `json:"detail,omitempty"`
}

// String writes the finding on one line.
//
// Example:
//
//	"error V-CFG-004 builder-1: 10.42.32.0/20 is allowed for both ...".
func (f Finding) String() string {
	text := fmt.Sprintf("%s %s %s: %s", f.Level, f.Rule, f.Subject, f.Message)
	if f.Hint != "" {
		text += " (" + f.Hint + ")"
	}
	return text
}

// Suppressed is a finding that a suppression accepted, kept so that what was
// accepted, and why, is on the record.
//
// Example:
//
//	Suppressed{Finding: the V-TOPO-003 warning for dgx-spark, Reason: "single uplink accepted (BC-41)"}.
type Suppressed struct {
	// Finding is the finding that was suppressed.
	//
	// Example:
	//
	//	the V-TOPO-003 warning for dgx-spark.
	Finding Finding `json:"finding"`
	// Reason is the reason the file gave.
	//
	// Example:
	//
	//	"single uplink accepted (BC-41)".
	Reason string `json:"reason"`
}

// Result is what validation found.
//
// Example:
//
//	res := validate.Validate(plan, validate.Options{})
//	if !res.OK { return res.Err() }
type Result struct {
	// Findings are the findings that stand, sorted by level, rule, subject and
	// message.
	//
	// Example:
	//
	//	one Warning V-TOPO-004 on the clean three-site plan.
	Findings []Finding `json:"findings"`
	// Suppressed are the findings that were accepted by a suppression.
	//
	// Example:
	//
	//	the V-TOPO-003 warning for dgx-spark, with its reason.
	Suppressed []Suppressed `json:"suppressed"`
	// OK is true when there is no error.
	//
	// Example:
	//
	//	true for the clean three-site plan.
	OK bool `json:"ok"`
}

// level returns the findings of one level.
func (r Result) level(level Level) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Level == level {
			out = append(out, f)
		}
	}
	return out
}

// Errors returns the findings that are errors.
//
// Example:
//
//	for _, f := range res.Errors() { fmt.Println(f) }
func (r Result) Errors() []Finding { return r.level(Error) }

// Warnings returns the findings that are warnings.
//
// Example:
//
//	len(res.Warnings()) is 1 on the clean three-site plan.
func (r Result) Warnings() []Finding { return r.level(Warning) }

// Infos returns the findings that are only information.
//
// Example:
//
//	res.Infos() lists the suppressions that matched nothing.
func (r Result) Infos() []Finding { return r.level(Info) }

// Has reports whether any finding stands for the rule.
//
// Example:
//
//	res.Has("V-TOPO-004") is true on the three-site plan.
func (r Result) Has(rule string) bool {
	return slices.ContainsFunc(r.Findings, func(f Finding) bool { return f.Rule == rule })
}

// Err returns nil when there is no error, and otherwise an error with the code
// VALIDATION_FAILED that lists every error finding, one to a line.
//
// Example:
//
//	if err := res.Err(); err != nil { return err }
func (r Result) Err() error {
	errs := r.Errors()
	if len(errs) == 0 {
		return nil
	}
	lines := make([]string, len(errs))
	for i, f := range errs {
		lines[i] = f.String()
	}
	return types.Errf(types.ErrValidationFailed, "validation", "%d error(s):\n%s", len(errs), strings.Join(lines, "\n"))
}

// JSON writes the result as indented JSON with a final newline. The findings are in
// their fixed order and no finding is a map, so the same plan always gives the same
// bytes.
//
// Example:
//
//	data, err := res.JSON()
func (r Result) JSON() ([]byte, error) {
	out := r
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	if out.Suppressed == nil {
		out.Suppressed = []Suppressed{}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, types.Errf(types.ErrInvalidValue, "validation", "writing it: %v", err)
	}
	return append(data, '\n'), nil
}

// Rule is one check in the catalogue.
//
// Example:
//
//	Rule{ID: "V-CFG-004", Level: Error, Title: "no prefix in two peers' lists", Check: prefixInTwoPeers}.
type Rule struct {
	// ID is the stable ID, such as "V-CFG-004".
	//
	// Example:
	//
	//	"V-CFG-004".
	ID string
	// Level is the level of every finding the rule makes.
	//
	// Example:
	//
	//	validate.Error.
	Level Level
	// Title says in a line what the rule checks.
	//
	// Example:
	//
	//	"no prefix in two peers' allowed_prefixes on the same node".
	Title string
	// Check looks at the plan and returns what it found. It is nil for the rules
	// the framework itself applies (the V-SUP rules).
	//
	// Example:
	//
	//	a function from a *Plan to its findings.
	Check func(p *Plan) []Finding
	// Slow marks a rule that is costly on a large plan, which Options.SkipSweep
	// leaves out. Today that is the single-failure sweep.
	//
	// Example:
	//
	//	true for V-SIM-003.
	Slow bool
}

// registry holds the rules, in the order they were registered.
var registry []Rule

// register adds a rule to the catalogue. Each rules file calls it from init.
func register(id string, level Level, title string, check func(p *Plan) []Finding) {
	registry = append(registry, Rule{ID: id, Level: level, Title: title, Check: check})
}

// registerSlow adds a rule that Options.SkipSweep leaves out.
func registerSlow(id string, level Level, title string, check func(p *Plan) []Finding) {
	registry = append(registry, Rule{ID: id, Level: level, Title: title, Check: check, Slow: true})
}

// Rules returns the whole catalogue, sorted by ID, including the rules the
// framework applies itself. The slice is a copy.
//
// Example:
//
//	for _, r := range validate.Rules() { fmt.Println(r.ID, r.Level, r.Title) }
func Rules() []Rule {
	out := slices.Clone(registry)
	slices.SortFunc(out, func(a, b Rule) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Options changes how Validate runs.
//
// Example:
//
//	validate.Options{Suppress: []topology.Suppression{{Rule: "V-TOPO-003", Subject: "dgx-spark", Reason: "BC-41"}}}.
type Options struct {
	// Suppress lists warnings to accept in addition to the ones in the topology
	// file. The same rules apply to both.
	//
	// Example:
	//
	//	a suppression a caller adds for one run.
	Suppress []topology.Suppression
	// SkipSweep leaves out the single-failure sweep (V-SIM-003), which recomputes the
	// routes once for every link and every hub and is the slowest part of validation
	// on a large network. Nothing else changes.
	//
	// Example:
	//
	//	validate.Options{SkipSweep: true} for a 500-node plan in a hurry.
	SkipSweep bool
}

// Validate runs every rule of the catalogue on the plan, applies the suppressions
// (those in the topology file and in opts), and returns what stands, sorted by
// level, rule, subject and message. It never changes the plan.
//
// Example:
//
//	res := validate.Validate(plan, validate.Options{})
//	if !res.OK { return res.Err() }
func Validate(p *Plan, opts Options) Result { return Run(p, opts, Rules()) }

// Run is Validate with the rules given, so that the framework can be tested on rules
// of its own and a caller can add one. Every finding a rule makes gets that rule's ID
// and level, whatever the rule wrote there. The framework rules are always applied.
//
// Example:
//
//	res := validate.Run(plan, validate.Options{}, validate.Rules())
func Run(p *Plan, opts Options, rules []Rule) Result {
	var found []Finding
	for _, rule := range rules {
		if rule.Check == nil || (rule.Slow && opts.SkipSweep) {
			continue
		}
		for _, f := range rule.Check(p) {
			f.Rule, f.Level = rule.ID, rule.Level
			found = append(found, f)
		}
	}
	var suppressions []topology.Suppression
	if p != nil && p.Topology != nil {
		suppressions = append(suppressions, p.Topology.Validation.Suppress...)
	}
	suppressions = append(suppressions, opts.Suppress...)
	kept, suppressed, extra := applySuppressions(found, suppressions, rules)
	found = append(kept, extra...)
	sortFindings(found)
	slices.SortFunc(suppressed, func(a, b Suppressed) int { return compareFindings(a.Finding, b.Finding) })
	return Result{Findings: found, Suppressed: suppressed, OK: !slices.ContainsFunc(found, func(f Finding) bool { return f.Level == Error })}
}

// compareFindings orders findings by level, rule, subject, message, hint and detail, so that
// two findings that differ anywhere differ in order too, and the report never depends on the
// order they were made in.
func compareFindings(a, b Finding) int {
	return cmp.Or(cmp.Compare(a.Level.rank(), b.Level.rank()), cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Subject, b.Subject),
		cmp.Compare(a.Message, b.Message), cmp.Compare(a.Hint, b.Hint), cmp.Compare(a.Detail, b.Detail))
}

// sortFindings puts findings in their fixed order.
func sortFindings(findings []Finding) { slices.SortFunc(findings, compareFindings) }
