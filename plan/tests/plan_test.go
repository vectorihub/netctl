package tests

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/types"
)

// ruleIDs returns the rules of the result's findings, sorted, without repeats.
func ruleIDs(r plan.Result) []string {
	var ids []string
	for _, f := range r.Validation.Findings {
		if !slices.Contains(ids, f.Rule) {
			ids = append(ids, f.Rule)
		}
	}
	slices.Sort(ids)
	return ids
}

// TestAFreshPlanOfTheThreeSiteNetwork checks the first plan from an empty address plan: it
// allocates the reference addresses, does not use the direct link before it is proven,
// validates with exactly the reference warnings, and gives every node a config.
func TestAFreshPlanOfTheThreeSiteNetwork(t *testing.T) {
	r := mustPlan(t, fresh(t))
	if !r.Validation.OK || len(r.Configs) != 3 || !strings.HasPrefix(r.PlanID, "three-site@") {
		t.Fatalf("OK %v, %d configs, plan %q", r.Validation.OK, len(r.Configs), r.PlanID)
	}
	if got := ruleIDs(r); !slices.Equal(got, []string{"V-SIM-003", "V-TOPO-004"}) {
		t.Errorf("findings %v, want the reference V-SIM-003 and V-TOPO-004", got)
	}
	if !slices.Equal(r.Unusable, []types.LinkID{direct}) {
		t.Errorf("unusable %v: a direct link nothing is known about must not be used", r.Unusable)
	}
	want := map[string]string{
		"site:aws": "10.42.0.0/20", "site:builder": "10.42.16.0/20", "site:dgx": "10.42.32.0/20",
		"node:aws-hub": "10.42.240.1/32", "node:builder-1": "10.42.240.2/32", "node:dgx-spark": "10.42.240.3/32",
		"link:builder-1~dgx-spark": "10.42.252.0/30",
	}
	got := map[string]string{}
	for _, c := range r.Changes {
		if c.Kind == "allocated" {
			got[c.Subject] = c.Prefix.String()
		}
	}
	for owner, prefix := range want {
		if got[owner] != prefix {
			t.Errorf("%s got %q, want %s", owner, got[owner], prefix)
		}
	}
	if len(r.Changes) != len(want) {
		t.Errorf("%d changes, want %d", len(r.Changes), len(want))
	}
	if !r.Simulation.Passed || r.Simulation.Summary.Total != 16 {
		t.Errorf("simulation: passed %v, %d flows", r.Simulation.Passed, r.Simulation.Summary.Total)
	}
	// By the hub, since the direct link is not proven.
	for _, f := range r.Simulation.Flows {
		if f.Flow.Label == "builder-1→dgx-spark (overlay)" && f.Forward.Crossings() != 2 {
			t.Errorf("builder-1 to dgx-spark crosses %d links, want 2", f.Forward.Crossings())
		}
	}
}

// TestHealthDecidesWhichConfigsComeOut is the pipeline against the reviewed files: with
// the direct link proven the three configs are, byte for byte, the golden healthy ones;
// with it failed they are the failed-over ones. The config package pinned those files
// without the planner, so this joins the two.
func TestHealthDecidesWhichConfigsComeOut(t *testing.T) {
	for name, up := range map[string]bool{"healthy": true, "failed-over": false} {
		in := fresh(t)
		in.Health = evaluator(t, up)
		r := mustPlan(t, in)
		for _, c := range r.Configs {
			data, err := c.Clone().Encode()
			if err != nil {
				t.Fatal(err)
			}
			if want := golden(t, name+"-"+string(c.Node)+".json"); string(data) != want {
				t.Errorf("%s %s differs from the golden file:\n%s", name, c.Node, data)
			}
		}
	}
	in := fresh(t)
	in.Health = evaluator(t, true)
	healthy := mustPlan(t, in)
	if got := ruleIDs(healthy); !slices.Equal(got, []string{"V-TOPO-004"}) {
		t.Errorf("the healthy plan has findings %v, want just V-TOPO-004", got)
	}
	if len(healthy.Unusable) != 0 {
		t.Errorf("unusable %v", healthy.Unusable)
	}
}

// TestPlanningAgainIsIdempotent checks a second plan from the first one's address plan
// changes nothing: no changes, the same snapshot, the same configs, the same plan ID.
func TestPlanningAgainIsIdempotent(t *testing.T) {
	first := mustPlan(t, fresh(t))
	in := fresh(t)
	in.IPAM = first.IPAM
	second := mustPlan(t, in)
	if len(second.Changes) != 0 {
		t.Errorf("changes on a second plan: %v", second.Changes)
	}
	a, _ := ipam.Marshal(first.IPAM)
	b, _ := ipam.Marshal(second.IPAM)
	if string(a) != string(b) || first.PlanID != second.PlanID {
		t.Error("the second plan differs from the first")
	}
	for i := range first.Configs {
		if first.Configs[i].Hash != second.Configs[i].Hash {
			t.Errorf("%s changed", first.Configs[i].Node)
		}
	}
}

// TestAPlanCanStartFromTheTopologyAlone checks a zero snapshot is replaced by the topology's
// own inline address plan, and that a reference that is not read is a clear error.
func TestAPlanCanStartFromTheTopologyAlone(t *testing.T) {
	in := plan.Inputs{Topology: withAddressing(t), Clock: clock()}
	r := mustPlan(t, in)
	if !r.Validation.OK || len(r.Configs) != 3 {
		t.Fatalf("%+v", r.Validation.Errors())
	}
	in = plan.Inputs{Topology: fixture.ThreeSiteTopology(), Clock: clock()}
	in.Topology.Addressing.Ref = "address-plan.yaml"
	if _, err := plan.Plan(in); !types.HasCode(err, types.ErrNotFound) || !strings.Contains(err.Error(), "address-plan.yaml") {
		t.Errorf("%v, want NOT_FOUND naming the reference", err)
	}
}

// TestPlanRefusesNothingToPlanWith checks the inputs it cannot work without.
func TestPlanRefusesNothingToPlanWith(t *testing.T) {
	if _, err := plan.Plan(plan.Inputs{Clock: clock()}); !types.HasCode(err, types.ErrInvalidValue) {
		t.Errorf("no topology: %v", err)
	}
	in := fresh(t)
	in.Clock = nil
	if _, err := plan.Plan(in); !types.HasCode(err, types.ErrInvalidValue) {
		t.Errorf("no clock: %v", err)
	}
}

// TestADamagedSnapshotFailsBeforeAnythingElse checks step 1: a snapshot that cannot be loaded
// is refused with its code, no config exists, and the result hands back the snapshot it
// was given.
func TestADamagedSnapshotFailsBeforeAnythingElse(t *testing.T) {
	in := fresh(t)
	in.IPAM.Schema = "beaver.ipam/v0"
	r, err := plan.Plan(in)
	if !types.HasCode(err, types.ErrUnsupportedSchema) {
		t.Fatalf("%v, want UNSUPPORTED_SCHEMA", err)
	}
	if len(r.Configs) != 0 || r.IPAM.Schema != "beaver.ipam/v0" || len(r.Tables) != 0 {
		t.Errorf("a failed plan produced %+v", r)
	}
	in = fresh(t)
	in.IPAM.Allocations = append(in.IPAM.Allocations, ipam.Allocation{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Pool: "sites", Owner: ipam.Node("stray"), State: ipam.Allocated})
	if _, err := plan.Plan(in); !types.HasCode(err, types.ErrOutOfSupernet) {
		t.Errorf("an allocation outside the supernet: %v", err)
	}
}

// TestAllocationFailuresAreReportedTogether checks step 2 to 5 failures: with no transit
// pool and a sites pool too small, every problem is reported at once, no config exists, and the
// snapshot returned is the one given.
func TestAllocationFailuresAreReportedTogether(t *testing.T) {
	in := fresh(t)
	var pools []ipam.Pool
	for _, p := range in.IPAM.Pools {
		switch p.Name {
		case "transit":
			continue // gone
		case "sites":
			p.Prefix = netip.MustParsePrefix("10.42.0.0/19") // two /20s for three sites
			p.ChildBits = 20
		}
		pools = append(pools, p)
	}
	in.IPAM.Pools = pools
	r, err := plan.Plan(in)
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, code := range []types.Code{types.ErrPoolExhausted, types.ErrNotFound} {
		if !types.HasCode(err, code) {
			t.Errorf("%v lacks %s", err, code)
		}
	}
	if len(r.Configs) != 0 || len(r.IPAM.Allocations) != 0 || len(r.Changes) != 0 {
		t.Errorf("a failed plan changed state: %d configs, %d allocations, %d changes", len(r.Configs), len(r.IPAM.Allocations), len(r.Changes))
	}
}

// TestPlanDoesNotChangeItsInputs checks the topology, the snapshot and the health are left
// as they were found.
func TestPlanDoesNotChangeItsInputs(t *testing.T) {
	in := fresh(t)
	in.Health = evaluator(t, true)
	slices.Reverse(in.Topology.Nodes) // not in normalised order, to see whether Plan sorts the caller's
	slices.Reverse(in.Topology.Links)
	nodes := slices.Clone(in.Topology.Nodes)
	links := slices.Clone(in.Topology.Links)
	allocations := len(in.IPAM.Allocations)
	state := in.Health.Links[direct].State
	mustPlan(t, in)
	for i := range nodes {
		if in.Topology.Nodes[i].ID != nodes[i].ID {
			t.Errorf("the topology's nodes were reordered")
		}
	}
	for i := range links {
		if in.Topology.Links[i].A != links[i].A || in.Topology.Links[i].ID != links[i].ID {
			t.Errorf("the topology's links were changed")
		}
	}
	if len(in.IPAM.Allocations) != allocations || in.Health.Links[direct].State != state {
		t.Error("the snapshot or the health changed")
	}
}

// TestHealthIsCarriedOverByLinkID checks a link the evaluator knows keeps its state, a link
// it does not know starts Unknown, and a link in the evaluator that the topology no longer
// has is not an error.
func TestHealthIsCarriedOverByLinkID(t *testing.T) {
	in := fresh(t)
	in.Health = evaluator(t, false) // the direct link is Down
	r := mustPlan(t, in)
	if !slices.Equal(r.Unusable, []types.LinkID{direct}) {
		t.Errorf("unusable %v", r.Unusable)
	}
	// An evaluator that has never heard of the direct link: it starts Unknown, which for a
	// link that must be proven is not usable either.
	in = fresh(t)
	known := evaluator(t, true)
	delete(known.Links, direct)
	in.Health = known
	if r := mustPlan(t, in); !slices.Equal(r.Unusable, []types.LinkID{direct}) {
		t.Errorf("a new link must start Unknown: unusable %v", r.Unusable)
	}
	// A link the topology no longer has.
	in = fresh(t)
	extra := evaluator(t, true)
	ghost, _ := healthLink("ghost~other")
	extra.Links["ghost~other"] = ghost
	in.Health = extra
	if r := mustPlan(t, in); len(r.Unusable) != 0 {
		t.Errorf("unusable %v", r.Unusable)
	}
}
