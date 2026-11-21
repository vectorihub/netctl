package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/topology"
)

// The rules the framework applies itself, to suppressions. They have no Check: the
// framework makes their findings, and they are in the catalogue so that they have IDs
// and tests like every other rule.
func init() {
	for _, r := range []struct {
		id    string
		level Level
		title string
	}{
		{"V-SUP-001", Error, "a suppression has a reason"},
		{"V-SUP-002", Error, "an error cannot be suppressed"},
		{"V-SUP-003", Error, "a suppression names a rule that exists"},
		{"V-SUP-004", Info, "a suppression matched something"},
	} {
		register(r.id, r.level, r.title, nil)
	}
}

// applySuppressions applies the suppressions to the findings. It returns the findings
// that stand, the ones that were accepted with their reasons, and a finding for every
// suppression that is not valid or matched nothing.
//
// A suppression is valid when it has a reason (V-SUP-001), names a rule of the
// catalogue (V-SUP-003) and that rule is not an error (V-SUP-002). An invalid one
// suppresses nothing. Only the findings of a valid suppression's rule, and its
// subject if it names one, are accepted. The framework's own findings are never
// suppressed.
func applySuppressions(found []Finding, suppressions []topology.Suppression, rules []Rule) (kept []Finding, accepted []Suppressed, extra []Finding) {
	levels := map[string]Level{}
	for _, r := range registry {
		if r.Check == nil {
			levels[r.ID] = r.Level // the framework's own rules are always known
		}
	}
	for _, r := range rules {
		levels[r.ID] = r.Level
	}
	used := make([]bool, len(suppressions))
	valid := make([]bool, len(suppressions))
	for i, s := range suppressions {
		subject := "suppression of " + s.Rule
		if s.Subject != "" {
			subject += " for " + s.Subject
		}
		level, known := levels[s.Rule]
		switch {
		case strings.TrimSpace(s.Reason) == "":
			extra = append(extra, Finding{Rule: "V-SUP-001", Level: Error, Subject: subject,
				Message: "a suppression must give a reason, and this one gives none", Hint: "write why the warning is accepted, with a ticket if there is one"})
		case !known:
			extra = append(extra, Finding{Rule: "V-SUP-003", Level: Error, Subject: subject,
				Message: fmt.Sprintf("there is no rule %q", s.Rule), Hint: "use the ID of a rule of the catalogue, such as V-TOPO-003"})
		case level == Error:
			extra = append(extra, Finding{Rule: "V-SUP-002", Level: Error, Subject: subject,
				Message: fmt.Sprintf("%s is an error rule, and an error can never be suppressed", s.Rule), Hint: "fix what the rule found"})
		default:
			valid[i] = true
		}
	}
	for _, f := range found {
		suppressedBy := -1
		for i, s := range suppressions {
			if valid[i] && s.Rule == f.Rule && (s.Subject == "" || s.Subject == f.Subject) {
				suppressedBy = i
				break
			}
		}
		if suppressedBy < 0 {
			kept = append(kept, f)
			continue
		}
		used[suppressedBy] = true
		accepted = append(accepted, Suppressed{Finding: f, Reason: suppressions[suppressedBy].Reason})
	}
	for i, s := range suppressions {
		if valid[i] && !used[i] {
			subject := s.Rule
			if s.Subject != "" {
				subject += " for " + s.Subject
			}
			extra = append(extra, Finding{Rule: "V-SUP-004", Level: Info, Subject: "suppression of " + subject,
				Message: "it matched no finding", Hint: "remove it if the problem is gone"})
		}
	}
	slices.SortFunc(extra, compareFindings)
	return kept, accepted, extra
}
