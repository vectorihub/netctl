package tests

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// loop returns an override that, with the direct link unproven, makes traffic between the
// spokes loop: the hub sends dgx-spark's site to builder-1, and builder-1 sends it to the hub.
func loop() []route.Override {
	return []route.Override{{Node: hub, Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: builder}}
}

// P1-VAL-053
//
// TestAFailingValidationProducesNoConfigs checks a plan the validator refuses has no
// configs, no plan ID, the full report, and an error with the code a command line maps to
// exit 2: the library side of "writes nothing".
func TestAFailingValidationProducesNoConfigs(t *testing.T) {
	in := fresh(t)
	in.Overrides = loop()
	r, err := plan.Plan(in)
	if !types.HasCode(err, types.ErrValidationFailed) {
		t.Fatalf("%v, want VALIDATION_FAILED", err)
	}
	if len(r.Configs) != 0 || r.PlanID != "" || r.Validation.OK {
		t.Errorf("configs %d, plan %q, ok %v", len(r.Configs), r.PlanID, r.Validation.OK)
	}
	if !r.Validation.Has("V-SIM-001") || !r.Validation.Has("V-ROUTE-005") {
		t.Errorf("the report lacks the loop or the override: %v", ruleIDs(r))
	}
	if len(r.Decisions) != 0 {
		t.Errorf("decisions with nothing in force: %+v", r.Decisions)
	}
	if len(r.Tables) == 0 || len(r.IPAM.Allocations) == 0 || r.Simulation.Passed {
		t.Error("the report needs the tables, the address plan and the failing simulation to explain itself")
	}
	if !strings.Contains(err.Error(), "V-SIM-001") {
		t.Errorf("the error does not list the errors: %v", err)
	}
}

// TestARejectedPlanKeepsThePlanInForce is the guard: a new plan that fails validation while
// the plan in force passed is rejected with a FailoverRejected decision and the plan in force
// is what comes back.
func TestARejectedPlanKeepsThePlanInForce(t *testing.T) {
	good := mustPlan(t, fresh(t))
	in := fresh(t)
	in.IPAM = good.IPAM
	in.Previous = &good
	in.Overrides = loop()
	r, err := plan.Plan(in)
	if !types.HasCode(err, types.ErrValidationFailed) {
		t.Fatalf("%v", err)
	}
	if len(r.Decisions) != 1 || r.Decisions[0].Kind != "FailoverRejected" || !strings.Contains(r.Decisions[0].Reason, good.PlanID) || len(r.Decisions[0].Findings) == 0 {
		t.Fatalf("decisions %+v", r.Decisions)
	}
	if r.PlanID != good.PlanID || len(r.Configs) != 3 {
		t.Errorf("plan %q with %d configs: the plan in force was not kept", r.PlanID, len(r.Configs))
	}
	for i := range r.Configs {
		if r.Configs[i] != good.Configs[i] {
			t.Errorf("config %d is not the one in force", i)
		}
	}
	if r.Validation.OK || !r.Validation.Has("V-SIM-001") {
		t.Error("the new report must say why")
	}
	if !strings.Contains(err.Error(), good.PlanID) {
		t.Errorf("the error does not name the plan kept: %v", err)
	}
}

// TestNothingToKeepWhenThePlanInForceFailedToo checks the guard only protects a plan that
// was good: if the one in force was refused as well there is nothing to keep.
func TestNothingToKeepWhenThePlanInForceFailedToo(t *testing.T) {
	in := fresh(t)
	in.Overrides = loop()
	bad, _ := plan.Plan(in)
	in2 := fresh(t)
	in2.Overrides = loop()
	in2.Previous = &bad
	r, err := plan.Plan(in2)
	if !types.HasCode(err, types.ErrValidationFailed) || len(r.Decisions) != 0 || len(r.Configs) != 0 {
		t.Errorf("%v, decisions %+v, configs %d", err, r.Decisions, len(r.Configs))
	}
}

// TestAGoodPlanReplacesThePlanInForce checks the guard stays out of the way when the new
// plan passes, including a failover: the configs change and nothing is rejected.
func TestAGoodPlanReplacesThePlanInForce(t *testing.T) {
	in := fresh(t)
	in.Health = evaluator(t, true)
	healthy := mustPlan(t, in)
	in2 := fresh(t)
	in2.IPAM = healthy.IPAM
	in2.Health = evaluator(t, false)
	in2.Previous = &healthy
	failedOver, err := plan.Plan(in2)
	if err != nil {
		t.Fatal(err)
	}
	if len(failedOver.Decisions) != 0 || failedOver.PlanID == healthy.PlanID || len(failedOver.Changes) != 0 {
		t.Errorf("plan %q, decisions %+v, changes %v", failedOver.PlanID, failedOver.Decisions, failedOver.Changes)
	}
}

// TestAConfigThatCannotBeGeneratedFailsThePlan checks a failure in generating the configs
// comes back as its own error with no configs: a static override that makes a prefix active
// through two peers is not something WireGuard can express.
func TestAConfigThatCannotBeGeneratedFailsThePlan(t *testing.T) {
	in := fresh(t)
	in.Health = evaluator(t, true)
	in.Overrides = []route.Override{{Node: builder, Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: hub}}
	r, err := plan.Plan(in)
	if err == nil {
		t.Fatalf("a prefix active through two peers was planned: %v", ruleIDs(r))
	}
	if len(r.Configs) != 0 {
		t.Errorf("configs from a failed generation")
	}
}
