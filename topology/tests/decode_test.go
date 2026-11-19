package tests

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// fixture reads a file from testdata/topologies, failing the test if it cannot.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "topologies", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fixtureBytes is fixture for callers that have no *testing.T, such as fuzz seeds.
func fixtureBytes(name string) []byte {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "topologies", name))
	if err != nil {
		panic(err)
	}
	return data
}

// decode reads a fixture and decodes it, failing the test on any error.
func decode(t *testing.T, name string) *topology.Topology {
	t.Helper()
	got, err := topology.Decode(name, fixture(t, name), 0)
	if err != nil {
		t.Fatalf("Decode(%s): %v", name, err)
	}
	return got
}

// problems returns the list of problems in err.
func problems(t *testing.T, err error) types.List {
	t.Helper()
	var list types.List
	if !errors.As(err, &list) {
		t.Fatalf("error is %T, want types.List: %v", err, err)
	}
	return list
}

// mustFail decodes text and expects every problem to be INVALID_TOPOLOGY; it
// returns them.
func mustFail(t *testing.T, text string) types.List {
	t.Helper()
	got, err := topology.Decode("t.yaml", []byte(text), 0)
	if err == nil {
		t.Fatalf("accepted:\n%s\n=> %+v", text, got)
	}
	list := problems(t, err)
	for _, problem := range list {
		if problem.Code != types.ErrInvalidTopology {
			t.Errorf("problem %v has code %s, want INVALID_TOPOLOGY", problem, problem.Code)
		}
	}
	return list
}

// wantProblem fails unless some problem mentions every fragment, at line.
func wantProblem(t *testing.T, list types.List, line int, fragments ...string) {
	t.Helper()
	subject := "t.yaml"
	if line > 0 {
		subject = "t.yaml:" + itoa(line)
	}
	for _, problem := range list {
		if problem.Subject != subject {
			continue
		}
		all := true
		for _, fragment := range fragments {
			all = all && strings.Contains(problem.Msg, fragment)
		}
		if all {
			return
		}
	}
	t.Errorf("no problem at %s mentioning %q in:\n%v", subject, fragments, list)
}

// itoa writes a non-negative number as decimal text, without importing strconv into the test file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

// P1-TOPO-001: three sites, three nodes, three links, four intents, defaults applied.
func TestDecodeThreeSite(t *testing.T) {
	got := decode(t, "three-site.yaml")
	if got.Schema != topology.Schema || got.Name != "three-site" {
		t.Errorf("schema %q name %q", got.Schema, got.Name)
	}
	if len(got.Sites) != 3 || len(got.Nodes) != 3 || len(got.Links) != 3 || len(got.Intents) != 4 {
		t.Fatalf("%d sites, %d nodes, %d links, %d intents; want 3, 3, 3, 4", len(got.Sites), len(got.Nodes), len(got.Links), len(got.Intents))
	}
	if got.Addressing.Ref != "address-plan.yaml" || got.Addressing.Supernet.IsValid() {
		t.Errorf("addressing = %+v", got.Addressing)
	}
	if got.Sites[0] != (topology.Site{ID: "aws", Provider: "aws", Region: "eu-west-1"}) || got.Sites[1].Region != "" {
		t.Errorf("sites = %+v", got.Sites)
	}

	hub := got.Nodes[0]
	if hub.ID != "aws-hub" || hub.Site != "aws" || hub.Role != types.RoleHub || hub.NAT != topology.NATNone ||
		!reflect.DeepEqual(hub.Endpoints, []netip.AddrPort{netip.MustParseAddrPort("203.0.113.10:51820")}) || hub.Advertises != nil {
		t.Errorf("hub = %+v", hub)
	}
	spoke := got.Nodes[1]
	if spoke.ID != "builder-1" || spoke.Role != types.RoleSpoke || spoke.NAT != topology.NATBehind ||
		!reflect.DeepEqual(spoke.Advertises, []topology.Advertisement{{Site: true}}) {
		t.Errorf("spoke = %+v", spoke)
	}

	// Decode normalises: the first link got the hub-spoke defaults, the direct
	// link kept the cost and health it gave. Both got their derived IDs.
	first, direct := got.Links[0], got.Links[2]
	if first != (topology.Link{ID: "aws-hub~builder-1", Kind: types.LinkHubSpoke, A: "aws-hub", B: "builder-1", Cost: 20, Health: topology.HealthAssumed}) {
		t.Errorf("hub-spoke link = %+v", first)
	}
	if direct != (topology.Link{ID: "builder-1~dgx-spark", Kind: types.LinkDirect, A: "builder-1", B: "dgx-spark", Cost: 10, Health: topology.HealthRequired}) {
		t.Errorf("direct link = %+v", direct)
	}

	wantIntents := []struct {
		action topology.Action
		from   string
		to     string
		ports  []uint16
	}{
		{topology.ActionAllow, "node:builder-1", "node:dgx-spark", nil},
		{topology.ActionAllow, "node:dgx-spark", "node:builder-1", nil},
		{topology.ActionAllow, "*", "node:aws-hub", nil},
		{topology.ActionDeny, "site:builder", "site:aws", []uint16{5432}},
	}
	for i, want := range wantIntents {
		intent := got.Intents[i]
		if intent.Action != want.action || intent.From.String() != want.from || intent.To.String() != want.to ||
			!reflect.DeepEqual(intent.Ports, want.ports) {
			t.Errorf("intent %d = %+v (%s -> %s), want %+v", i, intent, intent.From, intent.To, want)
		}
	}
}

// TestDecodeIsRepeatable checks that decoding the same bytes twice gives equal results.
func TestDecodeIsRepeatable(t *testing.T) {
	first, second := decode(t, "three-site.yaml"), decode(t, "three-site.yaml")
	if !reflect.DeepEqual(first, second) {
		t.Error("decoding the same bytes twice gave different results")
	}
}

// P1-TOPO-003 and P1-E2E-062: the typo names its file, line and field, and
// suggests the field that was meant.
func TestDecodeTypoNamesLineFieldAndHint(t *testing.T) {
	_, err := topology.Decode("three-site-typo.yaml", fixture(t, "three-site-typo.yaml"), 0)
	if !types.HasCode(err, types.ErrInvalidTopology) {
		t.Fatalf("want INVALID_TOPOLOGY, got %v", err)
	}
	list := problems(t, err)
	// The typo also leaves the hub without a role, a second consequence of one slip.
	var typo *types.Error
	for _, problem := range list {
		if strings.Contains(problem.Msg, `"rol"`) {
			typo = problem
		}
	}
	if typo == nil {
		t.Fatalf("no problem mentions rol: %v", list)
	}
	if typo.Subject != "three-site-typo.yaml:17" {
		t.Errorf("subject = %q, want three-site-typo.yaml:17", typo.Subject)
	}
	if !strings.Contains(typo.Msg, `did you mean "role"?`) || !strings.Contains(typo.Msg, "nodes[0]") {
		t.Errorf("message = %q", typo.Msg)
	}
}

// TestDecodeInlineAddressing checks an addressing block written inline (supernet and pools)
// instead of as a $ref.
func TestDecodeInlineAddressing(t *testing.T) {
	got, err := topology.Decode("t.yaml", []byte(`
schema: beaver.topology/v1alpha1
addressing:
  supernet: 10.42.0.0/16
  pools:
    - name: sites
      prefix: 10.42.0.0/17
      child_bits: 20
    - name: overlay
      prefix: 10.42.240.0/21
      child_bits: 32
      skip_first: 1
    - name: growth
      prefix: 10.42.128.0/18
      reserved: true
`), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := topology.Addressing{
		Supernet: netip.MustParsePrefix("10.42.0.0/16"),
		Pools: []topology.AddressPool{
			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
			{Name: "overlay", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
			{Name: "growth", Prefix: netip.MustParsePrefix("10.42.128.0/18"), Reserved: true},
		},
	}
	if !reflect.DeepEqual(got.Addressing, want) {
		t.Errorf("addressing = %+v\nwant %+v", got.Addressing, want)
	}
}

// TestDecodeLabelsAdvertisesAndEndpoints checks the node fields that hold lists and maps.
func TestDecodeLabelsAdvertisesAndEndpoints(t *testing.T) {
	got, err := topology.Decode("t.yaml", []byte(`
schema: beaver.topology/v1alpha1
nodes:
  - id: n1
    site: s
    role: spoke
    endpoints: ["[2001:db8::1]:51820", 198.51.100.7:443]
    advertises: [site, 192.168.50.0/24]
    labels: { env: prod, tier: 2, empty: "" }
`), 0)
	if err != nil {
		t.Fatal(err)
	}
	node := got.Nodes[0]
	if len(node.Endpoints) != 2 || node.Endpoints[0] != netip.MustParseAddrPort("[2001:db8::1]:51820") {
		t.Errorf("endpoints = %v", node.Endpoints)
	}
	if len(node.Advertises) != 2 || !node.Advertises[0].Site || node.Advertises[1].Prefix != netip.MustParsePrefix("192.168.50.0/24") ||
		node.Advertises[1].String() != "192.168.50.0/24" || node.Advertises[0].String() != "site" {
		t.Errorf("advertises = %+v", node.Advertises)
	}
	if !reflect.DeepEqual(node.Labels, map[string]string{"env": "prod", "tier": "2", "empty": ""}) {
		t.Errorf("labels = %v", node.Labels)
	}
}

// Everything wrong at once is reported at once, each at its own line.
func TestDecodeReportsEveryProblem(t *testing.T) {
	list := mustFail(t, `schema: beaver.topology/v1alpha1
nodes:
  - id: a
    site: s
    rol: hub
  - id: b
    site: s
    role: king
    nat: sideways
    endpoints: [not-an-endpoint]
links:
  - kind: wire
    a: a
    b: b
    cost: 0
    health: maybe
intents:
  - allow: { from: "a b", to: b, ports: [0, 70000, http] }
  - allow: { from: a, to: b }
    deny: { from: a, to: b }
`)
	wantProblem(t, list, 5, "unknown field", `"rol"`, `did you mean "role"`)
	wantProblem(t, list, 3, "nodes[0]", `missing required field "role"`)
	wantProblem(t, list, 8, `"king"`, "hub or spoke")
	wantProblem(t, list, 9, `"sideways"`, "none, behind or unknown")
	wantProblem(t, list, 10, `"not-an-endpoint"`, "203.0.113.10:51820")
	wantProblem(t, list, 12, `"wire"`, "hub-spoke or direct")
	wantProblem(t, list, 15, "not positive")
	wantProblem(t, list, 16, `"maybe"`, "required or assumed")
	wantProblem(t, list, 18, `"a b"`, "not a valid node ID")
	wantProblem(t, list, 18, `"0"`, "port")
	wantProblem(t, list, 18, `"70000"`, "port")
	wantProblem(t, list, 18, `"http"`, "port")
	wantProblem(t, list, 19, "not both")
	for i := 1; i < len(list); i++ {
		if lineOf(list[i-1]) > lineOf(list[i]) {
			t.Errorf("problems are not in line order: %v", list)
			break
		}
	}
}

// lineOf reads the line number out of a problem's "file:line" subject, or 0 when it has none.
func lineOf(problem *types.Error) int {
	_, after, _ := strings.Cut(problem.Subject, ":")
	n := 0
	for _, digit := range after {
		n = n*10 + int(digit-'0')
	}
	return n
}

// TestDecodeRejectsAmbiguousShapes checks, case by case, that each kind of ambiguous or wrong
// input is refused at the right line with a clear message.
func TestDecodeRejectsAmbiguousShapes(t *testing.T) {
	cases := map[string]struct {
		yaml     string
		line     int
		fragment string
	}{
		"empty":             {"", 0, "empty"},
		"only comments":     {"# nothing\n", 0, "empty"},
		"scalar root":       {"hello\n", 1, "expected a mapping"},
		"list root":         {"- a\n- b\n", 1, "expected a mapping"},
		"two documents":     {"schema: x\n---\nschema: y\n", 2, "more than one"},
		"syntax error":      {"schema: x\nnodes: [a, b\n", 0, ""},
		"tab indent":        {"nodes:\n\t- id: a\n", 2, ""},
		"unknown top level": {"schema: x\nsitez: []\n", 2, `did you mean "sites"`},
		"far from any name": {"schema: x\nbanana: 1\n", 2, "the fields here are"},
		"duplicate key":     {"schema: x\nschema: y\n", 2, "given twice (first on line 1)"},
		"merge key":         {"base: &b {id: a}\nsites:\n  - <<: *b\n", 1, "unknown field"},
		"nodes not a list":  {"nodes: {id: a}\n", 1, "expected a list"},
		"node not a map":    {"nodes:\n  - plain\n", 2, "expected a mapping"},
		"id is a list":      {"sites:\n  - id: [a]\n", 2, "expected text"},
		"empty id":          {"sites:\n  - id:\n", 2, "must not be empty"},
		"missing id":        {"sites:\n  - provider: aws\n", 2, `missing required field "id"`},
		"cost is text":      {"links:\n  - {kind: direct, a: x, b: y, cost: cheap}\n", 2, "whole number"},
		"cost is a float":   {"links:\n  - {kind: direct, a: x, b: y, cost: 1.5}\n", 2, "whole number"},
		"cost is huge":      {"links:\n  - {kind: direct, a: x, b: y, cost: 99999999999999999999}\n", 2, "whole number"},
		"reserved is text":  {"addressing:\n  pools:\n    - {name: p, prefix: 10.0.0.0/8, reserved: yes please}\n", 3, "true or false"},
		"ref and inline":    {"addressing:\n  $ref: plan.yaml\n  supernet: 10.0.0.0/8\n", 2, "not both"},
		"host bits":         {"addressing:\n  supernet: 10.42.1.5/16\n", 2, "host bits set; write 10.42.0.0/16"},
		"ipv6 prefix":       {"addressing:\n  supernet: fd00::/64\n", 2, "not IPv4"},
		"not a prefix":      {"addressing:\n  supernet: ten\n", 2, "not a prefix"},
		"bad child bits":    {"addressing:\n  pools:\n    - {name: p, prefix: 10.0.0.0/8, child_bits: 40}\n", 3, "between 0 and 32"},
		"negative skip":     {"addressing:\n  pools:\n    - {name: p, prefix: 10.0.0.0/8, skip_first: -1}\n", 3, "negative"},
		"advert not prefix": {"nodes:\n  - {id: a, site: s, role: hub, advertises: [everything]}\n", 2, "not a prefix"},
		"labels not a map":  {"nodes:\n  - {id: a, site: s, role: hub, labels: [x]}\n", 2, "mapping of label"},
		"label is a list":   {"nodes:\n  - {id: a, site: s, role: hub, labels: {k: [v]}}\n", 2, "plain text"},
		"intent empty":      {"intents:\n  - {}\n", 2, "allow or deny"},
		"intent odd key":    {"intents:\n  - permit: {from: a, to: b}\n", 2, "the fields here are [allow deny]"},
		"selector bad kind": {"intents:\n  - allow: {from: team:x, to: b}\n", 2, "unknown selector kind"},
		"ports not a list":  {"intents:\n  - allow: {from: a, to: b, ports: 22}\n", 2, "expected a list"},
	}
	for name, c := range cases {
		list := mustFail(t, c.yaml)
		if len(list) == 0 {
			t.Errorf("%s: no problems", name)
			continue
		}
		found := false
		for _, problem := range list {
			wantSubject := "t.yaml"
			if c.line > 0 {
				wantSubject += ":" + itoa(c.line)
			}
			if c.fragment == "" && c.line == 0 {
				found = true // any located-or-not syntax error is fine
			}
			if problem.Subject == wantSubject && strings.Contains(problem.Msg, c.fragment) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want a problem at line %d mentioning %q, got:\n%v", name, c.line, c.fragment, list)
		}
	}
}

// TestDecodeAcceptsEmptySectionsAndOptionalFields checks that empty or missing optional sections
// decode without error.
func TestDecodeAcceptsEmptySectionsAndOptionalFields(t *testing.T) {
	got, err := topology.Decode("t.yaml", []byte("schema: beaver.topology/v1alpha1\nsites:\nnodes: []\nlinks:\nintents:\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sites)+len(got.Nodes)+len(got.Links)+len(got.Intents) != 0 {
		t.Errorf("%+v", got)
	}
	// Nothing about schema or IDs is checked here: those are the V25 rules.
	loose, err := topology.Decode("t.yaml", []byte("schema: beaver.topology/v9\nsites:\n  - id: Not A Valid ID\n  - id: Not A Valid ID\n"), 0)
	if err != nil || loose.Schema != "beaver.topology/v9" || len(loose.Sites) != 2 {
		t.Errorf("decoding is stricter than it should be: %+v, %v", loose, err)
	}
}

// TestSelectorsRoundTrip checks that each selector form parses and prints back in its canonical
// text, and bad forms are refused.
func TestSelectorsRoundTrip(t *testing.T) {
	cases := map[string]string{
		"*":                "*",
		"builder-1":        "node:builder-1",
		"node:builder-1":   "node:builder-1",
		"site:aws":         "site:aws",
		"label:env=prod":   "label:env=prod",
		"label:team.x=a=b": "label:team.x=a=b",
		"site:mars":        "site:mars",
	}
	for text, want := range cases {
		selector, err := topology.ParseSelector(text)
		if err != nil || selector.String() != want {
			t.Errorf("ParseSelector(%q) = %v, %v; want %s", text, selector, err, want)
		}
		again, err := topology.ParseSelector(selector.String())
		if err != nil || again != selector {
			t.Errorf("canonical text %q does not parse back: %v, %v", selector, again, err)
		}
	}
	for _, text := range []string{"", ":", "node:", "site:", "node:Bad ID", "Bad ID", "label:", "label:k", "label:k=", "label:=v", "label:k =v",
		"team:x", "node:a:b", "**", "a b"} {
		if selector, err := topology.ParseSelector(text); !types.HasCode(err, types.ErrInvalidTopology) {
			t.Errorf("ParseSelector(%q) = %v, %v; want INVALID_TOPOLOGY", text, selector, err)
		}
	}
	if (topology.Selector{}).String() != "" {
		t.Error("the zero Selector should print as nothing")
	}
}

// TestEnums checks the text forms of the NAT, health and other enums and that unknown text is
// refused.
func TestEnums(t *testing.T) {
	for text, want := range map[string]topology.NATMode{"none": topology.NATNone, "behind": topology.NATBehind, "unknown": topology.NATUnknown} {
		if got, ok := topology.ParseNATMode(text); !ok || got != want || got.String() != text {
			t.Errorf("NAT %q: %v, %v", text, got, ok)
		}
	}
	for text, want := range map[string]topology.HealthMode{"required": topology.HealthRequired, "assumed": topology.HealthAssumed} {
		if got, ok := topology.ParseHealthMode(text); !ok || got != want || got.String() != text {
			t.Errorf("health %q: %v, %v", text, got, ok)
		}
	}
	for _, bad := range []string{"", "None", "BEHIND", "yes"} {
		if _, ok := topology.ParseNATMode(bad); ok {
			t.Errorf("ParseNATMode(%q) accepted", bad)
		}
	}
	for _, bad := range []string{"", "Required", "unset"} {
		if _, ok := topology.ParseHealthMode(bad); ok {
			t.Errorf("ParseHealthMode(%q) accepted", bad)
		}
	}
	// Zero values mean "not said", never a real choice.
	var nat topology.NATMode
	var health topology.HealthMode
	var action topology.Action
	if nat != topology.NATUnknown || nat.String() != "unknown" || health.String() != "unset" || action.String() != "unknown" ||
		topology.ActionAllow.String() != "allow" || topology.ActionDeny.String() != "deny" {
		t.Error("zero values or names are wrong")
	}
}
