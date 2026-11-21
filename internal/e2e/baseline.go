package e2e

import (
	"time"

	"github.com/vectorihub/netctl/route"
)

// DirectLink is the ID of the direct link of the three-site network.
const DirectLink = "builder-1~dgx-spark"

// Baseline plans the three-site network, proves the direct link healthy and plans again. It
// returns the fresh plan (out-0) and the healthy baseline (out-1) and leaves the clock at
// T0+30s, with the direct link Healthy since T0+25s: five good probes from both ends.
//
// Example:
//
//	fresh, base := e2e.Baseline(w)
func Baseline(w *W) (fresh, base *Out) {
	w.T.Helper()
	w.Setup()
	return BaselineFrom(w, DefaultTopology)
}

// BaselineFrom is Baseline for a topology file already in the workspace.
func BaselineFrom(w *W, file string) (fresh, base *Out) {
	w.T.Helper()
	return BaselineWith(w, file, nil)
}

// BaselineWith is BaselineFrom with static routes applied to both plans, for the tests whose
// fixture carries an override.
//
// Example:
//
//	fresh, base := e2e.BaselineWith(w, "three-site.yaml", loop)
func BaselineWith(w *W, file string, overrides []route.Override) (fresh, base *Out) {
	w.T.Helper()
	topo := w.Load(file)
	w.Step("fresh plan")
	fresh = w.MustPlan(Options{Topology: topo, Name: "out-0", Overrides: overrides})
	w.Step("prove the direct link healthy")
	signals := new(Signals).Probes(DirectLink, true, 5, 5*time.Second, 5*time.Second)
	signals.Write(w, "s-ok.json")
	w.At(30 * time.Second)
	w.Step("healthy baseline plan")
	base = w.MustPlan(Options{Topology: topo, From: fresh, Signals: signals.List(), Name: "out-1", Overrides: overrides})
	return fresh, base
}
