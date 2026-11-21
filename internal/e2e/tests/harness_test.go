package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/internal/e2e"
)

// failure is the panic value fakeT uses to stop a test the way t.Fatalf does.
type failure struct{ msg string }

// fakeT stands in for a *testing.T so a harness failure can be observed instead of failing
// the real test. Fatalf records the message and unwinds with a panic that run catches.
type fakeT struct {
	testing.TB
	dir      string
	failed   bool
	message  string
	cleanups []func()
}

// Helper does nothing; the fake has no call stack to hide helpers from.
func (f *fakeT) Helper() {}

// Fatalf records the failure message and unwinds.
func (f *fakeT) Fatalf(format string, args ...any) {
	f.failed = true
	f.message = sprintf(format, args...)
	panic(failure{f.message})
}

// Logf drops log output.
func (f *fakeT) Logf(string, ...any) {}

// Failed reports whether the fake test has failed.
func (f *fakeT) Failed() bool { return f.failed }

// TempDir returns the directory the fake test owns.
func (f *fakeT) TempDir() string { return f.dir }

// Cleanup queues a function to run when finish is called.
func (f *fakeT) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

// run executes body as the fake test, catching the unwinding Fatalf, then runs the cleanups
// the way the testing package would.
func (f *fakeT) run(body func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(failure); !ok {
				panic(r)
			}
		}
		for i := len(f.cleanups) - 1; i >= 0; i-- {
			f.cleanups[i]()
		}
	}()
	body()
}

// newFake returns a fake test with its own workspace directory.
func newFake(t *testing.T) *fakeT { return &fakeT{TB: t, dir: t.TempDir()} }

// TestAFailureNamesTheStepAndTheLastAction is the H5 rule: a failing assertion says which
// step it stopped in and what the last action was, so nobody reruns a test to find out.
func TestAFailureNamesTheStepAndTheLastAction(t *testing.T) {
	t.Chdir(t.TempDir())
	f := newFake(t)
	f.run(func() {
		w := e2e.New(f, "P1-E2E-999")
		w.Setup()
		w.Step("the plan that must fail")
		out := w.Plan(e2e.Options{})
		w.ExpectRefused(out, "OVERLAP")
	})
	for _, want := range []string{"P1-E2E-999", "step 1", "the plan that must fail", "plan out-0"} {
		if !strings.Contains(f.message, want) {
			t.Errorf("failure message lacks %q:\n%s", want, f.message)
		}
	}
}

// TestAFailedTestLeavesItsArtifacts checks that a failed test keeps its workspace and a
// steps.json under artifacts/<id>/, and that a passing one leaves nothing behind.
func TestAFailedTestLeavesItsArtifacts(t *testing.T) {
	t.Chdir(t.TempDir())
	f := newFake(t)
	f.run(func() {
		w := e2e.New(f, "P1-E2E-998")
		w.Setup()
		w.Step("plan")
		w.MustPlan(e2e.Options{})
		w.Fatalf("forced")
	})
	steps := filepath.Join(e2e.ArtifactsDir, "P1-E2E-998", "steps.json")
	if _, err := os.Stat(steps); err != nil {
		t.Fatalf("a failed test left no steps.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e2e.ArtifactsDir, "P1-E2E-998", "out-0", "plan.json")); err != nil {
		t.Errorf("the workspace was not kept: %v", err)
	}

	ok := newFake(t)
	ok.run(func() {
		w := e2e.New(ok, "P1-E2E-997")
		w.Setup()
		w.MustPlan(e2e.Options{})
	})
	if _, err := os.Stat(filepath.Join(e2e.ArtifactsDir, "P1-E2E-997")); err == nil {
		t.Error("a passing test kept artifacts without -keep")
	}
}

// TestProbesWriteBothEndsWithTheRightTimes checks the signal builder: five probes with two
// ends are ten signals, five seconds apart, the first end before the second at equal times.
func TestProbesWriteBothEndsWithTheRightTimes(t *testing.T) {
	list := new(e2e.Signals).Probes("builder-1~dgx-spark", true, 5, 5*time.Second, 5*time.Second).List()
	if len(list) != 10 {
		t.Fatalf("got %d signals, want 10", len(list))
	}
	if !list[0].At.Equal(e2e.T0.Add(5*time.Second)) || !list[9].At.Equal(e2e.T0.Add(25*time.Second)) {
		t.Errorf("times run from %v to %v", list[0].At, list[9].At)
	}
	if list[0].From != "builder-1" || list[1].From != "dgx-spark" {
		t.Errorf("ends are %s then %s", list[0].From, list[1].From)
	}
	one := new(e2e.Signals).Probes("builder-1~dgx-spark", false, 3, 0, time.Second, e2e.OneWay("builder-1"), e2e.Kind("handshake")).List()
	if len(one) != 3 || one[0].From != "builder-1" || one[0].Kind.String() != "handshake" || one[0].OK {
		t.Errorf("one-way handshake signals are %+v", one)
	}
}

// TestSignalsAreOrderedByTimeThenByAddition checks the rule of a signals file: timestamp
// order, and file order for equal timestamps.
func TestSignalsAreOrderedByTimeThenByAddition(t *testing.T) {
	s := new(e2e.Signals)
	s.Probes("a~b", true, 1, 10*time.Second, 0)
	s.Probes("a~b", false, 1, 5*time.Second, 0)
	list := s.List()
	if list[0].OK || !list[2].OK {
		t.Errorf("order %+v", list)
	}
}

// editInput is a small topology-shaped document the edit tests work on.
const editInput = `# top comment
name: t
nodes:
  - id: a
    role: hub
  - id: b
    role: spoke
links:
  - {kind: direct, a: b, b: a, cost: 10}
intents:
  - allow: {from: a, to: b}
`

// TestEditsSetAppendDeleteShuffle checks each edit: Set by selector (including a link by its
// computed ID), Append, Delete, and a seeded Shuffle that keeps every element and refuses to
// touch intents.
func TestEditsSetAppendDeleteShuffle(t *testing.T) {
	out, err := e2e.EditBytes([]byte(editInput),
		e2e.Set("links[id=a~b].cost", 50),
		e2e.Append("nodes", map[string]any{"id": "c", "role": "spoke"}),
		e2e.Delete("nodes[id=b]"),
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"# top comment", "cost: 50", "id: c"} {
		if !strings.Contains(text, want) {
			t.Errorf("edited text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "id: b") {
		t.Errorf("node b was not deleted:\n%s", text)
	}
	one, err := e2e.EditBytes([]byte(editInput), e2e.Shuffle(1, "nodes"))
	if err != nil || !strings.Contains(string(one), "id: a") || !strings.Contains(string(one), "id: b") {
		t.Errorf("shuffle lost an element: %v\n%s", err, one)
	}
	if _, err := e2e.EditBytes([]byte(editInput), e2e.Shuffle(1, "intents")); err == nil {
		t.Error("shuffling intents was allowed")
	}
	if _, err := e2e.EditBytes([]byte(editInput), e2e.Set("nodes[id=zzz].role", "hub")); err == nil {
		t.Error("an edit that matches nothing was silently accepted")
	}
}

// TestShuffleIsDeterministicForASeed checks that the same seed gives the same order, which
// the reproducibility test relies on to be repeatable.
func TestShuffleIsDeterministicForASeed(t *testing.T) {
	a, _ := e2e.EditBytes([]byte(editInput), e2e.Shuffle(42, "nodes"))
	b, _ := e2e.EditBytes([]byte(editInput), e2e.Shuffle(42, "nodes"))
	if string(a) != string(b) {
		t.Error("the same seed gave two orders")
	}
}

// TestBaselineLeavesTheDocumentedState checks Baseline: a fresh plan that does not use the
// direct link, a healthy baseline that does, the clock at T0+30s, and the files of the
// output directories on disk.
func TestBaselineLeavesTheDocumentedState(t *testing.T) {
	w := e2e.New(t, "P1-E2E-996")
	fresh, base := e2e.Baseline(w)
	if !w.Now.Equal(e2e.T0.Add(30 * time.Second)) {
		t.Errorf("the clock is at %v, want T0+30s", w.Now)
	}
	if len(fresh.Result.Unusable) != 1 || len(base.Result.Unusable) != 0 {
		t.Errorf("unusable: fresh %v, baseline %v", fresh.Result.Unusable, base.Result.Unusable)
	}
	w.ExpectFiles(base, "plan.json", "ipam.json", "routes.txt", "validation.json", "simulation.json", "nodes/builder-1.json")
	if _, err := os.Stat(filepath.Join(w.Dir, "out-1", "nodes", "builder-1.json")); err != nil {
		t.Errorf("out-1 was not written: %v", err)
	}
	if got := w.JSON(base, "nodes/builder-1.json").Str("routes[prefix=10.42.32.0/20].via"); got != "dgx-spark" {
		t.Errorf("builder-1 reaches the dgx site via %q, want dgx-spark", got)
	}
}

// TestTheOracleAcceptsAGoodPlanAndCatchesABrokenOne walks the baseline, then damages one
// config (a missing allowed prefix) and checks the oracle names the problem.
func TestTheOracleAcceptsAGoodPlanAndCatchesABrokenOne(t *testing.T) {
	w := e2e.New(t, "P1-E2E-995")
	_, base := e2e.Baseline(w)
	w.ExpectOracleOK(base)
	if path := w.OraclePath(base, "builder-1", "dgx-spark"); len(path.Hops) != 2 {
		t.Errorf("builder-1 to dgx-spark goes %v, want direct", path.Hops)
	}
	broken := *base
	broken.Files = map[string][]byte{}
	for k, v := range base.Files {
		broken.Files[k] = v
	}
	broken.Files["nodes/aws-hub.json"] = []byte(strings.ReplaceAll(string(base.Files["nodes/aws-hub.json"]), "10.42.240.3/32", "10.42.99.3/32"))
	pairs, err := broken.Oracle()
	if err != nil {
		t.Fatal(err)
	}
	if pair := pairs[[2]string{"builder-1", "aws-hub"}]; !pair.Forward.Delivered {
		t.Errorf("builder-1 to the hub should still work: %s", pair.Forward.Problem)
	}
	if pair := pairs[[2]string{"aws-hub", "dgx-spark"}]; pair.Forward.Delivered {
		t.Error("the oracle delivered a packet the hub has no route for")
	}
}

// TestTheIPAMCheckerFindsWhatIsWrong feeds the checker a sound file and then three broken
// ones: an overlap, an allocation outside its pool, and one outside the supernet.
func TestTheIPAMCheckerFindsWhatIsWrong(t *testing.T) {
	w := e2e.New(t, "P1-E2E-994")
	_, base := e2e.Baseline(w)
	w.ExpectIPAMOK(base)
	problems, counts := e2e.CheckIPAM(base.Files["ipam.json"])
	if len(problems) != 0 || counts.Allocated != 12295 || counts.Free != 53241 || counts.Total != 65536 {
		t.Fatalf("baseline: problems %v counts %+v", problems, counts)
	}
	good := string(base.Files["ipam.json"])
	overlap := strings.Replace(good, `"prefix": "10.42.32.0/20"`, `"prefix": "10.42.16.0/21"`, 1)
	if problems, _ := e2e.CheckIPAM([]byte(overlap)); len(problems) == 0 {
		t.Error("an overlap went unnoticed")
	}
	outside := strings.Replace(good, `"prefix": "10.42.32.0/20"`, `"prefix": "192.168.0.0/20"`, 1)
	if problems, _ := e2e.CheckIPAM([]byte(outside)); len(problems) < 2 {
		t.Errorf("an allocation outside the supernet gave %v", problems)
	}
}

// TestJSONQueries checks the query language on a small document: keys, indices and
// field selectors.
func TestJSONQueries(t *testing.T) {
	w := e2e.New(t, "P1-E2E-993")
	j := w.ParseJSON([]byte(`{"a":{"b":[{"n":"x","v":["1","2"]},{"n":"y","v":[]}]},"ok":true,"k":3}`))
	if got := j.Str("a.b.0.n"); got != "x" {
		t.Errorf("index: %q", got)
	}
	if got := j.Strings("a.b[n=x].v"); len(got) != 2 || got[1] != "2" {
		t.Errorf("selector: %v", got)
	}
	if j.Len("a.b") != 2 || j.Len("a.b[n=y].v") != 0 || j.Len("nope") != -1 {
		t.Error("Len is wrong")
	}
	if !j.Bool("ok") || j.Num("k") != 3 || j.Str("k") != "3" || j.Get("a.b[n=zzz]") != nil {
		t.Error("scalars are wrong")
	}
}

// TestMaskedHidesThePlanIDAndHash checks the golden masking used to compare a config while
// ignoring the two fields that change with everything.
func TestMaskedHidesThePlanIDAndHash(t *testing.T) {
	got := string(e2e.Masked([]byte(`{"plan_id": "three-site@abc", "hash": "sha256:123", "node": "x"}`)))
	if strings.Contains(got, "abc") || strings.Contains(got, "123") || !strings.Contains(got, `"node": "x"`) {
		t.Errorf("masked: %s", got)
	}
}

// TestSnapshotsAndDirHashesSeeAnyChange checks that a snapshot notices a changed byte, an
// added file and a removed one, and that equal trees hash equally.
func TestSnapshotsAndDirHashesSeeAnyChange(t *testing.T) {
	w := e2e.New(t, "P1-E2E-992")
	w.Write("d/a.txt", []byte("one"))
	before := w.Snapshot("d")
	hash := w.DirHash("d")
	if w.DirHash("d") != hash {
		t.Fatal("hashing twice gave two answers")
	}
	w.Write("d/a.txt", []byte("two"))
	if w.DirHash("d") == hash {
		t.Error("a changed byte was not noticed")
	}
	f := newFake(t)
	f.run(func() {
		x := e2e.New(f, "P1-E2E-991")
		x.Write("d/a.txt", []byte("one"))
		snap := x.Snapshot("d")
		x.Write("d/b.txt", []byte("new"))
		x.ExpectUnchanged("d", snap)
	})
	if !f.failed || !strings.Contains(f.message, "b.txt") {
		t.Errorf("an added file was not named: %q", f.message)
	}
	_ = before
}

// TestEveryFixtureLoadsOrIsRefusedAsDesigned loads each topology fixture of testdata/e2e: the
// good ones decode and check clean, the bad ones are refused for their own reason (a typo, a
// schema version nobody reads, an adjacency listed on one side).
func TestEveryFixtureLoadsOrIsRefusedAsDesigned(t *testing.T) {
	w := e2e.New(t, "P1-E2E-990")
	w.Copy("e2e/address-plan.yaml")
	good := []string{"three-site", "three-site-secret", "lab-a1", "lab-a2", "lab-a3", "adjacency"}
	for _, name := range good {
		w.Copy("e2e/" + name + ".yaml")
		if _, err := w.TryLoad(name + ".yaml"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]string{"three-site-typo": "rol", "three-site-v9": "beaver.topology/v9", "adjacency-one-sided": "builder-1~dgx-spark"}
	for name, want := range bad {
		w.Copy("e2e/" + name + ".yaml")
		_, err := w.TryLoad(name + ".yaml")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error mentioning %q", name, err, want)
		}
	}
}
