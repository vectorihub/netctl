package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// clean is a valid topology; each test breaks one thing in it.
const clean = `schema: beaver.topology/v1alpha1
sites: [{id: aws}, {id: builder}, {id: dgx}]
nodes:
  - {id: aws-hub, site: aws, role: hub, nat: none}
  - {id: builder-1, site: builder, role: spoke, nat: none, labels: {env: prod}}
  - {id: dgx-spark, site: dgx, role: spoke, nat: none}
links:
  - {kind: hub-spoke, a: aws-hub, b: builder-1}
  - {kind: hub-spoke, a: aws-hub, b: dgx-spark}
intents:
  - allow: {from: "*", to: aws-hub}
  - allow: {from: "site:builder", to: "label:env=prod"}
`

// load runs topology.Load on text and returns the report and error; the topology itself is not
// needed by these tests.
func load(t *testing.T, text string) (topology.Report, error) {
	t.Helper()
	_, report, err := topology.Load("t.yaml", []byte(text), 0)
	return report, err
}

// rules returns the rule of every finding, in order.
func rules(report topology.Report) []topology.Rule {
	var out []topology.Rule
	for _, finding := range report.Findings {
		out = append(out, finding.Rule)
	}
	return out
}

// wantRules fails the test unless the report holds exactly the rules in want, in that order.
func wantRules(t *testing.T, report topology.Report, want ...topology.Rule) {
	t.Helper()
	got := rules(report)
	if len(got) != len(want) {
		t.Fatalf("rules = %v, want %v\n%v", got, want, report.Findings)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rules = %v, want %v\n%v", got, want, report.Findings)
		}
	}
}

// replace returns the clean topology with its first occurrence of old changed to new, failing if
// old is not there.
func replace(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(clean, old) {
		t.Fatalf("clean topology has no %q", old)
	}
	return strings.Replace(clean, old, new, 1)
}

// The clean-plan test for every rule: nothing fires.
func TestCleanTopologyHasNoFindings(t *testing.T) {
	report, err := load(t, clean)
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("err %v, findings %v", err, report.Findings)
	}
}

// The reference file loads, and its one finding is the T8 warning (P1-TOPO-010).
func TestThreeSiteWarnsNeedsTraversal(t *testing.T) {
	data := fixture(t, "three-site.yaml")
	_, report, err := topology.Load("three-site.yaml", data, 0)
	if err != nil {
		t.Fatalf("a warning must not fail the load: %v", err)
	}
	wantRules(t, report, topology.RuleNeedsTraversal)
	finding := report.Warnings()[0]
	if finding.Level != topology.LevelWarning || finding.Subject != "link builder-1~dgx-spark" || !strings.Contains(finding.Msg, "needs-traversal") {
		t.Errorf("%v", finding)
	}
	if len(report.Errors()) != 0 || report.Err() != nil {
		t.Errorf("errors %v", report.Errors())
	}
}

// TestT1Schema checks T1: a wrong schema and a missing schema are each reported once.
func TestT1Schema(t *testing.T) {
	for name, text := range map[string]string{
		"wrong":   replace(t, "beaver.topology/v1alpha1", "beaver.topology/v2"),
		"missing": strings.Replace(clean, "schema: beaver.topology/v1alpha1\n", "", 1),
	} {
		report, err := load(t, text)
		wantRules(t, report, topology.RuleSchema)
		if !types.HasCode(err, types.ErrInvalidTopology) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestT2IDs checks T2: duplicate node IDs, duplicate site IDs and an invalid ID.
func TestT2IDs(t *testing.T) {
	// P1-TOPO-004: duplicate node ID.
	report, err := load(t, replace(t, "{id: dgx-spark, site: dgx", "{id: builder-1, site: dgx"))
	if !report.Has(topology.RuleIDs) || !types.HasCode(err, types.ErrInvalidTopology) || !strings.Contains(err.Error(), "T2") {
		t.Errorf("duplicate node: %v %v", report.Findings, err)
	}
	// Duplicate site and an invalid ID.
	report, _ = load(t, replace(t, "{id: dgx}", "{id: aws}"))
	wantRules(t, report, topology.RuleIDs, topology.RuleNodeSite) // dgx-spark's site is now gone too
	topo := &topology.Topology{Schema: topology.Schema, Sites: []topology.Site{{ID: "Bad ID"}}}
	wantRules(t, topo.Check(), topology.RuleIDs)
}

// TestT3NodeSite checks T3: a node in a site that does not exist.
func TestT3NodeSite(t *testing.T) {
	report, _ := load(t, replace(t, "site: dgx, role", "site: mars, role"))
	wantRules(t, report, topology.RuleNodeSite)
}

// P1-TOPO-005 and -006.
//
// TestT4LinkEnds checks T4: a link to an unknown node and a link from a node to itself.
func TestT4LinkEnds(t *testing.T) {
	report, err := load(t, replace(t, "b: dgx-spark}", "b: ghost}"))
	wantRules(t, report, topology.RuleLinkEnds, topology.RuleSpokeReachesHub)
	if err == nil || !strings.Contains(err.Error(), `unknown node "ghost"`) {
		t.Errorf("%v", err)
	}
	report, _ = load(t, replace(t, "{kind: hub-spoke, a: aws-hub, b: dgx-spark}", "{kind: direct, a: builder-1, b: builder-1}"))
	if !report.Has(topology.RuleLinkEnds) || report.Has(topology.RuleHubSpokeEnds) {
		t.Errorf("self-link: %v", report.Findings)
	}
	if !strings.Contains(report.Errors()[0].Msg, "itself") {
		t.Errorf("%v", report.Errors()[0])
	}
}

// P1-TOPO-007.
//
// TestT5DuplicateLink checks T5: two links of one kind between a pair are an error, one of each
// kind is fine.
func TestT5DuplicateLink(t *testing.T) {
	text := replace(t, "intents:", "  - {kind: direct, a: builder-1, b: dgx-spark}\n  - {kind: direct, a: dgx-spark, b: builder-1, cost: 5}\nintents:")
	report, _ := load(t, text)
	wantRules(t, report, topology.RuleDuplicateLink)
	// One of each kind between a pair is fine.
	report, _ = load(t, replace(t, "intents:", "  - {kind: direct, a: aws-hub, b: builder-1}\nintents:"))
	wantRules(t, report)
}

// P1-TOPO-008.
//
// TestT6HubSpokeEnds checks T6: a hub-spoke link needs exactly one hub end, so spoke-spoke and
// hub-hub both fail.
func TestT6HubSpokeEnds(t *testing.T) {
	text := replace(t, "intents:", "  - {kind: hub-spoke, a: builder-1, b: dgx-spark}\nintents:")
	report, _ := load(t, text)
	wantRules(t, report, topology.RuleHubSpokeEnds)
	if report.Errors()[0].Subject != "link builder-1~dgx-spark" {
		t.Errorf("%v", report.Errors()[0])
	}
	// Two hubs on one hub-spoke link is also malformed.
	text = replace(t, "role: spoke, nat: none, labels", "role: hub, nat: none, labels")
	report, _ = load(t, text)
	wantRules(t, report, topology.RuleHubSpokeEnds)
}

// TestT7HubExists checks T7: hub-spoke links with no hub anywhere are an error, and no links means
// no error.
func TestT7HubExists(t *testing.T) {
	report, _ := load(t, replace(t, "role: hub", "role: spoke"))
	// Both links now have no hub end (T6), and T7 names the missing hub; T9 stays quiet.
	wantRules(t, report, topology.RuleHubSpokeEnds, topology.RuleHubSpokeEnds, topology.RuleHubExists)
	// No hub-spoke links and no hubs is not a T7 matter.
	report, _ = load(t, `schema: beaver.topology/v1alpha1
sites: [{id: a}]
nodes: [{id: n1, site: a, role: spoke}]
`)
	if report.Has(topology.RuleHubExists) {
		t.Errorf("%v", report.Findings)
	}
}

// P1-TOPO-010: T8 is a warning and the topology loads.
func TestT8OnlyWhenBothBehindNAT(t *testing.T) {
	both := replace(t, "intents:", "  - {kind: direct, a: builder-1, b: dgx-spark}\nintents:")
	both = strings.ReplaceAll(both, "nat: none", "nat: behind")
	both = strings.Replace(both, "{id: aws-hub, site: aws, role: hub, nat: behind}", "{id: aws-hub, site: aws, role: hub, nat: none}", 1)
	report, err := load(t, both)
	if err != nil {
		t.Fatalf("warnings must not fail the load: %v", err)
	}
	wantRules(t, report, topology.RuleNeedsTraversal)

	one := replace(t, "intents:", "  - {kind: direct, a: builder-1, b: dgx-spark}\nintents:")
	report, _ = load(t, one)
	wantRules(t, report)
	// A hub-spoke link between two NATed nodes is not a direct link.
	hubBehind := strings.ReplaceAll(clean, "nat: none", "nat: behind")
	report, _ = load(t, hubBehind)
	wantRules(t, report)
}

// P1-TOPO-009: the isolated spoke is named.
func TestT9SpokeReachesHub(t *testing.T) {
	text := replace(t, "  - {kind: hub-spoke, a: aws-hub, b: dgx-spark}\n", "")
	report, err := load(t, text)
	wantRules(t, report, topology.RuleSpokeReachesHub)
	if report.Errors()[0].Subject != "node dgx-spark" || !strings.Contains(err.Error(), "dgx-spark") {
		t.Errorf("%v", report.Errors())
	}
	// A spoke that reaches the hub through another spoke is not isolated.
	text = replace(t, "  - {kind: hub-spoke, a: aws-hub, b: dgx-spark}\n", "  - {kind: direct, a: builder-1, b: dgx-spark}\n")
	report, _ = load(t, text)
	wantRules(t, report)
	// A pair of spokes joined only to each other is isolated.
	text = replace(t, "  - {kind: hub-spoke, a: aws-hub, b: builder-1}\n  - {kind: hub-spoke, a: aws-hub, b: dgx-spark}\n", "  - {kind: direct, a: builder-1, b: dgx-spark}\n")
	report, _ = load(t, text)
	wantRules(t, report, topology.RuleSpokeReachesHub, topology.RuleSpokeReachesHub)
}

// P1-TOPO-011.
//
// TestT10SelectorsMatch checks T10: a selector that matches nothing is an error and one that
// matches is not.
func TestT10SelectorsMatch(t *testing.T) {
	for _, bad := range []string{"site:mars", "node:ghost", "label:env=dev", "label:tier=prod", "ghost"} {
		text := replace(t, `{from: "*", to: aws-hub}`, `{from: "*", to: "`+bad+`"}`)
		report, _ := load(t, text)
		wantRules(t, report, topology.RuleSelectorMatches)
		if report.Errors()[0].Subject != "intents[0].to" {
			t.Errorf("%s: %v", bad, report.Errors()[0])
		}
	}
	// Selectors that match are fine, including the label one in clean.
	for _, good := range []string{"site:aws", "node:aws-hub", "label:env=prod", "dgx-spark", `"*"`} {
		report, _ := load(t, replace(t, `to: aws-hub}`, `to: `+good+`}`))
		wantRules(t, report)
	}
	// A Topology built in code with no selector set is also caught.
	built := &topology.Topology{Schema: topology.Schema, Intents: []topology.Intent{{Action: topology.ActionAllow}}}
	wantRules(t, built.Check(), topology.RuleSelectorMatches, topology.RuleSelectorMatches)
}

// T11 belongs to Decode; Load passes its errors through unchanged.
func TestT11UnknownFieldStillReportedByLoad(t *testing.T) {
	_, report, err := topology.Load("three-site-typo.yaml", fixture(t, "three-site-typo.yaml"), 0)
	if err == nil || !strings.Contains(err.Error(), `unknown field "rol"`) || len(report.Findings) != 0 {
		t.Errorf("%v %v", err, report.Findings)
	}
}

// P1-TOPO-012: five independent faults, five reports, in one error.
func TestFiveFaultsReportedTogether(t *testing.T) {
	topo, report, err := topology.Load("five-faults.yaml", fixture(t, "five-faults.yaml"), 0)
	if topo == nil {
		t.Fatal("the topology should come back beside the errors")
	}
	wantRules(t, report, topology.RuleIDs, topology.RuleNodeSite, topology.RuleLinkEnds, topology.RuleLinkEnds, topology.RuleSelectorMatches)
	list := problems(t, err)
	if len(list) != 5 {
		t.Fatalf("%d problems, want 5:\n%v", len(list), err)
	}
	for _, problem := range list {
		if problem.Code != types.ErrInvalidTopology {
			t.Errorf("%v", problem)
		}
	}
}

// TestCheckIsRepeatableAndDoesNotChangeTheTopology checks that Check gives the same findings
// twice, changes nothing, and tolerates a nil topology.
func TestCheckIsRepeatableAndDoesNotChangeTheTopology(t *testing.T) {
	topo, _, _ := topology.Load("five-faults.yaml", fixture(t, "five-faults.yaml"), 0)
	before := *topo
	first, second := topo.Check(), topo.Check()
	if len(first.Findings) != len(second.Findings) {
		t.Fatal("not repeatable")
	}
	for i := range first.Findings {
		if first.Findings[i] != second.Findings[i] {
			t.Errorf("finding %d differs", i)
		}
	}
	if len(before.Nodes) != len(topo.Nodes) || len(before.Links) != len(topo.Links) {
		t.Error("Check changed the topology")
	}
	var none *topology.Topology
	if got := none.Check(); !got.Has(topology.RuleSchema) {
		t.Errorf("%v", got.Findings)
	}
}
