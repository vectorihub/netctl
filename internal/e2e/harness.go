package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl"
	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// keep makes every workspace survive the run, under artifacts/<test-id>/, so the gate folder
// can hold the evidence of tests that passed as well as of the ones that failed.
var keep = flag.Bool("keep", false, "keep every workspace under artifacts/")

// T0 is the fixed base time every test starts from. Times in the tests are offsets from it.
var T0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// ArtifactsDir is where kept workspaces go. It is relative to the directory of the test
// package, which is how the guide lays it out, and it is git-ignored.
const ArtifactsDir = "artifacts"

// Step is one entry of the log: what the test was doing and what it got.
type Step struct {
	// N is the step number, starting at 1.
	N int `json:"step"`
	// Name is what the test called the step.
	Name string `json:"name"`
	// Action says what was run, for example "plan out-1".
	Action string `json:"action"`
	// Error is the error the action returned, or empty.
	Error string `json:"error,omitempty"`
	// Code is the error code of the action, or empty.
	Code string `json:"code,omitempty"`
	// At is the test clock when the action ran.
	At time.Time `json:"at"`
	// Took is how long the action took.
	Took time.Duration `json:"took_ns"`
}

// W is one test's workspace: a private directory, a test clock, and a log of steps.
type W struct {
	// T is the test that owns the workspace.
	T testing.TB
	// ID is the test ID, for example "P1-E2E-020".
	ID string
	// Dir is the workspace directory ($W in the guide). Fixtures, outputs and signal files
	// all live under it, so a kept workspace can be examined on its own.
	Dir string
	// Now is the test clock. Advance moves it; every plan runs with it.
	Now time.Time

	// NoDisk keeps rendered plans in memory only. The chaos runs plan thousands of times and
	// have no use for the files.
	NoDisk bool

	step  int
	name  string
	log   []Step
	plans int
}

// New starts a test: a fresh workspace directory, the clock at T0, and a cleanup that keeps
// the workspace under artifacts/<id>/ when the test failed or -keep was given.
//
// Example:
//
//	w := e2e.New(t, "P1-E2E-020")
func New(t testing.TB, id string) *W {
	t.Helper()
	w := &W{T: t, ID: id, Dir: t.TempDir(), Now: T0}
	t.Cleanup(func() {
		if *keep || t.Failed() {
			w.saveArtifacts()
		}
	})
	return w
}

// Step starts a named step. Every action after it is logged under that name, and a failure
// message says which step it happened in.
//
// Example:
//
//	w.Step("prove the direct link healthy")
func (w *W) Step(name string) {
	w.step++
	w.name = name
}

// Advance moves the test clock forward by d.
//
// Example:
//
//	w.Advance(30 * time.Second)
func (w *W) Advance(d time.Duration) { w.Now = w.Now.Add(d) }

// At moves the test clock to T0 plus d, which is how the guide writes times.
//
// Example:
//
//	w.At(50 * time.Second) // T0+50s
func (w *W) At(d time.Duration) { w.Now = T0.Add(d) }

// Fatalf fails the test with the context rule H5 asks for: the test ID, the step number and
// name, and the last action with its error, so nobody has to rerun the test to see where
// it stopped.
func (w *W) Fatalf(format string, args ...any) {
	w.T.Helper()
	var last string
	if n := len(w.log); n > 0 {
		s := w.log[n-1]
		last = fmt.Sprintf("\n   last action: %s", s.Action)
		if s.Error != "" {
			last += fmt.Sprintf("\n   error: %s", s.Error)
		}
	}
	w.T.Fatalf("%s step %d (%s): %s%s", w.ID, w.step, w.name, fmt.Sprintf(format, args...), last)
}

// record adds an action to the log under the current step.
func (w *W) record(action string, err error, took time.Duration) {
	s := Step{N: w.step, Name: w.name, Action: action, At: w.Now, Took: took}
	if err != nil {
		s.Error = err.Error()
		var coded *types.Error
		if asError(err, &coded) {
			s.Code = string(coded.Code)
		}
	}
	w.log = append(w.log, s)
}

// Log returns a copy of the step log, for the harness self-tests and for reports.
func (w *W) Log() []Step { return append([]Step(nil), w.log...) }

// saveArtifacts copies the workspace and the step log to artifacts/<id>/. It is best effort:
// a failure to save is reported to the test log, not turned into a second failure.
func (w *W) saveArtifacts() {
	dest := filepath.Join(ArtifactsDir, w.ID)
	if err := os.RemoveAll(dest); err != nil {
		w.T.Logf("artifacts: %v", err)
		return
	}
	if err := copyTree(w.Dir, dest); err != nil {
		w.T.Logf("artifacts: %v", err)
		return
	}
	raw, err := json.MarshalIndent(w.log, "", "  ")
	if err != nil {
		w.T.Logf("artifacts: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dest, "steps.json"), append(raw, '\n'), 0o644); err != nil {
		w.T.Logf("artifacts: %v", err)
	}
}

// Out is the result of one plan: the directory it was written to (when it was written), the
// planner's own result, and the files Render produced.
type Out struct {
	// Name is the directory name inside the workspace, for example "out-1".
	Name string
	// Dir is the absolute directory. It exists only when the plan succeeded.
	Dir string
	// Result is the planner's result.
	Result netctl.Result
	// Err is the planner's error, or nil.
	Err error
	// Files holds what Render returned, by relative path. Nil when the plan failed.
	Files map[string][]byte
	// At is the test clock the plan ran with.
	At time.Time
	// Took is how long the plan took.
	Took time.Duration
}

// OK reports whether the plan succeeded and wrote its directory.
func (o *Out) OK() bool { return o != nil && o.Err == nil && o.Files != nil }

// Options is everything one plan call can vary. The zero value plans the workspace topology
// from an empty address plan with no health evidence.
type Options struct {
	// Name names the output directory. Empty means out-N, counting plans from 0.
	Name string
	// Topology is the network to plan. Nil means the workspace topology (W.Load).
	Topology *topology.Topology
	// From is the plan this one follows: its address plan, its failover state and itself as
	// the plan in force. It is what --state, --health and --previous are on a command line.
	From *Out
	// State replaces the address plan taken from From, for tests that corrupt it by hand.
	State *ipam.Snapshot
	// Signals are the health observations to fold in at this plan's clock.
	Signals []health.Signal
	// Overrides are static routes applied on top of the computed ones.
	Overrides []route.Override
	// SkipSweep leaves out the validator's single-failure sweep, which recomputes the routes
	// once per link and dominates the time of a large plan. The chaos runs use it.
	SkipSweep bool
	// NoPrevious plans without handing From over as the plan in force.
	NoPrevious bool
}

// Plan runs the planner through the exported API with the test clock and writes the files
// Render returns to a new directory. On an error nothing is written, which is the property
// the bad-input tests check. The plan is logged; the caller decides whether it must succeed.
//
// Example:
//
//	out := w.Plan(e2e.Options{From: baseline, Signals: failed})
func (w *W) Plan(o Options) *Out {
	w.T.Helper()
	topo := o.Topology
	if topo == nil {
		topo = w.Load(DefaultTopology)
	}
	name := o.Name
	if name == "" {
		name = fmt.Sprintf("out-%d", w.plans)
	}
	w.plans++
	in := netctl.Inputs{Topology: topo, Clock: &types.FixedClock{T: w.Now}, Signals: o.Signals, Overrides: o.Overrides}
	in.Validate.SkipSweep = o.SkipSweep
	if from := o.From; from.OK() {
		in.IPAM = from.Result.IPAM
		if !o.NoPrevious {
			prev := from.Result
			in.Previous = &prev
		}
		if from.Result.Failover != nil {
			state := from.Result.Failover.State
			in.Failover = &state
		}
	}
	if o.State != nil {
		in.IPAM = *o.State
	}
	start := time.Now()
	result, err := netctl.Plan(in)
	out := &Out{Name: name, Result: result, Err: err, At: w.Now, Took: time.Since(start)}
	if err == nil {
		out.Files, err = result.Render()
		if err == nil {
			out.Dir = filepath.Join(w.Dir, name)
			if !w.NoDisk {
				err = writeFiles(out.Dir, out.Files)
			}
		}
		if err != nil {
			out.Err, out.Files = err, nil
		}
	}
	w.record("plan "+name, out.Err, out.Took)
	return out
}

// MustPlan is Plan that fails the test when the plan fails.
//
// Example:
//
//	out := w.MustPlan(e2e.Options{})
func (w *W) MustPlan(o Options) *Out {
	w.T.Helper()
	out := w.Plan(o)
	if out.Err != nil {
		w.Fatalf("plan %s: %v", out.Name, out.Err)
	}
	return out
}

// Rel returns the path of a file inside an output directory, joined with the right
// separator, for tests that read a rendered file.
func (o *Out) Rel(path string) string { return filepath.Join(o.Dir, filepath.FromSlash(path)) }

// Nodes returns the node IDs that have a config in the output, sorted.
func (o *Out) Nodes() []string {
	var nodes []string
	for path := range o.Files {
		if rest, ok := strings.CutPrefix(path, "nodes/"); ok {
			nodes = append(nodes, strings.TrimSuffix(rest, ".json"))
		}
	}
	sortStrings(nodes)
	return nodes
}

// FailoverState returns the failover state the plan saved, or nil when it ran without one.
func (o *Out) FailoverState() *failover.State {
	if o == nil || o.Result.Failover == nil {
		return nil
	}
	state := o.Result.Failover.State
	return &state
}
