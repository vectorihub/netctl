package tests

import (
	"flag"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/types"
	"github.com/vectorihub/netctl/validate"
)

// update rewrites the golden files from the current output instead of comparing with
// them: go test ./validate/tests -run Golden -update. Read the diff before committing it.
var update = flag.Bool("update", false, "rewrite the golden validation reports")

// golden compares got with the golden file, or rewrites the file under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "../../testdata/golden/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden file: %v (create it with -update)", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the golden file; run with -update and review the change\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

// rulesFired returns the IDs of the rules that have a finding, sorted, without repeats.
func rulesFired(res validate.Result) []string {
	var ids []string
	for _, f := range res.Findings {
		if !slices.Contains(ids, f.Rule) {
			ids = append(ids, f.Rule)
		}
	}
	slices.Sort(ids)
	return ids
}

// P1-VAL-050
//
// TestCleanThreeSitePlan checks the reference plans validate: no errors, and only the
// warnings the end-to-end guide expects. The healthy baseline has V-TOPO-004 (the direct
// link between two nodes behind NAT) and nothing else; the fresh plan, where the direct
// link is not yet proven, also has V-SIM-003 on the two hub links, since each spoke then
// has only one way out.
func TestCleanThreeSitePlan(t *testing.T) {
	healthy := validate.Validate(cleanPlan(t, true), validate.Options{})
	if !healthy.OK || healthy.Err() != nil || len(healthy.Errors()) != 0 {
		t.Fatalf("the healthy plan has errors: %v", healthy.Errors())
	}
	if got := rulesFired(healthy); !slices.Equal(got, []string{"V-TOPO-004"}) {
		t.Errorf("healthy plan findings %v, want just V-TOPO-004:\n%s", got, healthy.Text())
	}
	if f := healthy.Findings[0]; f.Level != validate.Warning || f.Subject != string(direct) {
		t.Errorf("%+v", f)
	}

	fresh := validate.Validate(cleanPlan(t, false), validate.Options{})
	if !fresh.OK {
		t.Fatalf("the fresh plan has errors: %v", fresh.Errors())
	}
	if got := rulesFired(fresh); !slices.Equal(got, []string{"V-SIM-003", "V-TOPO-004"}) {
		t.Errorf("fresh plan findings %v:\n%s", got, fresh.Text())
	}
	var sim003 []string
	for _, f := range fresh.Findings {
		if f.Rule == "V-SIM-003" {
			sim003 = append(sim003, f.Subject)
		}
	}
	if !slices.Equal(sim003, []string{string(hubB), string(hubD)}) {
		t.Errorf("V-SIM-003 subjects %v, want the two hub links", sim003)
	}
	if len(healthy.Infos()) != 0 || len(fresh.Infos()) != 0 {
		t.Error("the clean plans have information findings")
	}
}

// TestCleanPlanWithTheExpectedWarningsAccepted checks the reference warnings can be
// accepted in the topology file with a reason, and the plan then has no findings at all
// and says what was accepted.
func TestCleanPlanWithTheExpectedWarningsAccepted(t *testing.T) {
	p := cleanPlan(t, false)
	p.Topology.Validation.Suppress = append(p.Topology.Validation.Suppress,
		suppression("V-TOPO-004", string(direct), "both ends relay through the hub when hole punching fails"),
		suppression("V-SIM-003", "", "the direct link is not proven yet; the hub links are the only way out for now"))
	res := validate.Validate(p, validate.Options{})
	if len(res.Findings) != 0 || !res.OK || len(res.Suppressed) != 3 {
		t.Errorf("findings %v, suppressed %d", res.Findings, len(res.Suppressed))
	}
}

// TestGoldenValidationReports checks the reference results, as text and as JSON, are the reviewed files.
func TestGoldenValidationReports(t *testing.T) {
	for name, up := range map[string]bool{"healthy": true, "fresh": false} {
		res := validate.Validate(cleanPlan(t, up), validate.Options{})
		data, err := res.JSON()
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "validation-"+name+".json", data)
		golden(t, "validation-"+name+".txt", []byte(res.Text()))
	}
}

// P1-VAL-053
//
// TestAFailingValidationRefusesThePlan checks the contract the planner and the CLI rely on:
// any error makes Err return VALIDATION_FAILED, which the CLI maps to exit code 2 and
// the planner answers by producing no configs; warnings alone do not.
func TestAFailingValidationRefusesThePlan(t *testing.T) {
	p := cleanPlan(t, true)
	configOf(p, builder).Peers[0].AllowedPrefixes = nil // prefixes the routes still send there
	res := validate.Validate(p, validate.Options{})
	err := res.Err()
	if res.OK || !types.HasCode(err, types.ErrValidationFailed) {
		t.Fatalf("OK %v, err %v", res.OK, err)
	}
	for _, rule := range []string{"V-CFG-003", "V-CFG-001"} {
		if !strings.Contains(err.Error(), rule) {
			t.Errorf("the error does not list %s:\n%v", rule, err)
		}
	}
	// A plan with warnings only is not refused.
	if err := validate.Validate(cleanPlan(t, false), validate.Options{}).Err(); err != nil {
		t.Errorf("warnings refused the plan: %v", err)
	}
	// Suppressing the error does not help.
	p.Topology.Validation.Suppress = append(p.Topology.Validation.Suppress, suppression("V-CFG-003", "", "please"))
	if res := validate.Validate(p, validate.Options{}); res.OK || !res.Has("V-SUP-002") || !res.Has("V-CFG-003") {
		t.Errorf("%+v", res.Findings)
	}
}

// TestValidateDoesNotChangeThePlan checks the validator leaves the plan exactly as it found
// it: the same hashes, the same tables, the same snapshot.
func TestValidateDoesNotChangeThePlan(t *testing.T) {
	p := cleanPlan(t, false)
	var hashes []string
	for _, c := range p.Configs {
		hashes = append(hashes, c.Hash)
	}
	routes := len(p.Tables[builder].Routes)
	allocations := len(p.IPAM.Allocations)
	validate.Validate(p, validate.Options{})
	for i, c := range p.Configs {
		if c.Hash != hashes[i] || c.Verify() != nil {
			t.Errorf("the config of %s changed", c.Node)
		}
	}
	if len(p.Tables[builder].Routes) != routes || len(p.IPAM.Allocations) != allocations {
		t.Error("the tables or the snapshot changed")
	}
}
