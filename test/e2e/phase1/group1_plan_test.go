package phase1

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/ipam"
)

// init registers Group 1, planning lifecycle: a topology becomes correct addresses, routes
// and configs, and planning again changes nothing.
func init() {
	register("P1-E2E-001", e2e001FirstPlan)
	register("P1-E2E-002", e2e002Replan)
	register("P1-E2E-003", e2e003Labs)
	register("P1-E2E-004", e2e004Explain)
	register("P1-E2E-005", e2e005IPAMReport)
}

// referenceAllocations are the allocations the three-site network must get (7.1 of the guide).
var referenceAllocations = []string{
	"10.42.0.0/20 sites site:aws",
	"10.42.16.0/20 sites site:builder",
	"10.42.32.0/20 sites site:dgx",
	"10.42.240.1/32 overlay node:aws-hub",
	"10.42.240.2/32 overlay node:builder-1",
	"10.42.240.3/32 overlay node:dgx-spark",
	"10.42.252.0/30 transit link:builder-1~dgx-spark",
}

// allocations lists a plan's allocated blocks as "prefix pool owner", in address order.
func allocations(out *e2e.Out) []string {
	var got []string
	for _, a := range out.Result.IPAM.Allocations {
		if a.State == ipam.Allocated {
			got = append(got, fmt.Sprintf("%s %s %s", a.Prefix, a.Pool, a.Owner))
		}
	}
	return got
}

// e2e001FirstPlan is P1-E2E-001: the first plan of the three-site network. It proves AC3
// (correct tables), AC9 (no operating-system fields in configs) and the safe handling of a
// direct link nobody has proven: the fresh plan does not use it, the healthy baseline does.
func e2e001FirstPlan(w *e2e.W) {
	fresh, base := e2e.Baseline(w)

	w.Step("the fresh plan's files")
	w.ExpectFiles(fresh, "plan.json", "ipam.json", "routes.txt", "validation.json", "simulation.json",
		"nodes/aws-hub.json", "nodes/builder-1.json", "nodes/dgx-spark.json")
	if got := allocations(fresh); !slices.Equal(got, referenceAllocations) {
		w.Fatalf("allocations %v, want %v", got, referenceAllocations)
	}

	w.Step("the unproven direct link is not used")
	builder := w.JSON(fresh, "nodes/builder-1.json")
	dgxRoutes := 0
	for i := 0; i < builder.Len("routes"); i++ {
		if builder.Str(fmt.Sprintf("routes.%d.prefix", i)) == "10.42.32.0/20" {
			dgxRoutes++
		}
	}
	if via, metric := builder.Str("routes[prefix=10.42.32.0/20].via"), builder.Num("routes[prefix=10.42.32.0/20].metric"); via != "aws-hub" || metric != 40 || dgxRoutes != 1 {
		w.Fatalf("builder-1 routes the dgx site via %s metric %v with %d route(s), want aws-hub, 40, one", via, metric, dgxRoutes)
	}
	if got := builder.Strings("peers[node=dgx-spark].allowed_prefixes"); !slices.Equal(got, []string{"10.42.252.2/32"}) {
		w.Fatalf("builder-1's dgx-spark peer allows %s, want only its probe address", listed(got))
	}

	w.Step("simulation and validation")
	w.ExpectSimPass(fresh, 16)
	w.ExpectSimPass(base, 16)
	w.ExpectNoErrors(fresh)
	w.ExpectNoErrors(base)
	w.Golden("validation-fresh.json", fresh.Files["validation.json"])
	w.Golden("validation-healthy.json", base.Files["validation.json"])

	w.Step("the healthy baseline's configs")
	w.Golden("builder-1-healthy.json", e2e.Masked(base.Files["nodes/builder-1.json"]))
	w.Golden("aws-hub-healthy.json", e2e.Masked(base.Files["nodes/aws-hub.json"]))

	w.Step("the oracle walks the configs")
	w.ExpectOracleOK(fresh)
	w.ExpectOracleOK(base)
	if got := w.OraclePath(fresh, "builder-1", "dgx-spark").Hops; !slices.Equal(got, []string{"builder-1", "aws-hub", "dgx-spark"}) {
		w.Fatalf("fresh: builder-1 reaches dgx-spark via %v", got)
	}
	if got := w.OraclePath(base, "builder-1", "dgx-spark").Hops; !slices.Equal(got, []string{"builder-1", "dgx-spark"}) {
		w.Fatalf("baseline: builder-1 reaches dgx-spark via %v", got)
	}

	w.Step("no operating-system detail in a config")
	expectCleanKeys(w, fresh)
	expectCleanKeys(w, base)
}

// e2e002Replan is P1-E2E-002: planning the healthy baseline again, with nothing changed,
// changes nothing. It proves AC8 and that allocation is idempotent.
func e2e002Replan(w *e2e.W) {
	_, base := e2e.Baseline(w)
	w.Step("re-plan with no change")
	again := w.MustPlan(e2e.Options{From: base, Name: "out-2"})
	expectNoDiff(w, base, again)
	if base.SubtreeHash("nodes/") != again.SubtreeHash("nodes/") {
		w.Fatalf("node configs differ between the two plans")
	}
	if base.FileHash("ipam.json") != again.FileHash("ipam.json") {
		w.Fatalf("ipam.json differs between the two plans")
	}
	if base.Result.PlanID != again.Result.PlanID {
		w.Fatalf("plan ID %s, want the baseline's %s", again.Result.PlanID, base.Result.PlanID)
	}
	if len(again.Result.Changes) != 0 {
		w.Fatalf("a re-plan changed the address plan: %v", again.Result.Changes)
	}
}

// e2e003Labs is P1-E2E-003: each Packet Tracer lab plans cleanly, forwards on the oracle's
// check, and matches what the lab showed once the owner has transcribed it. The reviewed
// golden output of each lab is P1-GOLD-001 to 003.
func e2e003Labs(w *e2e.W) {
	for _, lab := range []string{"lab-a1", "lab-a2", "lab-a3"} {
		x := e2e.New(w.T, w.ID)
		topo := setupLab(x, lab)
		x.Step("plan " + lab)
		out := x.MustPlan(e2e.Options{Topology: topo})
		x.ExpectSimPass(out, -1)
		x.ExpectOracleOK(out)
		x.ExpectIPAMOK(out)
		obs, have := observation(x, lab)
		matchesLab(x, out, topo, obs, have, "")
	}
}

// e2e004Explain is P1-E2E-004: explaining a route gives the right match, backup and reason,
// and the explained route is the one in the node's config. (The command-line half, the same
// explanations as JSON, is the beaver-cli suite.)
func e2e004Explain(w *e2e.W) {
	_, base := e2e.Baseline(w)
	cases := []struct {
		from, to, prefix, via, backup string
	}{
		{"builder-1", "10.42.33.7", "10.42.32.0/20", "dgx-spark", "aws-hub"},
		{"dgx-spark", "10.42.16.9", "10.42.16.0/20", "builder-1", "aws-hub"},
		{"builder-1", "10.42.240.1", "10.42.240.1/32", "aws-hub", ""},
		{"dgx-spark", "10.42.240.1", "10.42.240.1/32", "aws-hub", ""},
	}
	for _, c := range cases {
		w.Step(fmt.Sprintf("explain %s to %s", c.from, c.to))
		exp, err := base.Result.Explain(nodeID(c.from), addr(c.to))
		if err != nil {
			w.Fatalf("explain: %v", err)
		}
		if exp.Match == nil || exp.Match.Prefix.String() != c.prefix || string(exp.Match.NextHop) != c.via || !exp.Match.Active {
			w.Fatalf("match %+v, want %s via %s active", exp.Match, c.prefix, c.via)
		}
		switch {
		case c.backup == "" && exp.Backup != nil:
			w.Fatalf("unexpected backup %+v", exp.Backup)
		case c.backup != "" && (exp.Backup == nil || string(exp.Backup.NextHop) != c.backup || exp.Backup.Metric != 40):
			w.Fatalf("backup %+v, want via %s metric 40", exp.Backup, c.backup)
		}
		if exp.Reason == "" {
			w.Fatalf("no reason given")
		}
		cfg := w.JSON(base, "nodes/"+c.from+".json")
		sel := fmt.Sprintf("routes[prefix=%s]", c.prefix)
		if cfg.Str(sel+".via") != c.via || int(cfg.Num(sel+".metric")) != exp.Match.Metric {
			w.Fatalf("the explained route is not the config's route to %s", c.prefix)
		}
		if text, err := base.Result.ExplainText(nodeID(c.from), addr(c.to)); err != nil || !strings.Contains(text, c.prefix) {
			w.Fatalf("ExplainText: %v\n%s", err, text)
		}
	}
	w.Golden("explain-builder-1-10.42.33.7.txt", mustText(w, base, "builder-1", "10.42.33.7"))
}

// e2e005IPAMReport is P1-E2E-005: the address accounting after planning adds up (AC2): the
// totals, the twenty-four free blocks in order, and the checker that does not use netctl.
func e2e005IPAMReport(w *e2e.W) {
	_, base := e2e.Baseline(w)
	rep := w.Accounting(base)
	if rep.Total != 65536 || rep.Allocated != 12295 || rep.Reserved != 0 || rep.Quarantined != 0 || rep.Free != 53241 {
		w.Fatalf("accounting %+v", rep)
	}
	if rep.Allocated+rep.Reserved+rep.Quarantined+rep.Free != rep.Total {
		w.Fatalf("the parts do not add up to the total")
	}
	want := strings.Fields(`10.42.48.0/20 10.42.64.0/18 10.42.128.0/18 10.42.192.0/19
10.42.224.0/20 10.42.240.0/32 10.42.240.4/30 10.42.240.8/29
10.42.240.16/28 10.42.240.32/27 10.42.240.64/26 10.42.240.128/25
10.42.241.0/24 10.42.242.0/23 10.42.244.0/22 10.42.248.0/22
10.42.252.4/30 10.42.252.8/29 10.42.252.16/28 10.42.252.32/27
10.42.252.64/26 10.42.252.128/25 10.42.253.0/24 10.42.254.0/23`)
	var got []string
	for _, b := range rep.FreeBlocks {
		got = append(got, b.String())
	}
	if !slices.Equal(got, want) {
		w.Fatalf("free blocks %v, want %v", got, want)
	}
	if len(rep.Problems) != 0 {
		w.Fatalf("problems: %v", rep.Problems)
	}
	w.ExpectIPAMOK(base)
}
