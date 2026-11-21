package phase1

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/internal/ptimport"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// init registers the golden lab tests: the three Packet Tracer labs, and the discussion
// example that IPAM must refuse.
func init() {
	register("P1-GOLD-001", goldA1)
	register("P1-GOLD-002", goldA2)
	register("P1-GOLD-003", goldA3)
	register("P1-GOLD-004", goldDiscussionOverlap)
}

// observation loads the transcribed results of a lab from testdata/golden/<lab>/observed. It
// reports false, and logs why, when the owner has not transcribed them yet: the comparison
// with the lab then cannot run, and only the reviewed golden output is checked.
func observation(w *e2e.W, lab string) (ptimport.Observation, bool) {
	w.T.Helper()
	root, err := e2e.ModuleRoot()
	if err != nil {
		w.Fatalf("%v", err)
	}
	obs, err := ptimport.Load(filepath.Join(root, "testdata", "golden", lab, "observed"))
	if errors.Is(err, os.ErrNotExist) {
		w.T.Logf("%s: no Packet Tracer observations transcribed yet (owner step); comparing with the reviewed golden output only", lab)
		return obs, false
	}
	if err != nil {
		w.Fatalf("%s observations: %v", lab, err)
	}
	return obs, true
}

// matchesLab compares a plan with the lab's observation of one state: the routes and the
// traceroute paths. It does nothing when there is no observation.
func matchesLab(w *e2e.W, out *e2e.Out, topo *topology.Topology, obs ptimport.Observation, have bool, state string) {
	w.T.Helper()
	if !have {
		return
	}
	plan := out.ForPacketTracer(topo, obs.Mapping)
	for _, problem := range ptimport.CompareRoutes(obs, state, plan) {
		w.T.Errorf("%s: %s", out.Name, problem)
	}
	for _, problem := range ptimport.ComparePaths(obs, state, plan) {
		w.T.Errorf("%s: %s", out.Name, problem)
	}
}

// setupLab copies the address plan and one lab topology into the workspace and loads it.
func setupLab(w *e2e.W, lab string) *topology.Topology {
	w.T.Helper()
	w.Copy("e2e/address-plan.yaml", "e2e/"+lab+".yaml")
	return w.Load(lab + ".yaml")
}

// goldA1 is P1-GOLD-001: lab A1, IP addressing. The plan hands out the same blocks as the lab
// (three sites, three machines, no transit space as there is no direct link), the accounting
// adds up, and the output equals the reviewed golden directory.
func goldA1(w *e2e.W) {
	topo := setupLab(w, "lab-a1")
	w.Step("plan lab A1")
	out := w.MustPlan(e2e.Options{Topology: topo})
	w.GoldenDir(out, "lab-a1")
	w.Step("allocations and accounting")
	rep := w.Accounting(out)
	if rep.Allocated != 3*4096+3 || rep.Free != 65536-(3*4096+3) || len(rep.Problems) != 0 {
		w.Fatalf("A1 accounting: allocated %d, free %d, problems %v", rep.Allocated, rep.Free, rep.Problems)
	}
	w.ExpectIPAMOK(out)
	w.ExpectOracleOK(out)
	obs, have := observation(w, "lab-a1")
	matchesLab(w, out, topo, obs, have, "")
}

// goldA2 is P1-GOLD-002: lab A2, hub-and-spoke routing. The tables and the traceroute paths
// equal the reviewed golden output, and traffic between the spokes goes through the hub.
func goldA2(w *e2e.W) {
	topo := setupLab(w, "lab-a2")
	w.Step("plan lab A2")
	out := w.MustPlan(e2e.Options{Topology: topo})
	w.GoldenDir(out, "lab-a2")
	w.Step("spoke to spoke goes through the hub")
	w.ExpectOracleOK(out)
	if path := w.OraclePath(out, "builder-1", "dgx-spark"); len(path.Hops) != 3 || path.Hops[1] != "aws-hub" {
		w.Fatalf("builder-1 reaches dgx-spark via %v, want the hub", path.Hops)
	}
	obs, have := observation(w, "lab-a2")
	matchesLab(w, out, topo, obs, have, "")
}

// goldA3 is P1-GOLD-003: lab A3, direct peering and failover, in three states: the baseline
// with the direct link healthy, the link shut, and the link restored. Each state equals its
// reviewed golden directory and the lab's observation; restoring brings back the baseline.
func goldA3(w *e2e.W) {
	topo := setupLab(w, "lab-a3")
	_, base := e2e.BaselineFrom(w, "lab-a3.yaml")
	w.GoldenDir(base, "baseline")
	obs, have := observation(w, "lab-a3")
	matchesLab(w, base, topo, obs, have, "baseline")
	if path := w.OraclePath(base, "builder-1", "dgx-spark"); len(path.Hops) != 2 {
		w.Fatalf("baseline: builder-1 reaches dgx-spark via %v, want the direct link", path.Hops)
	}

	w.Step("shut the direct link")
	w.At(50 * time.Second)
	fail := new(e2e.Signals).Probes(e2e.DirectLink, false, 3, 35*time.Second, 5*time.Second)
	shut := w.MustPlan(e2e.Options{Topology: topo, From: base, Signals: fail.List(), Name: "out-2"})
	w.GoldenDir(shut, "shutdown")
	matchesLab(w, shut, topo, obs, have, "shutdown")
	if path := w.OraclePath(shut, "builder-1", "dgx-spark"); len(path.Hops) != 3 || path.Hops[1] != "aws-hub" {
		w.Fatalf("shutdown: builder-1 reaches dgx-spark via %v, want the hub", path.Hops)
	}

	w.Step("no shutdown: the link comes back")
	w.At(2*time.Minute + 15*time.Second)
	back := new(e2e.Signals).
		Probes(e2e.DirectLink, false, 12, 50*time.Second, 5*time.Second).
		Probes(e2e.DirectLink, true, 5, 110*time.Second, 5*time.Second)
	restored := w.MustPlan(e2e.Options{Topology: topo, From: shut, Signals: back.List(), Name: "out-3"})
	w.GoldenDir(restored, "restored")
	matchesLab(w, restored, topo, obs, have, "restored")
	if base.Result.PlanID != restored.Result.PlanID {
		w.Fatalf("restored plan %s is not the baseline %s", restored.Result.PlanID, base.Result.PlanID)
	}
}

// goldDiscussionOverlap is P1-GOLD-004: the discussion example, a site block 10.42.0.0/20 for
// AWS and machines at 10.42.0.2, .3 and .4. IPAM refuses it, naming all three machines in one
// error, and writes nothing.
func goldDiscussionOverlap(w *e2e.W) {
	w.Setup()
	state := w.DiscussionOverlap()
	w.Step("plan from the discussion's addresses")
	out := w.Plan(e2e.Options{State: &state})
	w.ExpectRefused(out, types.ErrOverlap)
	for _, machine := range []string{"10.42.0.2/32", "10.42.0.3/32", "10.42.0.4/32"} {
		if !contains(out.Err.Error(), machine) {
			w.Fatalf("the refusal does not name %s: %v", machine, out.Err)
		}
	}
}
