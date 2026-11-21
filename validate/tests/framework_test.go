package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
	"github.com/vectorihub/netctl/validate"
)

// fakeRules returns two rules of our own, an error and a warning, each making the
// findings it is told to, so that the framework can be tested without the catalogue.
func fakeRules(errorSubjects, warningSubjects []string) []validate.Rule {
	make1 := func(subjects []string) func(*validate.Plan) []validate.Finding {
		return func(*validate.Plan) []validate.Finding {
			var out []validate.Finding
			for _, s := range subjects {
				// The rule writes a wrong rule ID and level on purpose: the framework overrides them.
				out = append(out, validate.Finding{Rule: "wrong", Level: validate.Info, Subject: s, Message: "found " + s})
			}
			return out
		}
	}
	return []validate.Rule{
		{ID: "T-ERR-001", Level: validate.Error, Title: "an error rule", Check: make1(errorSubjects)},
		{ID: "T-WARN-001", Level: validate.Warning, Title: "a warning rule", Check: make1(warningSubjects)},
	}
}

// planWith returns an empty plan whose topology carries the suppressions.
func planWith(s ...topology.Suppression) *validate.Plan {
	return &validate.Plan{Topology: &topology.Topology{Validation: topology.Validation{Suppress: s}}}
}

// TestFindingsGetTheirRulesIDAndLevel checks a finding is stamped with the ID and level
// of the rule that made it, whatever the rule wrote.
func TestFindingsGetTheirRulesIDAndLevel(t *testing.T) {
	res := validate.Run(planWith(), validate.Options{}, fakeRules([]string{"a"}, []string{"b"}))
	if len(res.Findings) != 2 || res.Findings[0].Rule != "T-ERR-001" || res.Findings[0].Level != validate.Error ||
		res.Findings[1].Rule != "T-WARN-001" || res.Findings[1].Level != validate.Warning {
		t.Errorf("findings %+v", res.Findings)
	}
}

// TestFindingsAreSortedByLevelRuleAndSubject checks the order is fixed whatever order
// the rules and their findings came in.
func TestFindingsAreSortedByLevelRuleAndSubject(t *testing.T) {
	rules := fakeRules([]string{"z", "a", "m"}, []string{"y", "b"})
	reversed := []validate.Rule{rules[1], rules[0]}
	a := validate.Run(planWith(), validate.Options{}, rules)
	b := validate.Run(planWith(), validate.Options{}, reversed)
	var got []string
	for _, f := range a.Findings {
		got = append(got, string(f.Level)+":"+f.Subject)
	}
	if want := "error:a error:m error:z warning:b warning:y"; strings.Join(got, " ") != want {
		t.Errorf("order %v, want %s", got, want)
	}
	ja, _ := a.JSON()
	jb, _ := b.JSON()
	if string(ja) != string(jb) {
		t.Error("the order of the rules changed the report")
	}
}

// TestOKMeansNoErrors checks OK, Err and the level accessors.
func TestOKMeansNoErrors(t *testing.T) {
	warn := validate.Run(planWith(), validate.Options{}, fakeRules(nil, []string{"w"}))
	if !warn.OK || warn.Err() != nil || len(warn.Warnings()) != 1 || len(warn.Errors()) != 0 || !warn.Has("T-WARN-001") || warn.Has("T-ERR-001") {
		t.Errorf("warnings only: %+v", warn)
	}
	bad := validate.Run(planWith(), validate.Options{}, fakeRules([]string{"e1", "e2"}, nil))
	if bad.OK || !types.HasCode(bad.Err(), types.ErrValidationFailed) || len(bad.Errors()) != 2 {
		t.Errorf("errors: %+v, %v", bad, bad.Err())
	}
	for _, want := range []string{"2 error(s)", "e1", "e2", "T-ERR-001"} {
		if !strings.Contains(bad.Err().Error(), want) {
			t.Errorf("the error does not say %q: %v", want, bad.Err())
		}
	}
}

// TestAWarningCanBeSuppressedWithAReason checks a valid suppression removes the warning
// from the findings, keeps it on the record with its reason, only for the subject it
// names, and is not reported as unused.
func TestAWarningCanBeSuppressedWithAReason(t *testing.T) {
	sup := topology.Suppression{Rule: "T-WARN-001", Subject: "a", Reason: "accepted until BC-41"}
	res := validate.Run(planWith(sup), validate.Options{}, fakeRules(nil, []string{"a", "b"}))
	if len(res.Findings) != 1 || res.Findings[0].Subject != "b" {
		t.Errorf("findings %+v, want only the warning for b", res.Findings)
	}
	if len(res.Suppressed) != 1 || res.Suppressed[0].Finding.Subject != "a" || res.Suppressed[0].Reason != "accepted until BC-41" {
		t.Errorf("suppressed %+v", res.Suppressed)
	}
	any := topology.Suppression{Rule: "T-WARN-001", Reason: "every subject"}
	if res := validate.Run(planWith(any), validate.Options{}, fakeRules(nil, []string{"a", "b"})); len(res.Findings) != 0 || len(res.Suppressed) != 2 {
		t.Errorf("a suppression with no subject: %+v", res)
	}
}

// TestSuppressionsFromTheCallerAreAppliedToo checks Options.Suppress works like the
// topology file's, and the two add up.
func TestSuppressionsFromTheCallerAreAppliedToo(t *testing.T) {
	file := topology.Suppression{Rule: "T-WARN-001", Subject: "a", Reason: "from the file"}
	call := topology.Suppression{Rule: "T-WARN-001", Subject: "b", Reason: "from the caller"}
	res := validate.Run(planWith(file), validate.Options{Suppress: []topology.Suppression{call}}, fakeRules(nil, []string{"a", "b", "c"}))
	if len(res.Findings) != 1 || res.Findings[0].Subject != "c" || len(res.Suppressed) != 2 {
		t.Errorf("%+v", res)
	}
}

// TestRule_V_SUP_002 is P1-VAL-051: an attempt to suppress an error is ignored, the error
// is still reported, and the attempt adds a finding of its own.
func TestRule_V_SUP_002(t *testing.T) {
	sup := topology.Suppression{Rule: "T-ERR-001", Subject: "a", Reason: "I would rather not"}
	res := validate.Run(planWith(sup), validate.Options{}, fakeRules([]string{"a"}, nil))
	if res.OK || !res.Has("T-ERR-001") || !res.Has("V-SUP-002") {
		t.Fatalf("findings %+v, want the error still there and V-SUP-002", res.Findings)
	}
	if len(res.Suppressed) != 0 {
		t.Errorf("an error was suppressed: %+v", res.Suppressed)
	}
	for _, f := range res.Findings {
		if f.Rule == "V-SUP-002" && (f.Level != validate.Error || !strings.Contains(f.Message, "T-ERR-001")) {
			t.Errorf("the finding about the attempt: %+v", f)
		}
	}
}

// TestRule_V_SUP_001 is P1-VAL-052: a suppression with no reason, or only blanks, is
// rejected: it suppresses nothing, and the warning it was for stands.
func TestRule_V_SUP_001(t *testing.T) {
	for _, reason := range []string{"", "   ", "\t\n"} {
		sup := topology.Suppression{Rule: "T-WARN-001", Subject: "a", Reason: reason}
		res := validate.Run(planWith(sup), validate.Options{}, fakeRules(nil, []string{"a"}))
		if res.OK || !res.Has("V-SUP-001") || !res.Has("T-WARN-001") || len(res.Suppressed) != 0 {
			t.Errorf("reason %q: %+v", reason, res)
		}
	}
}

// TestRule_V_SUP_003 checks a suppression for a rule that does not exist is an error.
func TestRule_V_SUP_003(t *testing.T) {
	sup := topology.Suppression{Rule: "V-NOPE-001", Reason: "a typo"}
	res := validate.Run(planWith(sup), validate.Options{}, fakeRules(nil, nil))
	if res.OK || !res.Has("V-SUP-003") {
		t.Errorf("%+v", res)
	}
}

// TestRule_V_SUP_004 checks a valid suppression that matched nothing is reported as Info,
// which does not stop the plan.
func TestRule_V_SUP_004(t *testing.T) {
	sup := topology.Suppression{Rule: "T-WARN-001", Subject: "gone", Reason: "was needed once"}
	res := validate.Run(planWith(sup), validate.Options{}, fakeRules(nil, []string{"a"}))
	if !res.OK || !res.Has("V-SUP-004") || len(res.Infos()) != 1 || !res.Has("T-WARN-001") {
		t.Errorf("%+v", res)
	}
}

// TestFrameworkFindingsAreNotSuppressible checks the errors about bad suppressions can
// not themselves be suppressed away.
func TestFrameworkFindingsAreNotSuppressible(t *testing.T) {
	bad := topology.Suppression{Rule: "T-WARN-001", Subject: "a"} // no reason
	hide := topology.Suppression{Rule: "V-SUP-001", Reason: "hide it"}
	res := validate.Run(planWith(bad, hide), validate.Options{}, fakeRules(nil, []string{"a"}))
	if res.OK || !res.Has("V-SUP-001") || !res.Has("V-SUP-002") {
		t.Errorf("%+v", res.Findings)
	}
}

// TestRunToleratesAnEmptyPlan checks nothing panics on a nil topology or a nil plan.
func TestRunToleratesAnEmptyPlan(t *testing.T) {
	if res := validate.Run(&validate.Plan{}, validate.Options{}, fakeRules(nil, nil)); !res.OK {
		t.Errorf("%+v", res)
	}
	if res := validate.Run(nil, validate.Options{}, nil); !res.OK || len(res.Findings) != 0 {
		t.Errorf("%+v", res)
	}
}

// TestJSONIsStableAndNeverNull checks the JSON has empty lists and not nulls, the keys
// in a fixed order, and the same bytes every time.
func TestJSONIsStableAndNeverNull(t *testing.T) {
	empty, err := validate.Run(planWith(), validate.Options{}, nil).JSON()
	if err != nil || strings.Contains(string(empty), "null") || !strings.Contains(string(empty), `"findings": []`) {
		t.Errorf("empty result: %v\n%s", err, empty)
	}
	res := validate.Run(planWith(), validate.Options{}, fakeRules([]string{"a"}, nil))
	first, _ := res.JSON()
	for i := 0; i < 10; i++ {
		if again, _ := res.JSON(); string(again) != string(first) {
			t.Fatal("the JSON changed")
		}
	}
	var back validate.Result
	if err := json.Unmarshal(first, &back); err != nil || len(back.Findings) != 1 || back.Findings[0].Rule != "T-ERR-001" {
		t.Errorf("read back %+v, %v", back, err)
	}
	if !strings.HasSuffix(string(first), "}\n") {
		t.Error("no final newline")
	}
}

// TestRulesAreSortedAndACopy checks the catalogue is sorted by ID, holds the framework
// rules, and that changing what Rules returns does not change the catalogue.
func TestRulesAreSortedAndACopy(t *testing.T) {
	rules := validate.Rules()
	for i := 1; i < len(rules); i++ {
		if rules[i-1].ID >= rules[i].ID {
			t.Errorf("%s is not before %s", rules[i-1].ID, rules[i].ID)
		}
	}
	found := map[string]bool{}
	for _, r := range rules {
		found[r.ID] = true
		if r.Title == "" || (r.Level != validate.Error && r.Level != validate.Warning && r.Level != validate.Info) {
			t.Errorf("rule %+v has no title or a bad level", r)
		}
	}
	for _, id := range []string{"V-SUP-001", "V-SUP-002", "V-SUP-003", "V-SUP-004"} {
		if !found[id] {
			t.Errorf("the catalogue lacks %s", id)
		}
	}
	rules[0].ID = "changed"
	if validate.Rules()[0].ID == "changed" {
		t.Error("Rules returned the catalogue's own slice")
	}
}

// TestFindingStringIsOneLine checks the text of a finding.
func TestFindingStringIsOneLine(t *testing.T) {
	f := validate.Finding{Rule: "V-CFG-004", Level: validate.Error, Subject: "builder-1", Message: "oops", Hint: "fix it"}
	if got, want := f.String(), "error V-CFG-004 builder-1: oops (fix it)"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
	f.Hint = ""
	if strings.Contains(f.String(), "(") {
		t.Errorf("an empty hint was written: %q", f.String())
	}
}
