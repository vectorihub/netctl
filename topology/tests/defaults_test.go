package tests

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// link builds a Link of the given kind between two nodes, with everything else unset.
func link(kind types.LinkKind, a, b types.NodeID) topology.Link {
	return topology.Link{Kind: kind, A: a, B: b}
}

// P1-TOPO-001: defaults are applied, in one place.
func TestNormalizeFillsDefaults(t *testing.T) {
	top := topology.Topology{Links: []topology.Link{
		link(types.LinkHubSpoke, "a", "b"),
		link(types.LinkDirect, "b", "c"),
		{Kind: types.LinkHubSpoke, A: "a", B: "c", Cost: 7, Health: topology.HealthRequired}, // given: kept
		{Kind: types.LinkDirect, A: "a", B: "d", Cost: 99, Health: topology.HealthAssumed},   // given: kept
		{A: "x", B: "y"}, // unknown kind: nothing to default from
	}}
	top.Normalize()
	want := map[types.LinkID]struct {
		cost   int
		health topology.HealthMode
	}{
		"a~b": {20, topology.HealthAssumed},
		"b~c": {10, topology.HealthRequired},
		"a~c": {7, topology.HealthRequired},
		"a~d": {99, topology.HealthAssumed},
		"x~y": {0, topology.HealthUnset},
	}
	if len(top.Links) != len(want) {
		t.Fatalf("%d links", len(top.Links))
	}
	for _, got := range top.Links {
		w := want[got.ID]
		if got.Cost != w.cost || got.Health != w.health {
			t.Errorf("link %s = cost %d health %s, want cost %d health %s", got.ID, got.Cost, got.Health, w.cost, w.health)
		}
	}
	if topology.DefaultHubSpokeCost != 20 || topology.DefaultDirectCost != 10 {
		t.Error("the documented defaults changed")
	}
	// A direct link is cheaper than the two hub hops it shortcuts.
	if topology.DefaultDirectCost >= 2*topology.DefaultHubSpokeCost {
		t.Error("a default direct link should beat the hub path")
	}
}

// TestNormalizeNATIsUnknownByDefault checks that a node that never said anything about NAT is
// NATUnknown.
func TestNormalizeNATIsUnknownByDefault(t *testing.T) {
	got, err := topology.Decode("t.yaml", []byte("nodes:\n  - {id: a, site: s, role: hub}\n"), 0)
	if err != nil || got.Nodes[0].NAT != topology.NATUnknown {
		t.Errorf("%+v, %v", got, err)
	}
}

// Links are stored A before B with a derived ID, whichever end was written first.
func TestNormalizeOrdersLinkEndsAndDerivesID(t *testing.T) {
	forward, backward := link(types.LinkHubSpoke, "aws-hub", "builder-1"), link(types.LinkHubSpoke, "builder-1", "aws-hub")
	wrong := link(types.LinkDirect, "x", "y")
	wrong.ID = "something-else" // the ID is derived, never chosen
	top := topology.Topology{Links: []topology.Link{backward, wrong}}
	top.Normalize()
	alt := topology.Topology{Links: []topology.Link{forward}}
	alt.Normalize()
	if top.Links[0] != alt.Links[0] || top.Links[0].A != "aws-hub" || top.Links[0].B != "builder-1" || top.Links[0].ID != "aws-hub~builder-1" {
		t.Errorf("%+v vs %+v", top.Links[0], alt.Links[0])
	}
	if top.Links[1].ID != "x~y" {
		t.Errorf("a wrong ID survived: %q", top.Links[1].ID)
	}
	if top.Links[0].ID != types.LinkIDOf("builder-1", "aws-hub") {
		t.Error("the ID must match types.LinkIDOf, which ipam uses to own the link's /30")
	}
}

// Sites, nodes and links are sorted by ID; intents are never touched.
func TestNormalizeSortsButKeepsIntentOrder(t *testing.T) {
	deny := topology.Intent{Action: topology.ActionDeny, From: topology.Selector{Kind: topology.SelectorAll}, To: topology.Selector{Kind: topology.SelectorSite, ID: "z"}}
	allow := topology.Intent{Action: topology.ActionAllow, From: topology.Selector{Kind: topology.SelectorAll}, To: topology.Selector{Kind: topology.SelectorAll}, Ports: []uint16{443}}
	top := topology.Topology{
		Sites:   []topology.Site{{ID: "mid"}, {ID: "zed"}, {ID: "alpha"}},
		Nodes:   []topology.Node{{ID: "n-10"}, {ID: "n-2"}, {ID: "N-1"}},
		Links:   []topology.Link{link(types.LinkDirect, "n-2", "n-10"), link(types.LinkHubSpoke, "a", "b")},
		Intents: []topology.Intent{deny, allow, deny},
	}
	top.Normalize()
	ids := func(n int, get func(i int) string) string {
		var out []string
		for i := 0; i < n; i++ {
			out = append(out, get(i))
		}
		return strings.Join(out, " ")
	}
	if got := ids(3, func(i int) string { return string(top.Sites[i].ID) }); got != "alpha mid zed" {
		t.Errorf("sites: %s", got)
	}
	// Plain byte order, not "natural" order: "N-1" < "n-10" < "n-2".
	if got := ids(3, func(i int) string { return string(top.Nodes[i].ID) }); got != "N-1 n-10 n-2" {
		t.Errorf("nodes: %s", got)
	}
	if got := ids(2, func(i int) string { return string(top.Links[i].ID) }); got != "a~b n-10~n-2" {
		t.Errorf("links: %s", got)
	}
	if !reflect.DeepEqual(top.Intents, []topology.Intent{deny, allow, deny}) {
		t.Errorf("intents were reordered: %+v", top.Intents)
	}
}

// A pair joined twice (hub-spoke and direct) still gets one fixed order.
func TestNormalizeOrdersLinksWithTheSameID(t *testing.T) {
	one := link(types.LinkHubSpoke, "a", "b")
	two := link(types.LinkDirect, "a", "b")
	three := topology.Link{Kind: types.LinkDirect, A: "b", B: "a", Cost: 5}
	// Same cost and health, so only the kind tells these two apart.
	sameA := topology.Link{Kind: types.LinkHubSpoke, A: "a", B: "b", Cost: 10, Health: topology.HealthRequired}
	sameB := topology.Link{Kind: types.LinkDirect, A: "a", B: "b", Cost: 10, Health: topology.HealthRequired}
	orders := [][]topology.Link{{one, two, three, sameA, sameB}, {sameB, three, two, one, sameA}, {two, sameA, one, sameB, three}, {sameA, sameB, two, three, one}}
	var first []topology.Link
	for i, order := range orders {
		top := topology.Topology{Links: append([]topology.Link(nil), order...)}
		top.Normalize()
		if i == 0 {
			first = top.Links
		} else if !reflect.DeepEqual(first, top.Links) {
			t.Errorf("order %d gave %+v, want %+v", i, top.Links, first)
		}
	}
}

// TestNormalizeIsIdempotentAndSafe checks that normalising twice changes nothing more, and that a
// nil topology is safe.
func TestNormalizeIsIdempotentAndSafe(t *testing.T) {
	got := decode(t, "three-site.yaml")
	once := *got
	got.Normalize()
	if !reflect.DeepEqual(&once, got) {
		t.Error("Normalize changed an already-normal Topology")
	}
	var nilTopology *topology.Topology
	nilTopology.Normalize() // must not panic
	var zero topology.Topology
	zero.Normalize()
	if zero.Sites != nil || zero.Nodes != nil || zero.Links != nil || zero.Intents != nil {
		t.Errorf("Normalize invented entries: %+v", zero)
	}
	// Given values and nil-versus-empty stay as they were.
	labelled := topology.Topology{Nodes: []topology.Node{{ID: "a"}, {ID: "b", Labels: map[string]string{}}}}
	labelled.Normalize()
	if labelled.Nodes[0].Labels != nil || labelled.Nodes[1].Labels == nil {
		t.Error("Normalize changed nil versus empty labels")
	}
}

// document renders a three-site-like topology with every list in the given order.
func document(sites, nodes, links, intents []int, swapEnds bool) string {
	siteText := []string{"  - {id: aws, provider: aws}", "  - {id: builder}", "  - {id: dgx}"}
	nodeText := []string{
		"  - {id: aws-hub, site: aws, role: hub, nat: none}",
		"  - {id: builder-1, site: builder, role: spoke, nat: behind, advertises: [site]}",
		"  - {id: dgx-spark, site: dgx, role: spoke, nat: behind}",
	}
	linkEnds := [][2]string{{"aws-hub", "builder-1"}, {"aws-hub", "dgx-spark"}, {"builder-1", "dgx-spark"}}
	kinds := []string{"hub-spoke", "hub-spoke", "direct"}
	intentText := []string{
		"  - allow: {from: builder-1, to: dgx-spark}",
		"  - deny: {from: 'site:builder', to: 'site:aws', ports: [5432]}",
		"  - allow: {from: '*', to: aws-hub}",
	}
	var b strings.Builder
	b.WriteString("schema: beaver.topology/v1alpha1\nsites:\n")
	for _, i := range sites {
		b.WriteString(siteText[i] + "\n")
	}
	b.WriteString("nodes:\n")
	for _, i := range nodes {
		b.WriteString(nodeText[i] + "\n")
	}
	b.WriteString("links:\n")
	for _, i := range links {
		a, c := linkEnds[i][0], linkEnds[i][1]
		if swapEnds {
			a, c = c, a
		}
		fmt.Fprintf(&b, "  - {kind: %s, a: %s, b: %s}\n", kinds[i], a, c)
	}
	b.WriteString("intents:\n")
	for _, i := range intents {
		b.WriteString(intentText[i] + "\n")
	}
	return b.String()
}

// decodeText decodes YAML given as text, failing the test on any error.
func decodeText(t *testing.T, text string) *topology.Topology {
	t.Helper()
	got, err := topology.Decode("t.yaml", []byte(text), 0)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	return got
}

// P1-TOPO-002: listing sites, nodes and links in a different order, or naming a
// link's ends the other way round, decodes to the identical Topology. (Hash() is
// V29; this compares the Topology value itself, which Hash will be computed from.)
func TestDecodeOrderDoesNotChangeTheTopology(t *testing.T) {
	identity := []int{0, 1, 2}
	want := decodeText(t, document(identity, identity, identity, identity, false))
	random := rand.New(rand.NewPCG(7, 7))
	for round := 0; round < 200; round++ {
		shuffle := func() []int {
			out := append([]int(nil), identity...)
			random.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
			return out
		}
		got := decodeText(t, document(shuffle(), shuffle(), shuffle(), identity, random.IntN(2) == 0))
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("round %d differs:\nwant %+v\ngot  %+v", round, want, got)
		}
	}
}

// Intent order is part of the meaning, so reordering intents is a real change.
func TestDecodeIntentOrderIsKept(t *testing.T) {
	identity := []int{0, 1, 2}
	first := decodeText(t, document(identity, identity, identity, []int{0, 1, 2}, false))
	second := decodeText(t, document(identity, identity, identity, []int{2, 1, 0}, false))
	if reflect.DeepEqual(first.Intents, second.Intents) {
		t.Fatal("different intent orders decoded to the same intents")
	}
	if first.Intents[1].Action != topology.ActionDeny || second.Intents[1].Action != topology.ActionDeny ||
		first.Intents[0].From.String() != "node:builder-1" || second.Intents[0].To.String() != "node:aws-hub" {
		t.Errorf("intents were reordered: %+v / %+v", first.Intents, second.Intents)
	}
	if !reflect.DeepEqual(first.Nodes, second.Nodes) || !reflect.DeepEqual(first.Links, second.Links) {
		t.Error("intent order should not affect nodes or links")
	}
}

// Property: any shuffle of a code-built topology normalises to the same value.
func TestNormalizeIsOrderIndependent(t *testing.T) {
	build := func(random *rand.Rand) topology.Topology {
		var top topology.Topology
		for i := 0; i < 30; i++ {
			top.Sites = append(top.Sites, topology.Site{ID: types.SiteID(fmt.Sprintf("site-%02d", i))})
			top.Nodes = append(top.Nodes, topology.Node{ID: types.NodeID(fmt.Sprintf("node-%02d", i)), Site: types.SiteID(fmt.Sprintf("site-%02d", i))})
		}
		for i := 1; i < 30; i++ {
			a, b := types.NodeID(fmt.Sprintf("node-%02d", i)), types.NodeID(fmt.Sprintf("node-%02d", random.IntN(i)))
			kind := types.LinkHubSpoke
			if i%3 == 0 {
				kind = types.LinkDirect
			}
			top.Links = append(top.Links, link(kind, a, b)) // ends deliberately in either order
		}
		return top
	}
	base := build(rand.New(rand.NewPCG(1, 1)))
	base.Normalize()
	for seed := uint64(1); seed <= 50; seed++ {
		random := rand.New(rand.NewPCG(seed, 1))
		top := build(rand.New(rand.NewPCG(1, 1)))
		random.Shuffle(len(top.Sites), func(i, j int) { top.Sites[i], top.Sites[j] = top.Sites[j], top.Sites[i] })
		random.Shuffle(len(top.Nodes), func(i, j int) { top.Nodes[i], top.Nodes[j] = top.Nodes[j], top.Nodes[i] })
		random.Shuffle(len(top.Links), func(i, j int) { top.Links[i], top.Links[j] = top.Links[j], top.Links[i] })
		for i := range top.Links {
			if random.IntN(2) == 0 {
				top.Links[i].A, top.Links[i].B = top.Links[i].B, top.Links[i].A
			}
		}
		top.Normalize()
		if !reflect.DeepEqual(base, top) {
			t.Fatalf("seed %d: shuffled topology normalised differently", seed)
		}
	}
}
