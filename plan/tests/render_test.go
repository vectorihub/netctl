package tests

import (
	"flag"
	"math/rand/v2"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/types"
)

// update rewrites the golden files of this package instead of comparing with them.
var update = flag.Bool("update", false, "rewrite the golden files")

// render renders a result and fails the test on an error.
func render(t testing.TB, r plan.Result) map[string][]byte {
	t.Helper()
	files, err := r.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return files
}

// checkGolden compares text with the reviewed file testdata/golden/plan/<name>, or rewrites
// the file under -update.
func checkGolden(t testing.TB, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", "plan", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the reviewed file; run with -update to review the change\n--- got\n%s", name, got)
	}
}

// TestRenderProducesTheFilesOfAPlan checks the set of files and that plan.json lists them.
func TestRenderProducesTheFilesOfAPlan(t *testing.T) {
	files := render(t, mustPlan(t, fresh(t)))
	var names []string
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	want := []string{"ipam.json", "nodes/aws-hub.json", "nodes/builder-1.json", "nodes/dgx-spark.json", "plan.json", "routes.txt", "simulation.json", "validation.json"}
	if !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	for _, name := range want[:4] {
		if len(files[name]) == 0 || files[name][len(files[name])-1] != '\n' {
			t.Errorf("%s is empty or has no final newline", name)
		}
	}
	if !strings.Contains(string(files["plan.json"]), `"nodes/builder-1.json"`) {
		t.Errorf("plan.json does not list the files:\n%s", files["plan.json"])
	}
}

// TestRenderIsTheSameWhateverTheOrderOfTheFile is P1-E2E-090 for the written bytes: fifty
// shuffled plans render every file identically.
func TestRenderIsTheSameWhateverTheOrderOfTheFile(t *testing.T) {
	want := render(t, mustPlan(t, fresh(t)))
	rng := rand.New(rand.NewPCG(74, 1))
	for i := 0; i < 50; i++ {
		got := render(t, mustPlan(t, shuffle(rng, fresh(t))))
		if len(got) != len(want) {
			t.Fatalf("shuffle %d rendered %d files, want %d", i, len(got), len(want))
		}
		for name, data := range want {
			if string(got[name]) != string(data) {
				t.Fatalf("shuffle %d changed %s", i, name)
			}
		}
	}
}

// TestRoutesTextMatchesTheReviewedFile keeps the text form of the tables from drifting.
func TestRoutesTextMatchesTheReviewedFile(t *testing.T) {
	checkGolden(t, "routes-fresh.txt", mustPlan(t, fresh(t)).RoutesText())
}

// TestAPlanWithNothingToKeepRendersNoNodeFiles checks a refused plan renders the reports
// that say why, and no config.
func TestAPlanWithNothingToKeepRendersNoNodeFiles(t *testing.T) {
	in := fresh(t)
	in.Overrides = loop()
	r, err := plan.Plan(in)
	if !types.HasCode(err, types.ErrValidationFailed) {
		t.Fatalf("want a refusal, got %v", err)
	}
	files := render(t, r)
	for name := range files {
		if strings.HasPrefix(name, "nodes/") {
			t.Errorf("a refused plan rendered %s", name)
		}
	}
	if !strings.Contains(string(files["validation.json"]), "V-SIM-001") {
		t.Error("the validation report does not say why")
	}
}

// TestExplainNamesTheHolderAndTheRoute checks the explanation reads the holder from the
// address plan and that text and JSON agree.
func TestExplainNamesTheHolderAndTheRoute(t *testing.T) {
	r := mustPlan(t, fresh(t))
	dst := netip.MustParseAddr("10.42.33.7")
	text, err := r.ExplainText(builder, dst)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"builder-1 → 10.42.33.7 (site dgx)", "match:", "10.42.32.0/20"} {
		if !strings.Contains(text, want) {
			t.Errorf("the explanation lacks %q:\n%s", want, text)
		}
	}
	data, err := r.ExplainJSON(builder, dst)
	if err != nil || !strings.Contains(string(data), `"owner": "site dgx"`) {
		t.Errorf("JSON %s (%v)", data, err)
	}
	if _, err := r.ExplainText("nobody", dst); !types.HasCode(err, types.ErrNotFound) {
		t.Errorf("an unknown node gave %v, want NOT_FOUND", err)
	}
}

// TestExplainShowsLinkHealthWhenTheGuardRan checks the health note appears once there is
// failover state to read it from.
func TestExplainShowsLinkHealthWhenTheGuardRan(t *testing.T) {
	in := fresh(t)
	in.Signals = probeSignals(true, 5, 5)
	r := planAt(t, in, 30)
	text, err := r.ExplainText(builder, netip.MustParseAddr("10.42.33.7"))
	if err != nil || !strings.Contains(text, string(direct)+" is up") {
		t.Errorf("%v\n%s", err, text)
	}
}

// TestSimulationRenderersAgreeWithTheReport checks the text ends with the verdict and the
// JSON is the report's.
func TestSimulationRenderersAgreeWithTheReport(t *testing.T) {
	r := mustPlan(t, fresh(t))
	if text := r.SimulationText(false); !strings.HasSuffix(strings.TrimSpace(text), "PASS") {
		t.Errorf("the report does not end PASS:\n%s", text)
	}
	data, err := r.SimulationJSON()
	if err != nil || string(data) != string(render(t, r)["simulation.json"]) {
		t.Errorf("simulation.json differs from SimulationJSON (%v)", err)
	}
}

// TestDiffBetweenAPlanAndItsFailover checks what a failover changes on the machines: the two
// ends of the direct link move their routes, the hub does not.
func TestDiffBetweenAPlanAndItsFailover(t *testing.T) {
	in := fresh(t)
	in.Signals = probeSignals(true, 5, 5)
	before := planAt(t, in, 30)

	next := fresh(t)
	next.IPAM, next.Previous, next.Failover = before.IPAM, &before, &before.Failover.State
	next.Signals = probeSignals(false, 3, 35)
	after := planAt(t, next, 50)

	d := plan.Diff(before, after)
	if d.Empty() || len(d.Nodes) != 2 || d.Nodes[0].Node != builder || d.Nodes[1].Node != dgx {
		t.Fatalf("diff %+v", d)
	}
	if !strings.Contains(d.Text(), "builder-1") {
		t.Errorf("the text does not name builder-1:\n%s", d.Text())
	}
	if data, err := plan.DiffJSON(before, after); err != nil || !strings.Contains(string(data), `"builder-1"`) {
		t.Errorf("JSON %s (%v)", data, err)
	}
	if !plan.Diff(before, before).Empty() {
		t.Error("a plan differs from itself")
	}
}
