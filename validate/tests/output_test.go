package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/validate"
)

// TestTextSaysWhetherThePlanStands checks the first line, the counts and their plurals.
func TestTextSaysWhetherThePlanStands(t *testing.T) {
	ok := validate.Result{OK: true}.Text()
	if ok != "validation: OK (0 errors, 0 warnings, 0 infos)\n" {
		t.Errorf("%q", ok)
	}
	res := validate.Result{OK: false, Findings: []validate.Finding{
		{Rule: "V-CFG-004", Level: validate.Error, Subject: "builder-1", Message: "oops", Hint: "fix it"},
		{Rule: "V-TOPO-004", Level: validate.Warning, Subject: "a~b", Message: "careful"},
		{Rule: "V-SIM-001", Level: validate.Error, Subject: "x", Message: "lost", Detail: "a > b\nBLACK_HOLE"},
	}}
	text := res.Text()
	for _, want := range []string{
		"validation: FAILED (2 errors, 1 warning, 0 infos)",
		"error V-CFG-004 builder-1\n  oops\n  hint: fix it\n",
		"warning V-TOPO-004 a~b\n  careful\n",
		"    | a > b\n    | BLACK_HOLE\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "suppressed") {
		t.Error("an empty suppressed section was written")
	}
}

// TestTextListsWhatWasSuppressed checks accepted findings are on the record with their reasons.
func TestTextListsWhatWasSuppressed(t *testing.T) {
	p := cleanPlan(t, true)
	p.Topology.Validation.Suppress = []topology.Suppression{{Rule: "V-TOPO-004", Subject: string(direct), Reason: "both ends are reachable by relay (BC-12)"}}
	res := validate.Validate(p, validate.Options{})
	if res.Has("V-TOPO-004") {
		t.Fatal("the warning was not suppressed")
	}
	text := res.Text()
	if !strings.Contains(text, "suppressed (1):\n  V-TOPO-004 builder-1~dgx-spark: both ends are reachable by relay (BC-12)\n") {
		t.Errorf("%s", text)
	}
}

// TestTextAndJSONAreStable checks both renderings are the same every time, for a result with
// several findings made by real rules.
func TestTextAndJSONAreStable(t *testing.T) {
	res := validate.Validate(cleanPlan(t, false), validate.Options{})
	text, j := res.Text(), ""
	data, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	j = string(data)
	for i := 0; i < 5; i++ {
		again := validate.Validate(cleanPlan(t, false), validate.Options{})
		d, _ := again.JSON()
		if again.Text() != text || string(d) != j {
			t.Fatal("the rendering changed")
		}
	}
}

// TestCatalogueText checks the catalogue is listed one rule to a line, sorted, with level and title.
func TestCatalogueText(t *testing.T) {
	lines := strings.Split(strings.TrimRight(validate.CatalogueText(), "\n"), "\n")
	if len(lines) != len(validate.Rules()) {
		t.Fatalf("%d lines for %d rules", len(lines), len(validate.Rules()))
	}
	if !strings.HasPrefix(lines[0], "V-ADDR-001") || !strings.Contains(lines[0], "error") || !strings.Contains(lines[0], "no two allocations overlap") {
		t.Errorf("first line %q", lines[0])
	}
	for i := 1; i < len(lines); i++ {
		if lines[i-1] > lines[i] {
			t.Errorf("not sorted at %q", lines[i])
		}
	}
}
