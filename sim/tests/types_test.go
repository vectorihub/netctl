package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/sim"
)

// TestHopResultNames checks every hop result's text, parsing it back, the
// zero value, and that an unknown result is refused when written as JSON.
func TestHopResultNames(t *testing.T) {
	all := map[sim.HopResult]string{
		sim.Forwarded: "FORWARDED", sim.Delivered: "DELIVERED", sim.BlackHole: "BLACK_HOLE",
		sim.LinkDown: "LINK_DOWN", sim.TTLExceeded: "TTL_EXCEEDED", sim.LoopDetected: "LOOP",
		sim.WrongNeighbour: "WRONG_NEIGHBOUR",
	}
	for result, name := range all {
		if result.String() != name {
			t.Errorf("%d is %q, want %q", int(result), result.String(), name)
		}
		parsed, ok := sim.ParseHopResult(name)
		if !ok || parsed != result {
			t.Errorf("ParseHopResult(%q) = %v, %v", name, parsed, ok)
		}
		data, err := json.Marshal(result)
		if err != nil || string(data) != `"`+name+`"` {
			t.Errorf("JSON of %s = %s, %v", name, data, err)
		}
		var back sim.HopResult
		if err := json.Unmarshal(data, &back); err != nil || back != result {
			t.Errorf("round trip of %s gave %v, %v", name, back, err)
		}
	}
	var zero sim.HopResult
	if zero != sim.HopUnknown || zero.String() != "UNKNOWN" {
		t.Errorf("the zero hop result is %q, want UNKNOWN", zero.String())
	}
	if _, err := json.Marshal(zero); err == nil {
		t.Error("an unknown hop result was written as JSON")
	}
	if _, ok := sim.ParseHopResult("loop"); ok {
		t.Error("a different capitalisation parsed")
	}
	back := sim.Delivered
	if err := json.Unmarshal([]byte(`"NOPE"`), &back); err == nil || back != sim.Delivered {
		t.Errorf("an unknown name gave %v, %v; want an error and no change", back, err)
	}
}

// TestVerdictNamesAndGate checks every verdict's text, its round trip, and
// whether it fails the gate, against the table in the engineering plan §11.6.
func TestVerdictNamesAndGate(t *testing.T) {
	table := []struct {
		verdict sim.Verdict
		name    string
		fails   bool
		warns   bool
	}{
		{sim.VerdictOK, "OK", false, false},
		{sim.VerdictOKAsymmetricAllowed, "OK_ASYMMETRIC_ALLOWED", false, true},
		{sim.VerdictAsymmetric, "ASYMMETRIC", true, false},
		{sim.VerdictNoReturn, "NO_RETURN", true, false},
		{sim.VerdictBlackHole, "BLACK_HOLE", true, false},
		{sim.VerdictLoop, "LOOP", true, false},
		{sim.VerdictLinkDown, "LINK_DOWN", true, false},
		{sim.VerdictUnreachableExpected, "UNREACHABLE_EXPECTED", false, false},
		{sim.VerdictWrongNeighbour, "WRONG_NEIGHBOUR", true, false},
		{sim.VerdictTTLExceeded, "TTL_EXCEEDED", true, false},
		{sim.VerdictUnexpectedDelivery, "UNEXPECTED_DELIVERY", true, false},
		{sim.VerdictNoHealthyPath, "NO_HEALTHY_PATH", false, false},
	}
	for _, c := range table {
		if c.verdict.String() != c.name || c.verdict.FailsGate() != c.fails || c.verdict.Warning() != c.warns {
			t.Errorf("%s: name %q, fails the gate %v, warning %v; want %v and %v",
				c.name, c.verdict.String(), c.verdict.FailsGate(), c.verdict.Warning(), c.fails, c.warns)
		}
		parsed, ok := sim.ParseVerdict(c.name)
		if !ok || parsed != c.verdict {
			t.Errorf("ParseVerdict(%q) = %v, %v", c.name, parsed, ok)
		}
		data, err := json.Marshal(c.verdict)
		if err != nil || string(data) != `"`+c.name+`"` {
			t.Errorf("JSON of %s = %s, %v", c.name, data, err)
		}
	}
	var unknown sim.Verdict
	if unknown != sim.VerdictUnknown || !unknown.FailsGate() || unknown.String() != "UNKNOWN" {
		t.Error("the zero verdict must be UNKNOWN and must fail the gate, so an unjudged flow cannot pass")
	}
	if _, err := json.Marshal(unknown); err == nil {
		t.Error("an unknown verdict was written as JSON")
	}
	var back sim.Verdict
	if err := json.Unmarshal([]byte(`"MAYBE"`), &back); err == nil || back != sim.VerdictUnknown {
		t.Errorf("an unknown verdict name gave %v, %v", back, err)
	}
}

// TestFamilyNames checks the flow families' text and JSON, and the unknown zero.
func TestFamilyNames(t *testing.T) {
	for family, name := range map[sim.Family]string{
		sim.FamilyOverlay: "overlay", sim.FamilySite: "site", sim.FamilyDeny: "deny", sim.FamilyNegative: "negative",
	} {
		data, err := json.Marshal(family)
		if err != nil || string(data) != `"`+name+`"` || family.String() != name {
			t.Errorf("%s: String %q, JSON %s, %v", name, family.String(), data, err)
		}
		var back sim.Family
		if err := json.Unmarshal(data, &back); err != nil || back != family {
			t.Errorf("round trip of %s gave %v, %v", name, back, err)
		}
	}
	var zero sim.Family
	if zero.String() != "unknown" {
		t.Errorf("the zero family is %q, want unknown", zero.String())
	}
	if _, err := json.Marshal(zero); err == nil {
		t.Error("an unknown family was written as JSON")
	}
	if err := json.Unmarshal([]byte(`"weird"`), &zero); err == nil {
		t.Error("an unknown family name was read")
	}
}

// TestTraceAccessors checks the helpers on a trace: the nodes visited, the links
// crossed, and the number of crossings, for a delivered journey and for one that
// ended at a loop, where the last hop crosses nothing.
func TestTraceAccessors(t *testing.T) {
	delivered := journey(sim.Delivered, "builder-1", "aws-hub", "dgx-spark")
	if !delivered.Delivered() || delivered.Crossings() != 2 {
		t.Errorf("delivered=%v crossings=%d, want true and 2", delivered.Delivered(), delivered.Crossings())
	}
	if got := strings.Join(nodesText(delivered), ","); got != "builder-1,aws-hub,dgx-spark" {
		t.Errorf("nodes = %s", got)
	}
	links := delivered.Links()
	if len(links) != 2 || links[0] != "aws-hub~builder-1" || links[1] != "aws-hub~dgx-spark" {
		t.Errorf("links = %v", links)
	}
	looped := journey(sim.LoopDetected, "builder-1", "aws-hub", "builder-1")
	if looped.Delivered() || looped.Crossings() != 2 {
		t.Errorf("a loop: delivered=%v crossings=%d, want false and 2", looped.Delivered(), looped.Crossings())
	}
	var empty sim.Trace
	if empty.Delivered() || empty.Crossings() != 0 || len(empty.Nodes()) != 0 {
		t.Error("an empty trace delivered something")
	}
}

// nodesText returns a trace's nodes as strings.
func nodesText(trace sim.Trace) []string {
	var out []string
	for _, node := range trace.Nodes() {
		out = append(out, string(node))
	}
	return out
}

// result builds a flow result with a label and a verdict, for report tests.
func result(label string, verdict sim.Verdict, problems ...sim.Problem) sim.FlowResult {
	return sim.FlowResult{Flow: sim.Flow{Label: label}, Verdict: verdict, Problems: problems}
}

// TestNewReportSummary checks the report: flows sorted by label, counts per
// verdict in a fixed order, counts per problem kind, warnings and failures, and
// the passed flag.
func TestNewReportSummary(t *testing.T) {
	input := []sim.FlowResult{
		result("c", sim.VerdictNoReturn, sim.Problem{Kind: sim.ProblemBlackHole}, sim.Problem{Kind: sim.ProblemBlackHole}),
		result("a", sim.VerdictOK),
		result("d", sim.VerdictOKAsymmetricAllowed, sim.Problem{Kind: sim.ProblemAsymmetric}),
		result("b", sim.VerdictOK),
		result("e", sim.VerdictUnreachableExpected),
	}
	report := sim.NewReport(input)
	var labels []string
	for _, flow := range report.Flows {
		labels = append(labels, flow.Flow.Label)
	}
	if strings.Join(labels, "") != "abcde" {
		t.Errorf("flows are in order %v, want sorted by label", labels)
	}
	if input[0].Flow.Label != "c" {
		t.Error("NewReport reordered its input")
	}
	s := report.Summary
	if s.Total != 5 || s.Failed != 1 || s.Warnings != 1 || report.Passed {
		t.Errorf("total %d failed %d warnings %d passed %v, want 5, 1, 1, false", s.Total, s.Failed, s.Warnings, report.Passed)
	}
	wantVerdicts := []sim.VerdictCount{
		{Verdict: sim.VerdictOK, Count: 2}, {Verdict: sim.VerdictOKAsymmetricAllowed, Count: 1},
		{Verdict: sim.VerdictUnreachableExpected, Count: 1}, {Verdict: sim.VerdictNoReturn, Count: 1},
	}
	if len(s.Verdicts) != len(wantVerdicts) {
		t.Fatalf("verdict counts = %v, want %v", s.Verdicts, wantVerdicts)
	}
	for i, want := range wantVerdicts {
		if s.Verdicts[i] != want {
			t.Errorf("verdict count %d = %v, want %v", i, s.Verdicts[i], want)
		}
	}
	if s.Count(sim.VerdictOK) != 2 || s.Count(sim.VerdictLoop) != 0 {
		t.Errorf("Count: OK %d, LOOP %d, want 2 and 0", s.Count(sim.VerdictOK), s.Count(sim.VerdictLoop))
	}
	wantProblems := []sim.ProblemCount{{Kind: sim.ProblemAsymmetric, Count: 1}, {Kind: sim.ProblemBlackHole, Count: 2}}
	if len(s.Problems) != 2 || s.Problems[0] != wantProblems[0] || s.Problems[1] != wantProblems[1] {
		t.Errorf("problem counts = %v, want %v", s.Problems, wantProblems)
	}
}

// TestNewReportPassesWithOnlyWarnings checks that a report whose flows are OK,
// allowed asymmetry, expected unreachable or partitioned passes, and that a
// report of no flows passes vacuously and says it tested nothing.
func TestNewReportPassesWithOnlyWarnings(t *testing.T) {
	report := sim.NewReport([]sim.FlowResult{
		result("a", sim.VerdictOK), result("b", sim.VerdictOKAsymmetricAllowed),
		result("c", sim.VerdictUnreachableExpected), result("d", sim.VerdictNoHealthyPath),
	})
	if !report.Passed || report.Summary.Failed != 0 || report.Summary.Warnings != 1 {
		t.Errorf("passed %v failed %d warnings %d", report.Passed, report.Summary.Failed, report.Summary.Warnings)
	}
	empty := sim.NewReport(nil)
	if !empty.Passed || empty.Summary.Total != 0 || empty.Flows == nil {
		t.Errorf("an empty report: passed %v total %d flows %v", empty.Passed, empty.Summary.Total, empty.Flows)
	}
	unjudged := sim.NewReport([]sim.FlowResult{result("x", sim.VerdictUnknown)})
	if unjudged.Passed {
		t.Error("a flow that was never judged passed")
	}
}
