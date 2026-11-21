package tests

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// update rewrites the golden files from the current output instead of comparing
// with them: go test ./sim/tests -run Golden -update. Read the diff before
// committing it.
var update = flag.Bool("update", false, "rewrite the golden report files")

// directLinkDown runs the direct-link-down scenario on the three-site network and
// returns its result.
func directLinkDown(t *testing.T, in sim.ScenarioInput) sim.ScenarioResult {
	t.Helper()
	result, err := sim.RunScenario(in, sim.Scenario{Name: "direct-link-down", LinksDown: []types.LinkID{"builder-1~dgx-spark"}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// golden compares got with the golden file, or rewrites the file under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "../../testdata/golden/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden file: %v (create it with -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file; run with -update and review the change\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

// P1-SIM-045
//
// TestReportJSONIsByteStable runs the same scenario twice from scratch, once with
// the graph's nodes and edges and the flows in a different order, and checks the
// JSON is byte for byte the same, and the same as the reviewed golden file.
func TestReportJSONIsByteStable(t *testing.T) {
	first, err := directLinkDown(t, threeSiteScenarioInput(t)).JSON()
	if err != nil {
		t.Fatal(err)
	}
	shuffled := threeSiteScenarioInput(t)
	rng := rand.New(rand.NewPCG(1, 2))
	shuffled.Graph = fixture.ThreeSite(true).Shuffled(rng)
	rng.Shuffle(len(shuffled.Flows), func(i, j int) { shuffled.Flows[i], shuffled.Flows[j] = shuffled.Flows[j], shuffled.Flows[i] })
	second, err := directLinkDown(t, shuffled).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two runs of the same scenario gave different JSON")
	}
	golden(t, "sim-report-direct-link-down.json", first)
}

// TestReportJSONIsValidAndOrdered checks the output parses, ends in a newline,
// and starts its keys in the fixed order name, report.
func TestReportJSONIsValidAndOrdered(t *testing.T) {
	data, err := directLinkDown(t, threeSiteScenarioInput(t)).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) || !bytes.HasSuffix(data, []byte("}\n")) || !bytes.HasPrefix(data, []byte("{\n  \"name\": \"direct-link-down\",\n  \"report\": {\n    \"flows\"")) {
		t.Errorf("unexpected JSON shape: %.120s", data)
	}
	plain, err := directLinkDown(t, threeSiteScenarioInput(t)).Report.JSON()
	if err != nil || !bytes.HasPrefix(plain, []byte("{\n  \"flows\"")) {
		t.Errorf("Report.JSON: %.60s, %v", plain, err)
	}
}

// TestReportJSONRefusesUnknownVerdict checks a report holding a flow that was
// never judged cannot be written out, so a half-built report cannot be mistaken
// for a result.
func TestReportJSONRefusesUnknownVerdict(t *testing.T) {
	report := sim.NewReport([]sim.FlowResult{{Flow: sim.Flow{Label: "x", Family: sim.FamilyOverlay}}})
	if _, err := report.JSON(); err == nil {
		t.Error("a flow with no verdict was written as JSON")
	}
}

// TestReportTextGolden compares the verbose text of the direct-link-down report
// with the reviewed golden file. It shows the plan's layout: a header, the
// verdict counts, PASS, then each flow's forward and return journey, link by
// link.
func TestReportTextGolden(t *testing.T) {
	text := directLinkDown(t, threeSiteScenarioInput(t)).Text(sim.TextOptions{Title: "three-site", Verbose: true})
	golden(t, "sim-report-direct-link-down.txt", []byte(text))
}

// count returns a summary line as the text report writes it: two spaces, the
// verdict name padded to 24 characters, and the count right-aligned in five.
func count(verdict string, n int) string { return fmt.Sprintf("  %-24s%5d\n", verdict, n) }

// TestReportTextLayout checks the header, the counts and the plan's example flow
// block on the three-site network.
func TestReportTextLayout(t *testing.T) {
	text := directLinkDown(t, threeSiteScenarioInput(t)).Text(sim.TextOptions{Title: "three-site", Verbose: true})
	for _, want := range []string{
		"SIMULATION three-site · scenario direct-link-down · 16 flows\n",
		count("OK", 13), count("ASYMMETRIC", 0), count("UNREACHABLE_EXPECTED", 3),
		"\nPASS\n",
		"builder-1→dgx-spark (overlay) (10.42.240.2 → 10.42.240.3) OK\n" +
			"  fwd: builder-1 ─[aws-hub~builder-1]→ aws-hub ─[aws-hub~dgx-spark]→ dgx-spark  DELIVERED (2 hops)\n" +
			"  ret: dgx-spark ─[aws-hub~dgx-spark]→ aws-hub ─[aws-hub~builder-1]→ builder-1  DELIVERED (2 hops)\n" +
			"  symmetric: yes\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text lacks %q:\n%s", want, text)
		}
	}
}

// TestReportTextHidesPassingFlowsUnlessVerbose checks the short form lists no
// flow when all pass, and the verbose form lists every one.
func TestReportTextHidesPassingFlowsUnlessVerbose(t *testing.T) {
	result := directLinkDown(t, threeSiteScenarioInput(t))
	short := result.Text(sim.TextOptions{})
	if strings.Contains(short, "fwd:") || !strings.HasPrefix(short, "SIMULATION · scenario direct-link-down · 16 flows\n") {
		t.Errorf("the short report lists flows or has the wrong header:\n%s", short)
	}
	if got := strings.Count(result.Text(sim.TextOptions{Verbose: true}), "  fwd: "); got != 16 {
		t.Errorf("the verbose report has %d flow blocks, want 16", got)
	}
}

// TestReportTextShowsFailures plants a fault and checks the report says FAIL, and
// lists the failing flow with where it went wrong.
func TestReportTextShowsFailures(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(false))
	tables := corrupt(t, p.tables, "aws-hub", "", dgxPrefixes...)
	net := sim.NewNet(p.graph, tables, p.owned, p.local)
	report := sim.NewReport([]sim.FlowResult{net.Run(overlayFlow("builder-1", "dgx-spark"), false)})
	text := report.Text(sim.TextOptions{Title: "broken"})
	for _, want := range []string{
		"SIMULATION broken · 1 flow\n", count("BLACK_HOLE", 1), "\nFAIL (1 failed)\n",
		"builder-1→dgx-spark (overlay) (10.42.240.2 → 10.42.240.3) BLACK_HOLE\n",
		"  fwd: builder-1 ─[aws-hub~builder-1]→ aws-hub  BLACK_HOLE (1 hop)\n",
		"  ret: not sent: the forward packet was not delivered\n",
		"  problem: aws-hub has no route for 10.42.240.3\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text lacks %q:\n%s", want, text)
		}
	}
}

// TestScenarioTextShowsMismatchesAndCutOffPairs checks a scenario with a wrong
// expectation says FAIL with the count not met, names the flow, and a scenario
// that cuts nodes off lists the pairs.
func TestScenarioTextShowsMismatchesAndCutOffPairs(t *testing.T) {
	in := threeSiteScenarioInput(t)
	wrong, err := sim.RunScenario(in, sim.Scenario{
		Name: "wrong", Expect: []sim.Expectation{{From: "builder-1", To: "dgx-spark", Verdict: sim.VerdictLoop}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := wrong.Text(sim.TextOptions{})
	if !strings.Contains(text, "\nFAIL (1 expectation not met)\n") ||
		!strings.Contains(text, "EXPECTATION NOT MET: builder-1→dgx-spark (overlay): expected LOOP, got OK\n") {
		t.Errorf("the text does not report the mismatch:\n%s", text)
	}
	cut, err := sim.RunScenario(in, sim.Scenario{
		Name: "isolated", LinksDown: []types.LinkID{"builder-1~dgx-spark", "aws-hub~builder-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if text := cut.Text(sim.TextOptions{}); !strings.Contains(text, "NO PATH: builder-1 → aws-hub (no healthy path)\n") {
		t.Errorf("the text does not list the cut-off pair:\n%s", text)
	}
}

// TestReportTextWarning checks a flow allowed to be asymmetric gives PASS with a
// warning count.
func TestReportTextWarning(t *testing.T) {
	flow := overlayFlow("builder-1", "dgx-spark")
	flow.AllowAsymmetric = true
	p := computePlan(t, fixture.ThreeSite(true))
	// Make the reply go by the hub: dgx-spark routes builder-1's address via aws-hub.
	tables := corrupt(t, p.tables, "dgx-spark", "aws-hub", "10.42.240.2/32")
	report := sim.NewReport([]sim.FlowResult{sim.NewNet(p.graph, tables, p.owned, p.local).Run(flow, false)})
	if text := report.Text(sim.TextOptions{}); !strings.Contains(text, "\nPASS (1 warning)\n") ||
		!strings.Contains(text, "OK_ASYMMETRIC_ALLOWED") || !strings.Contains(text, "symmetric: no") {
		t.Errorf("unexpected text:\n%s", text)
	}
}

// TestReportOfNoFlows checks an empty report is written, says 0 flows, and passes
// vacuously.
func TestReportOfNoFlows(t *testing.T) {
	text := sim.NewReport(nil).Text(sim.TextOptions{})
	if !strings.HasPrefix(text, "SIMULATION · 0 flows\n") || !strings.HasSuffix(text, "PASS\n") {
		t.Errorf("unexpected text:\n%s", text)
	}
	if data, err := sim.NewReport(nil).JSON(); err != nil || !bytes.Contains(data, []byte(`"total": 0`)) {
		t.Errorf("JSON of nothing: %s, %v", data, err)
	}
}
