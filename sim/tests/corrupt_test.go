package tests

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// corrupt applies CorruptTable for each prefix and returns the corrupted tables,
// or stops the test. It is the one way these tests plant a fault.
func corrupt(t *testing.T, tables route.Tables, node types.NodeID, nextHop types.NodeID, prefixes ...string) route.Tables {
	t.Helper()
	for _, prefix := range prefixes {
		var err error
		if tables, err = sim.CorruptTable(tables, node, prefixOf(prefix), nextHop); err != nil {
			t.Fatalf("CorruptTable(%s, %s): %v", node, prefix, err)
		}
	}
	return tables
}

// The prefixes the spokes of the three-site network announce: each one's site block
// and overlay address.
var (
	b1Prefixes  = []string{"10.42.16.0/20", "10.42.240.2/32"}
	dgxPrefixes = []string{"10.42.32.0/20", "10.42.240.3/32"}
)

// TestCorruptTableDoesNotChangeTheInput checks the original tables are untouched
// after a deletion, a retarget and an addition, and that the result is a
// different table for the corrupted node and the same one for the others.
func TestCorruptTableDoesNotChangeTheInput(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	before := map[types.NodeID][]route.Route{}
	for node, table := range p.tables {
		before[node] = slices.Clone(table.Routes)
	}
	out := corrupt(t, p.tables, "builder-1", "", "10.42.32.0/20")
	out = corrupt(t, out, "builder-1", "aws-hub", "10.42.240.3/32")
	out = corrupt(t, out, "builder-1", "aws-hub", "192.168.0.0/16")
	for node, routes := range before {
		if !slices.EqualFunc(routes, p.tables[node].Routes, func(a, b route.Route) bool { return route.Compare(a, b) == 0 }) {
			t.Errorf("%s's original table changed", node)
		}
	}
	if out["builder-1"] == p.tables["builder-1"] || out["aws-hub"] != p.tables["aws-hub"] {
		t.Error("only the corrupted node's table should be a new table")
	}
}

// TestCorruptTableDeletes checks an empty next hop removes every route for exactly
// that prefix, backups too, and leaves other prefixes alone.
func TestCorruptTableDeletes(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	out := corrupt(t, p.tables, "builder-1", "", "10.42.32.0/20")
	for _, r := range out["builder-1"].Routes {
		if r.Prefix == prefixOf("10.42.32.0/20") {
			t.Errorf("route %+v survived the deletion", r)
		}
	}
	if len(out["builder-1"].Routes) != len(p.tables["builder-1"].Routes)-2 {
		t.Errorf("%d routes left of %d; want the primary and its backup gone",
			len(out["builder-1"].Routes), len(p.tables["builder-1"].Routes))
	}
}

// TestCorruptTableRetargetsOnlyTheActiveRoute checks the active route for the
// prefix is pointed at the new next hop over the link between the two nodes, and
// the backup is left as it was.
func TestCorruptTableRetargetsOnlyTheActiveRoute(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	out := corrupt(t, p.tables, "builder-1", "aws-hub", "10.42.32.0/20")
	var active, backup *route.Route
	for i, r := range out["builder-1"].Routes {
		if r.Prefix == prefixOf("10.42.32.0/20") {
			if r.Active {
				active = &out["builder-1"].Routes[i]
			} else {
				backup = &out["builder-1"].Routes[i]
			}
		}
	}
	if active == nil || active.NextHop != "aws-hub" || active.Via != "aws-hub~builder-1" || !slices.Equal(active.Path, []types.NodeID{"builder-1", "aws-hub"}) {
		t.Errorf("active route %+v, want next hop aws-hub over aws-hub~builder-1", active)
	}
	if backup == nil || backup.NextHop != "aws-hub" || len(backup.Path) != 3 {
		t.Errorf("backup %+v should be unchanged: via aws-hub, a path of three nodes", backup)
	}
}

// TestCorruptTableAddsAStaticRoute checks a prefix with no active route gets one
// planted, flagged static so it is never taken for a computed route.
func TestCorruptTableAddsAStaticRoute(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	out := corrupt(t, p.tables, "builder-1", "aws-hub", "192.168.0.0/16")
	found, ok := out["builder-1"].Lookup(addrOf("192.168.1.1"))
	if !ok || found.Source != route.SourceStatic || found.NextHop != "aws-hub" || !found.Active {
		t.Errorf("planted route = %+v, found %v; want an active static route to aws-hub", found, ok)
	}
}

// TestCorruptTableRefusals checks an unknown node is NOT_FOUND and an invalid
// prefix is INVALID_PREFIX, and neither returns tables.
func TestCorruptTableRefusals(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	if out, err := sim.CorruptTable(p.tables, "nowhere", prefixOf("10.42.0.0/20"), ""); !types.HasCode(err, types.ErrNotFound) || out != nil {
		t.Errorf("unknown node: %v, %v", out, err)
	}
	if out, err := sim.CorruptTable(nil, "aws-hub", prefixOf("10.42.0.0/20"), ""); !types.HasCode(err, types.ErrNotFound) || out != nil {
		t.Errorf("nil tables: %v, %v", out, err)
	}
	if out, err := sim.CorruptTable(p.tables, "aws-hub", netip.Prefix{}, ""); !types.HasCode(err, types.ErrInvalidPrefix) || out != nil {
		t.Errorf("invalid prefix: %v, %v", out, err)
	}
}

// detected runs every flow of the three-site flow set over tables, with the
// network as graph, and returns the report and the verdicts keyed by label.
func detected(t *testing.T, graph *fixture.Graph, tables route.Tables) (sim.Report, map[string]sim.Verdict) {
	t.Helper()
	topo, plan := threeSite(t)
	input := fixture.ThreeSiteInput()
	net := sim.NewNet(graph, tables, input.Owned, input.Local)
	var results []sim.FlowResult
	for _, f := range mustFlows(t, topo, plan, sim.Options{}) {
		results = append(results, net.Run(f, false))
	}
	report := sim.NewReport(results)
	verdicts := map[string]sim.Verdict{}
	for _, r := range report.Flows {
		verdicts[r.Flow.Label] = r.Verdict
	}
	return report, verdicts
}

// P1-SIM-001
//
// TestHealthyThreeSiteIsCleanThroughScenarios checks the unspoiled network: the
// baseline scenario passes with every flow OK or expectedly unreachable, which is
// the control the planted faults below are measured against.
func TestHealthyThreeSiteIsCleanThroughScenarios(t *testing.T) {
	result, err := sim.RunScenario(threeSiteScenarioInput(t), sim.Scenario{Name: "baseline"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.Report.Summary.Failed != 0 || result.Report.Summary.Count(sim.VerdictOK) != 13 {
		t.Errorf("baseline: passed %v, %+v", result.Passed, result.Report.Summary.Verdicts)
	}
}

// P1-SIM-002, P1-SIM-003, P1-SIM-004, P1-SIM-010, P1-SIM-020, P1-SIM-021, P1-SIM-030, P1-SIM-031
//
// TestEveryPlantedFaultIsCaught plants each fault the plan lists with
// CorruptTable, runs the whole flow set, and checks the report fails and the flow
// that should notice gets exactly the right verdict. A fault the simulator missed
// would leave the report passing.
func TestEveryPlantedFaultIsCaught(t *testing.T) {
	cases := []struct {
		// name says which fault is planted.
		name string
		// computed is the network the tables are computed for, and graph the
		// network the packets run over. They differ only when the tables are
		// stale: computed before a link went down.
		computed, graph *fixture.Graph
		// plant spoils the tables.
		plant func(tables route.Tables) route.Tables
		// flow is the label of the flow that must notice, and want its verdict.
		flow string
		want sim.Verdict
	}{
		{"SIM-002 black hole for builder-1 on the hub", fixture.ThreeSite(false), fixture.ThreeSite(false),
			func(x route.Tables) route.Tables { return corrupt(t, x, "aws-hub", "", b1Prefixes...) },
			"builder-1→dgx-spark (overlay)", sim.VerdictNoReturn},
		{"SIM-003 the hub has no route for dgx-spark", fixture.ThreeSite(false), fixture.ThreeSite(false),
			func(x route.Tables) route.Tables { return corrupt(t, x, "aws-hub", "", dgxPrefixes...) },
			"builder-1→dgx-spark (overlay)", sim.VerdictBlackHole},
		{"SIM-004 default routes that bounce outside traffic", fixture.ThreeSite(false), fixture.ThreeSite(false),
			func(x route.Tables) route.Tables {
				x = corrupt(t, x, "builder-1", "aws-hub", "0.0.0.0/0")
				return corrupt(t, x, "aws-hub", "builder-1", "0.0.0.0/0")
			},
			"builder-1→192.168.1.1 (negative)", sim.VerdictLoop},
		{"SIM-010 two-node loop on dgx-spark's site", fixture.ThreeSite(false), fixture.ThreeSite(false),
			func(x route.Tables) route.Tables {
				x = corrupt(t, x, "aws-hub", "builder-1", dgxPrefixes...)
				return corrupt(t, x, "builder-1", "aws-hub", dgxPrefixes...)
			},
			"builder-1→dgx-spark (overlay)", sim.VerdictLoop},
		{"SIM-020 one-sided direct link", fixture.ThreeSite(true), fixture.ThreeSite(true),
			func(x route.Tables) route.Tables { return corrupt(t, x, "dgx-spark", "", b1Prefixes...) },
			"builder-1→dgx-spark (overlay)", sim.VerdictNoReturn},
		{"SIM-021 dgx-spark's site is not advertised", fixture.ThreeSite(true), fixture.ThreeSite(true),
			func(x route.Tables) route.Tables {
				x = corrupt(t, x, "builder-1", "", "10.42.32.0/20")
				return corrupt(t, x, "aws-hub", "", "10.42.32.0/20")
			},
			"builder→dgx (site)", sim.VerdictBlackHole},
		{"SIM-030 a route to a node that is not a neighbour", fixture.ThreeSite(false), fixture.ThreeSite(false),
			func(x route.Tables) route.Tables { return corrupt(t, x, "builder-1", "dgx-spark", dgxPrefixes...) },
			"builder-1→dgx-spark (overlay)", sim.VerdictWrongNeighbour},
		{"SIM-031 an active route over a link that is down", fixture.ThreeSite(true), fixture.ThreeSiteDirectDown(),
			func(x route.Tables) route.Tables { return x },
			"builder-1→dgx-spark (overlay)", sim.VerdictLinkDown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			healthy := computePlan(t, c.computed)
			report, verdicts := detected(t, c.graph, c.plant(healthy.tables))
			if report.Passed {
				t.Fatalf("the report passed with the fault planted: %+v", report.Summary.Verdicts)
			}
			if got := verdicts[c.flow]; got != c.want {
				t.Errorf("%s = %s, want %s", c.flow, got, c.want)
			}
		})
	}
}

// TestCorruptTableCanonicalisesThePrefix checks a prefix written with host bits set
// means the block it stands for: deleting 10.42.32.7/20 removes the routes for
// 10.42.32.0/20 and does not report success while doing nothing, retargeting it
// changes the existing route and adds no second one, and a stored route that is
// itself written with host bits is matched too.
func TestCorruptTableCanonicalisesThePrefix(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(true))
	gone := corrupt(t, p.tables, "builder-1", "", "10.42.32.7/20")
	if len(gone["builder-1"].Routes) != len(p.tables["builder-1"].Routes)-2 {
		t.Errorf("deleting 10.42.32.7/20 left %d routes of %d, want the primary and backup gone",
			len(gone["builder-1"].Routes), len(p.tables["builder-1"].Routes))
	}
	moved := corrupt(t, p.tables, "builder-1", "aws-hub", "10.42.32.7/20")
	if len(moved["builder-1"].Routes) != len(p.tables["builder-1"].Routes) {
		t.Errorf("retargeting added a route: %d routes, want %d", len(moved["builder-1"].Routes), len(p.tables["builder-1"].Routes))
	}
	if found, ok := moved["builder-1"].Lookup(addrOf("10.42.33.1")); !ok || found.NextHop != "aws-hub" || found.Prefix != prefixOf("10.42.32.0/20") {
		t.Errorf("lookup after retargeting = %+v, %v", found, ok)
	}
	odd := &route.Table{Node: "n", Routes: []route.Route{{
		Prefix: prefixOf("10.42.32.7/20"), NextHop: "a", Via: "a~n", Metric: 1, Source: route.SourceComputed, Active: true,
	}}}
	removed, err := sim.CorruptTable(route.Tables{"n": odd}, "n", prefixOf("10.42.32.0/20"), "")
	if err != nil || len(removed["n"].Routes) != 0 {
		t.Errorf("a stored route written with host bits was not matched: %v, %v", removed["n"].Routes, err)
	}
}
