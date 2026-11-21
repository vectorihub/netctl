package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
	"github.com/vectorihub/netctl/validate"
)

// P1-VAL-040
//
// TestRule_V_SIM_001 plants a black hole (builder-1 loses its routes to dgx-spark's
// overlay address) and a loop, and checks the finding names the flow and carries the
// packet's journey.
func TestRule_V_SIM_001(t *testing.T) {
	quiet(t, "V-SIM-001")
	p := cleanPlan(t, true)
	table := p.Tables[builder]
	var kept []route.Route
	for _, r := range table.Routes {
		if r.Prefix != pfx("10.42.240.3/32") {
			kept = append(kept, r)
		}
	}
	table.Routes = kept
	got := fires(t, p, "V-SIM-001")
	var found validate.Finding
	for _, f := range got {
		if f.Subject == "builder-1→dgx-spark (overlay)" {
			found = f
		}
	}
	if found.Rule == "" || !strings.Contains(found.Message, "BLACK_HOLE") {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(found.Detail, "builder-1") || !strings.Contains(found.Detail, "BLACK_HOLE") {
		t.Errorf("the trace is not attached: %q", found.Detail)
	}
	// A loop between the two spokes.
	p = cleanPlan(t, true)
	for i := range p.Tables[builder].Routes {
		r := &p.Tables[builder].Routes[i]
		if r.Prefix == pfx("10.42.240.3/32") && r.Active {
			r.NextHop, r.Via = hub, hubB
		}
	}
	for i := range p.Tables[hub].Routes {
		r := &p.Tables[hub].Routes[i]
		if r.Prefix == pfx("10.42.240.3/32") && r.Active {
			r.NextHop, r.Via = builder, hubB
		}
	}
	got = fires(t, p, "V-SIM-001")
	loops := 0
	for _, f := range got {
		if strings.Contains(f.Message, "LOOP") {
			loops++
		}
	}
	if loops == 0 {
		t.Errorf("no loop reported: %+v", got)
	}
	// A route over a link health has taken down is a failover that did not happen.
	if got := fires(t, withLinksDown(cleanPlan(t, true), direct), "V-SIM-001"); !strings.Contains(got[0].Message, "LINK_DOWN") {
		t.Errorf("%+v", got)
	}
}

// TestRule_V_SIM_001_cutOffNodesAreNotBlackHoles checks a flow between nodes the routing
// engine reported unreachable is judged cut off, not a black hole, and that a plan with no
// flows has nothing to fail.
func TestRule_V_SIM_001_cutOffNodesAreNotBlackHoles(t *testing.T) {
	p := withLinksDown(cleanPlan(t, false), hubB)
	computed, err := route.Compute(p.Graph, p.Input)
	if err != nil {
		t.Fatal(err)
	}
	p.Tables, p.Unreachable = computed.Tables, computed.Unreachable
	if got := findings(p, "V-SIM-001"); len(got) != 0 {
		t.Errorf("cut-off nodes failed the baseline: %+v", got)
	}
	p.Unreachable = nil // the same tables without the engine's word that they are cut off
	fires(t, p, "V-SIM-001")
	p.Flows = nil
	if got := findings(p, "V-SIM-001"); len(got) != 0 {
		t.Errorf("no flows, yet %+v", got)
	}
}

// TestRule_V_SIM_002 declares a scenario whose expectation is wrong, one that holds, and
// one that names a link that is not there.
func TestRule_V_SIM_002(t *testing.T) {
	quiet(t, "V-SIM-002")
	failDirect := []types.LinkID{direct}
	p := cleanPlan(t, true)
	p.Scenarios = []sim.Scenario{{Name: "direct-down-is-fine", LinksDown: failDirect, Expect: []sim.Expectation{
		{From: builder, To: dgx, Verdict: sim.VerdictOK},
	}}}
	if got := findings(p, "V-SIM-002"); len(got) != 0 {
		t.Errorf("a scenario that holds: %+v", got)
	}
	p.Scenarios = []sim.Scenario{{Name: "wrong-expectation", LinksDown: failDirect, Expect: []sim.Expectation{
		{From: builder, To: dgx, Verdict: sim.VerdictBlackHole},
	}}}
	got := fires(t, p, "V-SIM-002")
	if len(got) != 1 || got[0].Subject != "wrong-expectation" || !strings.Contains(got[0].Message, "expected BLACK_HOLE, got OK") {
		t.Errorf("%+v", got)
	}
	p.Scenarios = []sim.Scenario{{Name: "typo", LinksDown: []types.LinkID{"builder-1~dgx-sprak"}}}
	got = fires(t, p, "V-SIM-002")
	if !strings.Contains(got[0].Message, "cannot run") || !strings.Contains(got[0].Message, "dgx-sprak") {
		t.Errorf("%+v", got)
	}
	p.Scenarios = []sim.Scenario{{Expect: nil}} // no name
	fires(t, p, "V-SIM-002")
}

// P1-VAL-041
//
// TestRule_V_SIM_003 sweeps the plan where the direct link is not yet proven, so each
// spoke has one uplink: losing either hub link isolates a spoke. It is the reference's
// finding exactly, one for each of the two links and none for the hub, and the healthy
// plan, where the spokes have each other, has none.
func TestRule_V_SIM_003(t *testing.T) {
	if got := findings(cleanPlan(t, true), "V-SIM-003"); len(got) != 0 {
		t.Errorf("the healthy plan: %+v", got)
	}
	got := findings(cleanPlan(t, false), "V-SIM-003")
	if len(got) != 2 {
		t.Fatalf("findings %+v, want two", got)
	}
	want := map[string]string{string(hubB): "builder-1", string(hubD): "dgx-spark"}
	for _, f := range got {
		node, ok := want[f.Subject]
		if !ok || !strings.Contains(f.Message, "if the link fails, "+node+" is isolated") || f.Level != validate.Warning {
			t.Errorf("unexpected finding %+v", f)
		}
		delete(want, f.Subject)
	}
	if len(want) != 0 {
		t.Errorf("missing findings for %v", want)
	}
	// Losing the hub isolates both spokes too, but a link already says so, and the hub is
	// not blamed again.
	for _, f := range got {
		if f.Subject == string(hub) {
			t.Errorf("the hub was blamed for what its links already show: %+v", f)
		}
	}
}

// TestSweepOptionsAndBaseline checks SkipSweep leaves the sweep out and nothing else, and a
// node that is already cut off in the plan is not blamed on every link.
func TestSweepOptionsAndBaseline(t *testing.T) {
	p := cleanPlan(t, false)
	skipped := validate.Validate(p, validate.Options{SkipSweep: true})
	if skipped.Has("V-SIM-003") || !skipped.Has("V-TOPO-004") {
		t.Errorf("SkipSweep: %+v", skipped.Findings)
	}
	// builder-1 is already cut off: the direct link is unusable and its hub link is down.
	p = withLinksDown(cleanPlan(t, false), direct, hubB)
	computed, err := route.Compute(p.Graph, p.Input)
	if err != nil {
		t.Fatal(err)
	}
	p.Tables, p.Unreachable = computed.Tables, computed.Unreachable
	for _, f := range findings(p, "V-SIM-003") {
		if strings.Contains(f.Message, "builder-1 is isolated") {
			t.Errorf("an already isolated node was blamed on %s", f.Subject)
		}
	}
}

// TestSweepIsDeterministic checks the sweep gives the same findings in the same order
// every time.
func TestSweepIsDeterministic(t *testing.T) {
	first := findings(cleanPlan(t, false), "V-SIM-003")
	for i := 0; i < 5; i++ {
		again := findings(cleanPlan(t, false), "V-SIM-003")
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("run %d: %+v and %+v", i, first, again)
			}
		}
	}
}
