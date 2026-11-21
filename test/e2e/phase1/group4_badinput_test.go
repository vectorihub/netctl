package phase1

import (
	"strings"

	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/types"
)

// init registers Group 4, bad input and safety: in every case the planner refuses, says why
// clearly, and changes nothing. P1-E2E-065 (an unwritable output directory) and 066 (hostile
// files) need a process and are the beaver-cli suite's; the decoder limits behind 066 are
// library tests.
func init() {
	register("P1-E2E-060", e2e060Overlap)
	register("P1-E2E-061", e2e061CorruptState)
	register("P1-E2E-062", e2e062Typo)
	register("P1-E2E-063", e2e063OneSided)
	register("P1-E2E-064", e2e064UnknownSchema)
	register("P1-E2E-067", e2e067Secret)
}

// inputs hashes the workspace's input files, so a test can show a refusal left them alone.
func inputs(w *e2e.W) e2e.Snapshot {
	w.T.Helper()
	snap := w.Snapshot(".")
	for name := range snap {
		if strings.HasPrefix(name, "out-") {
			delete(snap, name)
		}
	}
	return snap
}

// expectInputsUnchanged fails unless the input files are what they were: the planner never
// "fixes" its input.
func expectInputsUnchanged(w *e2e.W, before e2e.Snapshot) {
	w.T.Helper()
	after := inputs(w)
	for name, sum := range before {
		if after[name] != sum {
			w.Fatalf("input %s was changed", name)
		}
	}
}

// e2e060Overlap is P1-E2E-060: the discussion's conflict, AWS's /20 with three machines
// numbered inside it. All three overlaps are reported in one run, so nobody fixes them one at
// a time, and nothing is written.
func e2e060Overlap(w *e2e.W) {
	w.Setup()
	state := w.DiscussionOverlap()
	before := inputs(w)
	w.Step("plan the discussion's addresses")
	out := w.Plan(e2e.Options{State: &state})
	w.ExpectRefused(out, types.ErrOverlap)
	text := out.Err.Error()
	for _, machine := range []string{"10.42.0.2/32", "10.42.0.3/32", "10.42.0.4/32"} {
		if !strings.Contains(text, machine) {
			w.Fatalf("the refusal does not report %s: %v", machine, out.Err)
		}
	}
	for _, owner := range []string{"node:aws-hub", "node:builder-1", "node:dgx-spark"} {
		if !strings.Contains(text, owner) {
			w.Fatalf("the refusal does not name %s", owner)
		}
	}
	if strings.Count(text, "overlaps site:aws (10.42.0.0/20)") != 3 {
		w.Fatalf("want three overlaps with site aws in one error: %v", out.Err)
	}
	expectInputsUnchanged(w, before)
}

// e2e061CorruptState is P1-E2E-061: a saved address plan edited by hand so that two sites
// overlap is refused before any planning starts, naming both owners.
func e2e061CorruptState(w *e2e.W) {
	_, base := e2e.Baseline(w)
	bad := w.CorruptOverlap(base.Result.IPAM)
	before := inputs(w)
	w.Step("plan from the corrupted address plan")
	out := w.Plan(e2e.Options{State: &bad, Name: "out-2"})
	w.ExpectRefused(out, types.ErrOverlap)
	text := out.Err.Error()
	for _, want := range []string{"10.42.16.0/20", "site:builder", "10.42.16.0/21", "site:extra"} {
		if !strings.Contains(text, want) {
			w.Fatalf("the refusal does not mention %s: %v", want, out.Err)
		}
	}
	if len(out.Result.Configs) != 0 || len(out.Result.Tables) != 0 {
		w.Fatalf("planning went on after the state was refused: %d configs", len(out.Result.Configs))
	}
	expectInputsUnchanged(w, before)
}

// e2e062Typo is P1-E2E-062: an unknown field is an error with the file, the line and a
// suggestion, not something the planner guesses around.
func e2e062Typo(w *e2e.W) {
	w.Copy("e2e/address-plan.yaml", "e2e/three-site-typo.yaml")
	before := inputs(w)
	_, err := w.TryLoad("three-site-typo.yaml")
	if err == nil || !types.HasCode(err, types.ErrInvalidTopology) {
		w.Fatalf("got %v, want INVALID_TOPOLOGY", err)
	}
	text := err.Error()
	for _, want := range []string{"three-site-typo.yaml:17", `unknown field "rol"`, `did you mean "role"`} {
		if !strings.Contains(text, want) {
			w.Fatalf("the error lacks %q: %v", want, err)
		}
	}
	expectInputsUnchanged(w, before)
}

// e2e063OneSided is P1-E2E-063: a link listed by one end only is refused (AC6), the agreeing
// links are not reported, and once the other end lists it the result is exactly the links form.
func e2e063OneSided(w *e2e.W) {
	w.Copy("e2e/address-plan.yaml", "e2e/three-site.yaml", "e2e/adjacency.yaml", "e2e/adjacency-one-sided.yaml")
	before := inputs(w)
	_, err := w.TryLoad("adjacency-one-sided.yaml")
	if !types.HasCode(err, types.ErrInvalidTopology) {
		w.Fatalf("got %v, want INVALID_TOPOLOGY", err)
	}
	text := err.Error()
	if !strings.Contains(text, "builder-1~dgx-spark") || !strings.Contains(text, "builder-1 lists dgx-spark as a peer, dgx-spark does not list builder-1") {
		w.Fatalf("the error does not explain the one-sided link: %v", err)
	}
	if strings.Contains(text, "aws-hub~") {
		w.Fatalf("an agreeing link was reported: %v", err)
	}
	expectInputsUnchanged(w, before)

	w.Step("with both ends listing each other it is the same network as the links form")
	fromLinks := w.MustPlan(e2e.Options{Topology: w.Load("three-site.yaml"), Name: "out-0"})
	fromPeers := w.MustPlan(e2e.Options{Topology: w.Load("adjacency.yaml"), Name: "out-1"})
	if fromLinks.Result.PlanID != fromPeers.Result.PlanID || fromLinks.SubtreeHash("nodes/") != fromPeers.SubtreeHash("nodes/") {
		w.Fatalf("the adjacency form planned to %s, the links form to %s", fromPeers.Result.PlanID, fromLinks.Result.PlanID)
	}
}

// e2e064UnknownSchema is P1-E2E-064: a topology of a version nobody reads is refused, and the
// error says which versions are supported.
func e2e064UnknownSchema(w *e2e.W) {
	w.Copy("e2e/address-plan.yaml", "e2e/three-site-v9.yaml")
	before := inputs(w)
	_, err := w.TryLoad("three-site-v9.yaml")
	if !types.HasCode(err, types.ErrInvalidTopology) {
		w.Fatalf("got %v, want INVALID_TOPOLOGY", err)
	}
	if text := err.Error(); !strings.Contains(text, "beaver.topology/v9") || !strings.Contains(text, "beaver.topology/v1alpha1") {
		w.Fatalf("the error does not name the schema it got and the one it reads: %v", err)
	}
	expectInputsUnchanged(w, before)
}

// e2e067Secret is P1-E2E-067: a private key typed into a label is caught by the validator
// before it can travel into a config, and the refusal never repeats it.
func e2e067Secret(w *e2e.W) {
	const secret = "cGxhbnRlZC1zZWNyZXQtZm9yLXRlc3RzLW9ubHktMTIzNA=="
	w.Copy("e2e/address-plan.yaml", "e2e/three-site-secret.yaml")
	before := inputs(w)
	out := w.Plan(e2e.Options{Topology: w.Load("three-site-secret.yaml")})
	w.ExpectRefused(out, types.ErrValidationFailed)
	w.ExpectFinding(out, "V-CFG-005", "error", "topology:nodes[builder-1].labels.wg_private_key")
	w.ExpectNoSecrets(out, secret, secret[:12])
	for _, f := range out.Result.Validation.Findings {
		if strings.Contains(f.Message+f.Subject+f.Hint, secret[:8]) {
			w.Fatalf("a finding repeats the secret: %+v", f)
		}
	}
	expectInputsUnchanged(w, before)
}
