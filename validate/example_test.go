package validate_test

// This file holds the runnable examples shown on the package documentation.

import (
	"fmt"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/validate"
)

// orphan is a rule of our own for the examples: it warns about the subject "dgx-spark".
var orphan = validate.Rule{
	ID: "X-DEMO-001", Level: validate.Warning, Title: "a demonstration rule",
	Check: func(*validate.Plan) []validate.Finding {
		return []validate.Finding{{Subject: "dgx-spark", Message: "has only one uplink", Hint: "add a second"}}
	},
}

// ExampleRun shows a rule making a finding, which gets the rule's ID and level.
func ExampleRun() {
	res := validate.Run(&validate.Plan{}, validate.Options{}, []validate.Rule{orphan})
	for _, f := range res.Findings {
		fmt.Println(f)
	}
	fmt.Println("ok:", res.OK)
	// Output:
	// warning X-DEMO-001 dgx-spark: has only one uplink (add a second)
	// ok: true
}

// ExampleOptions shows a warning accepted with a reason. It leaves the findings and
// stays on the record.
func ExampleOptions() {
	opts := validate.Options{Suppress: []topology.Suppression{{Rule: "X-DEMO-001", Subject: "dgx-spark", Reason: "second ISP due (BC-41)"}}}
	res := validate.Run(&validate.Plan{}, opts, []validate.Rule{orphan})
	fmt.Println(len(res.Findings), "findings;", len(res.Suppressed), "suppressed:", res.Suppressed[0].Reason)
	// Output:
	// 0 findings; 1 suppressed: second ISP due (BC-41)
}

// ExampleRun_suppressingAnError shows that an error cannot be suppressed: the attempt
// adds a finding of its own and the error stands.
func ExampleRun_suppressingAnError() {
	broken := validate.Rule{ID: "X-DEMO-002", Level: validate.Error, Title: "always broken",
		Check: func(*validate.Plan) []validate.Finding { return []validate.Finding{{Subject: "x", Message: "broken"}} }}
	opts := validate.Options{Suppress: []topology.Suppression{{Rule: "X-DEMO-002", Reason: "no"}}}
	res := validate.Run(&validate.Plan{}, opts, []validate.Rule{broken})
	for _, f := range res.Findings {
		fmt.Println(f.Rule, f.Level)
	}
	// Output:
	// V-SUP-002 error
	// X-DEMO-002 error
}

// ExampleValidate shows the catalogue run on an empty plan.
func ExampleValidate() {
	res := validate.Validate(&validate.Plan{}, validate.Options{})
	fmt.Println(res.OK, res.Err())
	// Output:
	// true <nil>
}

// ExampleRules shows the framework's rules in the catalogue.
func ExampleRules() {
	for _, r := range validate.Rules() {
		if r.ID[:5] == "V-SUP" {
			fmt.Println(r.ID, r.Level)
		}
	}
	// Output:
	// V-SUP-001 error
	// V-SUP-002 error
	// V-SUP-003 error
	// V-SUP-004 info
}

// ExampleResult_Err shows the error a refused plan returns, with every error in it.
func ExampleResult_Err() {
	res := validate.Result{Findings: []validate.Finding{{Rule: "V-CFG-004", Level: validate.Error, Subject: "builder-1", Message: "oops"}}}
	fmt.Println(res.Err())
	// Output:
	// VALIDATION_FAILED: validation: 1 error(s):
	// error V-CFG-004 builder-1: oops
}

// ExampleResult_JSON shows a clean result written as JSON.
func ExampleResult_JSON() {
	data, _ := validate.Result{OK: true}.JSON()
	fmt.Print(string(data))
	// Output:
	// {
	//   "findings": [],
	//   "suppressed": [],
	//   "ok": true
	// }
}

// ExampleResult_Errors shows the three views of a result's findings by level.
func ExampleResult_Errors() {
	res := validate.Result{Findings: []validate.Finding{
		{Rule: "A", Level: validate.Error}, {Rule: "B", Level: validate.Warning}, {Rule: "C", Level: validate.Info},
	}}
	fmt.Println(len(res.Errors()), len(res.Warnings()), len(res.Infos()), res.Has("B"), res.Has("Z"))
	// Output:
	// 1 1 1 true false
}

// ExampleResult_Warnings shows the warnings of a result.
func ExampleResult_Warnings() {
	res := validate.Result{Findings: []validate.Finding{{Rule: "V-TOPO-004", Level: validate.Warning}}}
	fmt.Println(res.Warnings()[0].Rule)
	// Output:
	// V-TOPO-004
}

// ExampleResult_Infos shows the information findings of a result.
func ExampleResult_Infos() {
	res := validate.Result{Findings: []validate.Finding{{Rule: "V-SUP-004", Level: validate.Info}}}
	fmt.Println(len(res.Infos()))
	// Output:
	// 1
}

// ExampleResult_Has shows asking whether a rule found anything.
func ExampleResult_Has() {
	res := validate.Result{Findings: []validate.Finding{{Rule: "V-TOPO-004", Level: validate.Warning}}}
	fmt.Println(res.Has("V-TOPO-004"), res.Has("V-CFG-004"))
	// Output:
	// true false
}

// ExampleFinding_String shows a finding on one line.
func ExampleFinding_String() {
	fmt.Println(validate.Finding{Rule: "V-CFG-004", Level: validate.Error, Subject: "builder-1", Message: "oops", Hint: "fix it"})
	// Output:
	// error V-CFG-004 builder-1: oops (fix it)
}
