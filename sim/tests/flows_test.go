package tests

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// flowText writes a flow list as text, one flow per line after a line giving the
// count, the form kept in the golden file.
func flowText(flows []sim.Flow) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%d flows\n", len(flows))
	for _, f := range flows {
		fmt.Fprintf(&out, "%s | %s | %s > %s | %s > %s | intent=%v asym=%v ports=%v\n",
			f.Label, f.Family, f.Src, f.Dst, f.From, f.To, f.Intent, f.AllowAsymmetric, f.Ports)
	}
	return out.String()
}

// mustFlows derives the flows of the three-site network, or stops the test.
func mustFlows(t *testing.T, topo *topology.Topology, plan *ipam.IPAM, opts sim.Options) []sim.Flow {
	t.Helper()
	flows, err := sim.Flows(topo, plan, opts)
	if err != nil {
		t.Fatalf("Flows: %v", err)
	}
	return flows
}

// threeSite returns the reference topology and address plan.
func threeSite(t *testing.T) (*topology.Topology, *ipam.IPAM) {
	t.Helper()
	plan, err := fixture.ThreeSitePlan()
	if err != nil {
		t.Fatalf("building the address plan: %v", err)
	}
	return fixture.ThreeSiteTopology(), plan
}

// P1-SIM-044
//
// TestFlowsGolden checks the derived flow list for the three-site network against
// the reviewed list in testdata/golden/sim-flows-three-site.txt, in order. The
// count is part of the file, so a flow that appears or disappears is a change to
// a reviewed file.
func TestFlowsGolden(t *testing.T) {
	want, err := os.ReadFile("../../testdata/golden/sim-flows-three-site.txt")
	if err != nil {
		t.Fatalf("reading the golden file: %v", err)
	}
	topo, plan := threeSite(t)
	if got := flowText(mustFlows(t, topo, plan, sim.Options{})); got != string(want) {
		t.Errorf("flows differ from the golden file\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFlowFamilyCounts checks the test guide's summary of the three-site flows: 6
// overlay, 6 site, 1 deny, 3 negative, 16 in all; and the guide's count for a
// fourth node that advertises its site: 12 overlay, 12 site, 1 deny, 4 negative,
// 29 in all.
func TestFlowFamilyCounts(t *testing.T) {
	count := func(flows []sim.Flow) map[sim.Family]int {
		counts := map[sim.Family]int{}
		for _, f := range flows {
			counts[f.Family]++
		}
		return counts
	}
	topo, plan := threeSite(t)
	flows := mustFlows(t, topo, plan, sim.Options{})
	if got := count(flows); len(flows) != 16 || got[sim.FamilyOverlay] != 6 || got[sim.FamilySite] != 6 || got[sim.FamilyDeny] != 1 || got[sim.FamilyNegative] != 3 {
		t.Errorf("three nodes: %d flows %v, want 16 = 6+6+1+3", len(flows), got)
	}

	topo.Nodes = append(topo.Nodes, topology.Node{ID: "mac-studio", Site: "studio", Role: types.RoleSpoke, NAT: topology.NATBehind,
		Advertises: []topology.Advertisement{{Site: true}}})
	topo.Sites = append(topo.Sites, topology.Site{ID: "studio", Provider: "owned"})
	topo.Links = append(topo.Links, topology.Link{Kind: types.LinkHubSpoke, A: "aws-hub", B: "mac-studio"})
	topo.Normalize()
	if _, err := plan.Allocate("sites", ipam.Site("studio")); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Allocate("overlay", ipam.Node("mac-studio")); err != nil {
		t.Fatal(err)
	}
	flows = mustFlows(t, topo, plan, sim.Options{})
	if got := count(flows); len(flows) != 29 || got[sim.FamilyOverlay] != 12 || got[sim.FamilySite] != 12 || got[sim.FamilyDeny] != 1 || got[sim.FamilyNegative] != 4 {
		t.Errorf("four nodes: %d flows %v, want 29 = 12+12+1+4", len(flows), got)
	}
}

// TestFlowsAreSortedAndDeterministic checks that the list is sorted by label and
// is the same whatever order the topology's nodes are written in: 40 shuffles.
func TestFlowsAreSortedAndDeterministic(t *testing.T) {
	topo, plan := threeSite(t)
	want := flowText(mustFlows(t, topo, plan, sim.Options{}))
	rng := rand.New(rand.NewPCG(42, 1))
	for run := 0; run < 40; run++ {
		shuffled := fixture.ThreeSiteTopology()
		rng.Shuffle(len(shuffled.Nodes), func(i, j int) { shuffled.Nodes[i], shuffled.Nodes[j] = shuffled.Nodes[j], shuffled.Nodes[i] })
		rng.Shuffle(len(shuffled.Sites), func(i, j int) { shuffled.Sites[i], shuffled.Sites[j] = shuffled.Sites[j], shuffled.Sites[i] })
		if got := flowText(mustFlows(t, shuffled, plan, sim.Options{})); got != want {
			t.Fatalf("run %d: a shuffled topology gave a different flow list", run)
		}
	}
	flows := mustFlows(t, topo, plan, sim.Options{})
	if !slices.IsSortedFunc(flows, func(a, b sim.Flow) int { return strings.Compare(a.Label, b.Label) }) {
		t.Error("flows are not sorted by label")
	}
	labels := map[string]bool{}
	for _, f := range flows {
		if labels[f.Label] {
			t.Errorf("label %q appears twice", f.Label)
		}
		labels[f.Label] = true
	}
}

// TestIntentFlowsAreMergedIntoOverlayFlows checks that allow intents add no flows
// of their own but mark the overlay flows they ask for: the two spoke-to-spoke
// intents and the "everyone to the hub" intent give four marked flows, and no other
// is marked.
func TestIntentFlowsAreMergedIntoOverlayFlows(t *testing.T) {
	topo, plan := threeSite(t)
	var marked []string
	for _, f := range mustFlows(t, topo, plan, sim.Options{}) {
		if f.Intent {
			marked = append(marked, f.Label)
			if f.Family != sim.FamilyOverlay {
				t.Errorf("%s is marked as an intent flow but is %s", f.Label, f.Family)
			}
		}
	}
	want := []string{
		"builder-1→aws-hub (overlay)", "builder-1→dgx-spark (overlay)",
		"dgx-spark→aws-hub (overlay)", "dgx-spark→builder-1 (overlay)",
	}
	if !slices.Equal(marked, want) {
		t.Errorf("intent flows = %v, want %v", marked, want)
	}
}

// TestDenyFlowIsReachableAndNoted checks the deny flow: from the first node the
// "from" selector picks to the first node the "to" selector picks, carrying the
// ports and a note saying policy is not enforced, and expected to be reachable.
func TestDenyFlowIsReachableAndNoted(t *testing.T) {
	topo, plan := threeSite(t)
	var deny []sim.Flow
	for _, f := range mustFlows(t, topo, plan, sim.Options{}) {
		if f.Family == sim.FamilyDeny {
			deny = append(deny, f)
		}
	}
	if len(deny) != 1 {
		t.Fatalf("%d deny flows, want 1", len(deny))
	}
	f := deny[0]
	if f.From != "builder-1" || f.To != "aws-hub" || f.Src != b1Addr || f.Dst != hubAddr || !slices.Equal(f.Ports, []uint16{5432}) ||
		f.Label != "builder-1→aws-hub (deny #1: site:builder→site:aws ports 5432)" || !strings.Contains(f.Note, "not enforced") {
		t.Errorf("deny flow = %+v", f)
	}
	p := computePlan(t, fixture.ThreeSite(true))
	if got := p.net().Run(f, false); got.Verdict != sim.VerdictOK {
		t.Errorf("the deny flow is %s, want OK (reachable, policy not enforced)", got.Verdict)
	}
}

// TestSiteFlowsUseTheFirstUsableAddress checks the site flows: the address one
// above the network address of each advertised block, injected at the node that
// advertises the site; and that a site no node advertises gets no flows.
func TestSiteFlowsUseTheFirstUsableAddress(t *testing.T) {
	topo, plan := threeSite(t)
	got := map[string]string{}
	for _, f := range mustFlows(t, topo, plan, sim.Options{}) {
		if f.Family == sim.FamilySite {
			got[f.Label] = fmt.Sprintf("%s > %s at %s", f.Src, f.Dst, f.From)
		}
	}
	for label, want := range map[string]string{
		"aws→builder (site)": "10.42.0.1 > 10.42.16.1 at aws-hub",
		"builder→dgx (site)": "10.42.16.1 > 10.42.32.1 at builder-1",
		"dgx→aws (site)":     "10.42.32.1 > 10.42.0.1 at dgx-spark",
	} {
		if got[label] != want {
			t.Errorf("%s = %q, want %q", label, got[label], want)
		}
	}

	quiet := fixture.ThreeSiteTopology()
	quiet.Nodes[2].Advertises = nil
	for _, f := range mustFlows(t, quiet, plan, sim.Options{}) {
		if f.Family == sim.FamilySite && (strings.Contains(f.Label, "dgx")) {
			t.Errorf("a site nobody advertises has a flow: %s", f.Label)
		}
	}
}

// TestNegativeFlowsAvoidTheSupernet checks the negative flows: one per node, to
// 192.168.1.1, and if the supernet contains that address, to the next candidate
// outside it.
func TestNegativeFlowsAvoidTheSupernet(t *testing.T) {
	topo, plan := threeSite(t)
	for _, f := range mustFlows(t, topo, plan, sim.Options{}) {
		if f.Negative() && (f.Dst != addrOf("192.168.1.1") || f.To != "" || f.Note == "") {
			t.Errorf("negative flow = %+v", f)
		}
	}
	registry, err := ipam.New(ipam.Snapshot{
		Schema: ipam.Schema, Supernet: prefixOf("192.168.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: prefixOf("192.168.0.0/17"), ChildBits: 20},
			{Name: "overlay", Prefix: prefixOf("192.168.240.0/21"), ChildBits: 32},
		},
	}, fixture.PlanClock, ipam.DefaultQuarantine)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"aws", "builder", "dgx"} {
		registry.Allocate("sites", ipam.Site(site))
	}
	for _, node := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		registry.Allocate("overlay", ipam.Node(node))
	}
	for _, f := range mustFlows(t, fixture.ThreeSiteTopology(), registry, sim.Options{}) {
		if f.Negative() && f.Dst != addrOf("172.31.255.1") {
			t.Errorf("negative flow goes to %s, want 172.31.255.1 (192.168.1.1 is inside this supernet)", f.Dst)
		}
	}
}

// TestAllowAsymmetricMarksBothDirections checks that a pair allowed to be
// asymmetric applies to the overlay and site flows between the two nodes in both
// directions, and to nothing else.
func TestAllowAsymmetricMarksBothDirections(t *testing.T) {
	topo, plan := threeSite(t)
	flows := mustFlows(t, topo, plan, sim.Options{AllowAsymmetric: [][2]types.NodeID{{"builder-1", "dgx-spark"}}})
	var marked []string
	for _, f := range flows {
		if f.AllowAsymmetric {
			marked = append(marked, f.Label)
		}
	}
	want := []string{"builder-1→dgx-spark (overlay)", "builder→dgx (site)", "dgx-spark→builder-1 (overlay)", "dgx→builder (site)"}
	if !slices.Equal(marked, want) {
		t.Errorf("marked = %v, want %v", marked, want)
	}
}

// TestFlowsRefuseToTestLess checks that Flows reports a plan it cannot fully
// simulate instead of quietly testing less: a missing overlay address, a site
// with no block, an intent that picks nothing and nil input, all at once where
// they are independent.
func TestFlowsRefuseToTestLess(t *testing.T) {
	topo, plan := threeSite(t)
	if _, err := sim.Flows(nil, plan, sim.Options{}); err == nil {
		t.Error("a nil topology was accepted")
	}
	if _, err := sim.Flows(topo, nil, sim.Options{}); err == nil {
		t.Error("a nil address plan was accepted")
	}

	broken := fixture.ThreeSiteTopology()
	broken.Nodes = append(broken.Nodes, topology.Node{ID: "mac-studio", Site: "studio", Role: types.RoleSpoke,
		Advertises: []topology.Advertisement{{Site: true}}})
	flows, err := sim.Flows(broken, plan, sim.Options{})
	if !types.HasCode(err, types.ErrNotFound) || flows != nil {
		t.Errorf("a node with no address: error %v, flows %d; want NOT_FOUND and none", err, len(flows))
	}
	if err != nil && (!strings.Contains(err.Error(), "mac-studio") || !strings.Contains(err.Error(), "studio")) {
		t.Errorf("the error does not name the node and site: %v", err)
	}

	lost := fixture.ThreeSiteTopology()
	lost.Intents = append(lost.Intents, topology.Intent{Action: topology.ActionAllow, From: lost.Intents[0].From, To: mustSelector(t, "node:ghost")})
	if _, err := sim.Flows(lost, plan, sim.Options{}); !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("an intent that picks nothing: %v, want INVALID_TOPOLOGY", err)
	}
}

// mustSelector parses a selector, or stops the test.
func mustSelector(t *testing.T, text string) topology.Selector {
	t.Helper()
	parsed, err := topology.ParseSelector(text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// P1-SIM-001
//
// TestHealthyThreeSiteEveryFlowPasses runs the whole derived flow set through the
// tables the engine computed, with the direct link up and with it down: 12 flows
// OK (6 overlay, 6 site, plus the deny flow, which is reachable), 3 negative
// flows UNREACHABLE_EXPECTED, and the report passes.
func TestHealthyThreeSiteEveryFlowPasses(t *testing.T) {
	topo, plan := threeSite(t)
	flows := mustFlows(t, topo, plan, sim.Options{})
	for name, g := range map[string]*fixture.Graph{"direct link up": fixture.ThreeSite(true), "direct link down": fixture.ThreeSiteDirectDown()} {
		p := computePlan(t, g)
		net := p.net()
		var results []sim.FlowResult
		for _, f := range flows {
			results = append(results, net.Run(f, false))
		}
		report := sim.NewReport(results)
		if !report.Passed || report.Summary.Count(sim.VerdictOK) != 13 || report.Summary.Count(sim.VerdictUnreachableExpected) != 3 || report.Summary.Total != 16 {
			t.Errorf("%s: passed %v, %d OK, %d expected-unreachable of %d; want a pass with 13 and 3 of 16\n%+v",
				name, report.Passed, report.Summary.Count(sim.VerdictOK), report.Summary.Count(sim.VerdictUnreachableExpected), report.Summary.Total, report.Summary.Verdicts)
		}
	}
}

// P1-SIM-021
//
// TestMissingSiteAdvertisement checks a plan where dgx-spark does not advertise
// its site block: flows to that site's address are BLACK_HOLE, while the overlay
// flows, which do not depend on the site block, are still OK. The flows are
// derived from the topology that says dgx advertises, and the tables from a plan
// that forgot to, which is the mistake being caught.
func TestMissingSiteAdvertisement(t *testing.T) {
	topo, plan := threeSite(t)
	flows := mustFlows(t, topo, plan, sim.Options{})
	input := fixture.ThreeSiteInput()
	input.Owned["dgx-spark"] = []netip.Prefix{prefixOf("10.42.240.3/32")}
	result, err := route.Compute(fixture.ThreeSite(true), input)
	if err != nil {
		t.Fatal(err)
	}
	net := sim.NewNet(fixture.ThreeSite(true), result.Tables, input.Owned, input.Local)
	var results []sim.FlowResult
	for _, f := range flows {
		results = append(results, net.Run(f, false))
	}
	byLabel := map[string]sim.Verdict{}
	for _, r := range results {
		byLabel[r.Flow.Label] = r.Verdict
	}
	if got := byLabel["builder→dgx (site)"]; got != sim.VerdictBlackHole {
		t.Errorf("builder→dgx (site) = %s, want BLACK_HOLE", got)
	}
	for _, label := range []string{"builder-1→dgx-spark (overlay)", "dgx-spark→builder-1 (overlay)", "aws-hub→dgx-spark (overlay)"} {
		if byLabel[label] != sim.VerdictOK {
			t.Errorf("%s = %s, want OK", label, byLabel[label])
		}
	}
	if report := sim.NewReport(results); report.Passed {
		t.Error("a plan that forgot an advertisement passed")
	}
}
