package tests

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// rowOf writes one route as a line of text: prefix, next hop, link, metric,
// source and whether it is active. Tests compare these lines, which read like
// the reference tables in the engineering plan.
func rowOf(r route.Route) string {
	hop, via := "-", "-"
	if r.NextHop != "" {
		hop = string(r.NextHop)
	}
	if r.Via != "" {
		via = string(r.Via)
	}
	state := "inactive"
	if r.Active {
		state = "active"
	}
	return fmt.Sprintf("%s via %s over %s metric %d %s %s", r.Prefix, hop, via, r.Metric, r.Source, state)
}

// rowsOf writes every route of a table as a line, sorted as text, so the
// comparison is about which routes exist and not about their order. Order is
// checked separately.
func rowsOf(table *route.Table) []string {
	rows := make([]string, 0, len(table.Routes))
	for _, r := range table.Routes {
		rows = append(rows, rowOf(r))
	}
	sort.Strings(rows)
	return rows
}

// wantRows checks that a table holds exactly the given lines, in any order.
func wantRows(t *testing.T, table *route.Table, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := rowsOf(table)
	if !slices.Equal(got, want) {
		t.Errorf("table of %s differs\n got:\n  %s\nwant:\n  %s", table.Node, join(got), join(want))
	}
}

// join writes lines one per row, indented, for failure messages.
func join(lines []string) string {
	text := ""
	for i, line := range lines {
		if i > 0 {
			text += "\n  "
		}
		text += line
	}
	return text
}

// mustCompute runs Compute and stops the test if it returns an error.
func mustCompute(t *testing.T, graph route.Graph, input route.Input) route.Result {
	t.Helper()
	result, err := route.Compute(graph, input)
	if err != nil {
		t.Fatalf("Compute returned an error: %v", err)
	}
	return result
}

// P1-ROUTE-010
//
// TestHealthyThreeSite checks every table of the three-site network with the
// direct link healthy, row by row against the reference table in the
// engineering plan (§10.4), plus the overlay and transit rows the plan leaves
// out for brevity.
func TestHealthyThreeSite(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())

	wantRows(t, result.Tables["aws-hub"],
		"10.42.0.0/20 via - over - metric 0 connected active",
		"10.42.240.1/32 via - over - metric 0 connected active",
		"10.42.16.0/20 via builder-1 over aws-hub~builder-1 metric 20 computed active",
		"10.42.240.2/32 via builder-1 over aws-hub~builder-1 metric 20 computed active",
		"10.42.32.0/20 via dgx-spark over aws-hub~dgx-spark metric 20 computed active",
		"10.42.240.3/32 via dgx-spark over aws-hub~dgx-spark metric 20 computed active",
	)
	wantRows(t, result.Tables["builder-1"],
		"10.42.16.0/20 via - over - metric 0 connected active",
		"10.42.240.2/32 via - over - metric 0 connected active",
		"10.42.252.0/30 via - over - metric 0 connected active",
		"10.42.0.0/20 via aws-hub over aws-hub~builder-1 metric 20 computed active",
		"10.42.240.1/32 via aws-hub over aws-hub~builder-1 metric 20 computed active",
		"10.42.32.0/20 via dgx-spark over builder-1~dgx-spark metric 10 computed active",
		"10.42.240.3/32 via dgx-spark over builder-1~dgx-spark metric 10 computed active",
		"10.42.32.0/20 via aws-hub over aws-hub~builder-1 metric 40 backup inactive",
		"10.42.240.3/32 via aws-hub over aws-hub~builder-1 metric 40 backup inactive",
	)
	wantRows(t, result.Tables["dgx-spark"],
		"10.42.32.0/20 via - over - metric 0 connected active",
		"10.42.240.3/32 via - over - metric 0 connected active",
		"10.42.252.0/30 via - over - metric 0 connected active",
		"10.42.0.0/20 via aws-hub over aws-hub~dgx-spark metric 20 computed active",
		"10.42.240.1/32 via aws-hub over aws-hub~dgx-spark metric 20 computed active",
		"10.42.16.0/20 via builder-1 over builder-1~dgx-spark metric 10 computed active",
		"10.42.240.2/32 via builder-1 over builder-1~dgx-spark metric 10 computed active",
		"10.42.16.0/20 via aws-hub over aws-hub~dgx-spark metric 40 backup inactive",
		"10.42.240.2/32 via aws-hub over aws-hub~dgx-spark metric 40 backup inactive",
	)
	if len(result.Unreachable) != 0 || len(result.Rejected) != 0 {
		t.Errorf("healthy network has unreachable pairs %v or rejections %v", result.Unreachable, result.Rejected)
	}
}

// P1-ROUTE-010
//
// TestReferenceTableElevenRows checks the eleven rows of the reference table
// in the plan exactly: the /20 rows only, across all three nodes.
func TestReferenceTableElevenRows(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	var got []string
	for _, node := range result.Tables.Nodes() {
		for _, r := range result.Tables[node].Routes {
			if r.Prefix.Bits() == 20 {
				got = append(got, fmt.Sprintf("%s | %s | %s | %s | %d | %s | %v", node, r.Prefix, r.NextHop, r.Via, r.Metric, r.Source, r.Active))
			}
		}
	}
	want := []string{
		"aws-hub | 10.42.0.0/20 |  |  | 0 | connected | true",
		"aws-hub | 10.42.16.0/20 | builder-1 | aws-hub~builder-1 | 20 | computed | true",
		"aws-hub | 10.42.32.0/20 | dgx-spark | aws-hub~dgx-spark | 20 | computed | true",
		"builder-1 | 10.42.0.0/20 | aws-hub | aws-hub~builder-1 | 20 | computed | true",
		"builder-1 | 10.42.16.0/20 |  |  | 0 | connected | true",
		"builder-1 | 10.42.32.0/20 | dgx-spark | builder-1~dgx-spark | 10 | computed | true",
		"builder-1 | 10.42.32.0/20 | aws-hub | aws-hub~builder-1 | 40 | backup | false",
		"dgx-spark | 10.42.0.0/20 | aws-hub | aws-hub~dgx-spark | 20 | computed | true",
		"dgx-spark | 10.42.16.0/20 | builder-1 | builder-1~dgx-spark | 10 | computed | true",
		"dgx-spark | 10.42.16.0/20 | aws-hub | aws-hub~dgx-spark | 40 | backup | false",
		"dgx-spark | 10.42.32.0/20 |  |  | 0 | connected | true",
	}
	if len(want) != 11 {
		t.Fatalf("the reference table has 11 rows, the test lists %d", len(want))
	}
	if !slices.Equal(got, want) {
		t.Errorf("the /20 rows differ\n got:\n  %s\nwant:\n  %s", join(got), join(want))
	}
}

// TestTablesAreSortedAndPathsRecorded checks that every table comes back in
// the order Compare defines, that connected routes record the node itself as
// their path, and that a computed route records the whole path.
func TestTablesAreSortedAndPathsRecorded(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	for _, node := range result.Tables.Nodes() {
		table := result.Tables[node]
		if !slices.IsSortedFunc(table.Routes, route.Compare) {
			t.Errorf("table of %s is not in Compare order", node)
		}
		if table.Node != node {
			t.Errorf("table stored under %s says it belongs to %s", node, table.Node)
		}
	}
	for _, r := range result.Tables["builder-1"].Routes {
		switch {
		case r.Source == route.SourceConnected && !slices.Equal(r.Path, []types.NodeID{"builder-1"}):
			t.Errorf("connected route %s has path %v, want [builder-1]", r.Prefix, r.Path)
		case r.Source == route.SourceBackup && r.Prefix.String() == "10.42.32.0/20" &&
			!slices.Equal(r.Path, []types.NodeID{"builder-1", "aws-hub", "dgx-spark"}):
			t.Errorf("backup to dgx-spark has path %v, want builder-1, aws-hub, dgx-spark", r.Path)
		}
	}
}

// P1-ROUTE-012
//
// TestHubAndSpokeOnly checks a network with no direct links: every spoke
// reaches every other spoke through the hub, at the sum of the two link
// costs, and has no backup, because the hub link is its only link.
func TestHubAndSpokeOnly(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(false), fixture.ThreeSiteInput())

	wantRows(t, result.Tables["builder-1"],
		"10.42.16.0/20 via - over - metric 0 connected active",
		"10.42.240.2/32 via - over - metric 0 connected active",
		"10.42.252.0/30 via - over - metric 0 connected active",
		"10.42.0.0/20 via aws-hub over aws-hub~builder-1 metric 20 computed active",
		"10.42.240.1/32 via aws-hub over aws-hub~builder-1 metric 20 computed active",
		"10.42.32.0/20 via aws-hub over aws-hub~builder-1 metric 40 computed active",
		"10.42.240.3/32 via aws-hub over aws-hub~builder-1 metric 40 computed active",
	)
	for _, c := range []struct {
		from types.NodeID
		dst  string
	}{{"builder-1", "10.42.33.7"}, {"dgx-spark", "10.42.16.9"}} {
		found, ok := result.Tables[c.from].Lookup(addrOf(c.dst))
		if !ok || found.NextHop != "aws-hub" || found.Metric != 40 {
			t.Errorf("%s to %s = %+v (found %v), want via aws-hub at metric 40", c.from, c.dst, found, ok)
		}
	}
}

// P1-ROUTE-016
//
// TestOverlayAddressFollowsItsNode checks that a node's overlay /32 is routed
// exactly like the node's site block: same next hop, same link, same metric.
func TestOverlayAddressFollowsItsNode(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	site, _ := result.Tables["builder-1"].Lookup(addrOf("10.42.33.7"))
	overlay, ok := result.Tables["builder-1"].Lookup(addrOf("10.42.240.3"))
	if !ok {
		t.Fatal("builder-1 has no route to 10.42.240.3")
	}
	if overlay.Prefix != prefixOf("10.42.240.3/32") {
		t.Errorf("10.42.240.3 matched %s, want its own /32", overlay.Prefix)
	}
	if overlay.NextHop != site.NextHop || overlay.Via != site.Via || overlay.Metric != site.Metric {
		t.Errorf("overlay route %s differs from the site route %s", rowOf(overlay), rowOf(site))
	}
	if overlay.NextHop != "dgx-spark" {
		t.Errorf("10.42.240.3 goes via %s, want dgx-spark", overlay.NextHop)
	}
}

// P1-ROUTE-017
//
// TestTransitEndsAreConnectedOnBothEnds checks that the /30 of the direct link
// is a connected route on both ends, and is never advertised: aws-hub, which is
// not on that link, has no route to it.
func TestTransitEndsAreConnectedOnBothEnds(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	for _, node := range []types.NodeID{"builder-1", "dgx-spark"} {
		found, ok := result.Tables[node].Lookup(addrOf("10.42.252.1"))
		if !ok || found.Source != route.SourceConnected || found.Prefix != prefixOf("10.42.252.0/30") {
			t.Errorf("%s: 10.42.252.1 = %+v (found %v), want a connected route for 10.42.252.0/30", node, found, ok)
		}
	}
	if found, ok := result.Tables["aws-hub"].Lookup(addrOf("10.42.252.1")); ok {
		t.Errorf("aws-hub has a route for the transit link it is not on: %s", rowOf(found))
	}
}

// TestSharedPrefixIsConnectedOnce checks that a prefix listed both as owned
// and as local on one node still gives one connected route.
func TestSharedPrefixIsConnectedOnce(t *testing.T) {
	input := route.Input{
		Owned: route.Owned{"a": {prefixOf("10.0.0.0/24")}},
		Local: route.Owned{"a": {prefixOf("10.0.0.0/24")}},
	}
	g := fixture.NewGraph().Hub("a")
	result := mustCompute(t, g, input)
	wantRows(t, result.Tables["a"], "10.0.0.0/24 via - over - metric 0 connected active")
}

// TestPrefixesAreCanonicalised checks that a prefix written with host bits set
// is stored in canonical form and sorted and de-duplicated with its twin.
func TestPrefixesAreCanonicalised(t *testing.T) {
	g := fixture.NewGraph().Hub("hub").Spoke("a")
	g.Link("hub", "a", 20)
	input := route.Input{Owned: route.Owned{
		"a": {prefixOf("10.1.0.5/24"), prefixOf("10.1.0.0/24"), prefixOf("10.1.0.9/24")},
	}}
	result := mustCompute(t, g, input)
	wantRows(t, result.Tables["hub"], "10.1.0.0/24 via a over a~hub metric 20 computed active")
	wantRows(t, result.Tables["a"], "10.1.0.0/24 via - over - metric 0 connected active")
}

// TestNodeAdvertisingNothingGetsNoRoutes checks that a node with no prefix is
// still reachable as a pair but produces no route.
func TestNodeAdvertisingNothingGetsNoRoutes(t *testing.T) {
	g := fixture.NewGraph().Hub("hub").Spoke("a")
	g.Link("hub", "a", 20)
	result := mustCompute(t, g, route.Input{})
	if len(result.Tables["hub"].Routes) != 0 || len(result.Tables["a"].Routes) != 0 {
		t.Errorf("tables are not empty: %v %v", result.Tables["hub"].Routes, result.Tables["a"].Routes)
	}
	if len(result.Unreachable) != 0 {
		t.Errorf("unreachable = %v, want none", result.Unreachable)
	}
}

// TestComputeEmptyGraph checks that a graph with no nodes gives no tables and
// no error.
func TestComputeEmptyGraph(t *testing.T) {
	result := mustCompute(t, fixture.NewGraph(), route.Input{})
	if len(result.Tables) != 0 || len(result.Unreachable) != 0 {
		t.Errorf("empty graph gave %d tables and %d unreachable pairs", len(result.Tables), len(result.Unreachable))
	}
}

// TestComputeRefusesBrokenInput checks that broken input is refused with a
// coded error, every problem at once, and no tables.
func TestComputeRefusesBrokenInput(t *testing.T) {
	cases := []struct {
		name  string
		build func() (route.Graph, route.Input)
		code  types.Code
	}{
		{"a prefix two nodes both advertise", func() (route.Graph, route.Input) {
			return fixture.ThreeSite(true), route.Input{Owned: route.Owned{
				"builder-1": {prefixOf("10.42.16.0/20")}, "dgx-spark": {prefixOf("10.42.16.0/20")}}}
		}, types.ErrOverlap},
		{"a local prefix another node advertises", func() (route.Graph, route.Input) {
			return fixture.ThreeSite(true), route.Input{
				Owned: route.Owned{"aws-hub": {prefixOf("10.42.252.0/30")}},
				Local: route.Owned{"builder-1": {prefixOf("10.42.252.0/30")}}}
		}, types.ErrOverlap},
		{"an IPv6 prefix", func() (route.Graph, route.Input) {
			return fixture.ThreeSite(true), route.Input{Owned: route.Owned{"aws-hub": {prefixOf("fd00::/64")}}}
		}, types.ErrInvalidPrefix},
		{"a zero prefix", func() (route.Graph, route.Input) {
			return fixture.ThreeSite(true), route.Input{Local: route.Owned{"aws-hub": {{}}}}
		}, types.ErrInvalidPrefix},
		{"an owner that is not a node", func() (route.Graph, route.Input) {
			return fixture.ThreeSite(true), route.Input{Owned: route.Owned{"ghost": {prefixOf("10.9.0.0/24")}}}
		}, types.ErrInvalidTopology},
		{"a local owner that is not a node", func() (route.Graph, route.Input) {
			return fixture.ThreeSite(true), route.Input{Local: route.Owned{"ghost": {prefixOf("10.9.0.0/30")}}}
		}, types.ErrInvalidTopology},
		{"a negative cost", func() (route.Graph, route.Input) {
			g := fixture.NewGraph().Hub("hub").Spoke("a")
			g.Link("hub", "a", -1)
			return g, route.Input{}
		}, types.ErrInvalidTopology},
		{"an edge to a node that does not exist", func() (route.Graph, route.Input) {
			g := fixture.NewGraph().Hub("hub")
			g.Link("hub", "ghost", 5)
			return g, route.Input{}
		}, types.ErrInvalidTopology},
	}
	for _, c := range cases {
		graph, input := c.build()
		result, err := route.Compute(graph, input)
		if !types.HasCode(err, c.code) {
			t.Errorf("%s: error = %v, want code %s", c.name, err, c.code)
		}
		if result.Tables != nil {
			t.Errorf("%s: tables returned alongside an error", c.name)
		}
	}
}

// TestComputeReportsEveryProblem checks that two separate problems are both
// reported, not just the first.
func TestComputeReportsEveryProblem(t *testing.T) {
	input := route.Input{
		Owned: route.Owned{
			"builder-1": {prefixOf("10.42.16.0/20")}, "dgx-spark": {prefixOf("10.42.16.0/20")},
			"aws-hub": {prefixOf("fd00::/64")},
		},
	}
	_, err := route.Compute(fixture.ThreeSite(true), input)
	if !types.HasCode(err, types.ErrOverlap) || !types.HasCode(err, types.ErrInvalidPrefix) {
		t.Errorf("error = %v, want both OVERLAP and INVALID_PREFIX", err)
	}
}

// P1-ROUTE-033
//
// TestComputeIsDeterministic checks that the same network gives identical
// tables whatever order its nodes, edges, prefixes and overrides come in: 50
// shuffled runs, compared field by field.
func TestComputeIsDeterministic(t *testing.T) {
	base := fixture.ThreeSite(true)
	input := fixture.ThreeSiteInput()
	input.Overrides = []route.Override{
		{Node: "dgx-spark", Prefix: prefixOf("10.42.240.1/32"), NextHop: "builder-1"},
		{Node: "builder-1", Prefix: prefixOf("10.50.0.0/16"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("10.50.0.0/16"), NextHop: "dgx-spark"},
		{Node: "builder-1", Prefix: prefixOf("10.51.0.0/16"), NextHop: "nowhere"},
	}
	want := mustCompute(t, base, input)
	rng := rand.New(rand.NewPCG(34, 1))
	for run := 0; run < 50; run++ {
		shuffled := input
		shuffled.Owned = route.Owned{}
		for node, prefixes := range input.Owned {
			copyOf := slices.Clone(prefixes)
			rng.Shuffle(len(copyOf), func(i, j int) { copyOf[i], copyOf[j] = copyOf[j], copyOf[i] })
			shuffled.Owned[node] = copyOf
		}
		shuffled.Overrides = slices.Clone(input.Overrides)
		rng.Shuffle(len(shuffled.Overrides), func(i, j int) {
			shuffled.Overrides[i], shuffled.Overrides[j] = shuffled.Overrides[j], shuffled.Overrides[i]
		})
		got := mustCompute(t, base.Shuffled(rng), shuffled)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: shuffled input gave a different result", run)
		}
	}
}

// TestComputeDoesNotChangeItsInput checks that Compute leaves the caller's
// prefixes and overrides as they were.
func TestComputeDoesNotChangeItsInput(t *testing.T) {
	input := fixture.ThreeSiteInput()
	input.Overrides = []route.Override{{Node: "builder-1", Prefix: prefixOf("10.50.0.9/16"), NextHop: "aws-hub"}}
	before := fixture.ThreeSiteInput()
	beforeOverrides := slices.Clone(input.Overrides)
	mustCompute(t, fixture.ThreeSite(true), input)
	if !reflect.DeepEqual(input.Owned, before.Owned) || !reflect.DeepEqual(input.Local, before.Local) ||
		!reflect.DeepEqual(input.Overrides, beforeOverrides) {
		t.Error("Compute changed its input")
	}
}

// TestRoutesDoNotShareMemory checks that two routes never share a Path, so
// changing one cannot change another.
func TestRoutesDoNotShareMemory(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(false), fixture.ThreeSiteInput())
	var changed *route.Route
	for i := range result.Tables["builder-1"].Routes {
		r := &result.Tables["builder-1"].Routes[i]
		if r.Source == route.SourceComputed && r.Prefix == prefixOf("10.42.32.0/20") {
			changed = r
		}
	}
	changed.Path[0] = "tampered"
	for _, r := range result.Tables["builder-1"].Routes {
		if r.Prefix == prefixOf("10.42.240.3/32") && r.Path[0] != "builder-1" {
			t.Errorf("changing one route's path changed %s", r.Prefix)
		}
	}
}

// P1-ROUTE-018, P1-ROUTE-019 (a first check; the full property tests arrive with
// V37)
//
// TestEveryRouteIsConsistentOnRandomNetworks builds 1,500 random networks and
// checks, for every node and every other node, that nothing is dropped
// silently and that every route is well formed. A pair is either routed or
// listed as unreachable, never both and never neither. Every active computed
// route has a path that starts at the node, ends at the prefix owner,
// visits no node twice and sums to the route's metric.
func TestEveryRouteIsConsistentOnRandomNetworks(t *testing.T) {
	rng := rand.New(rand.NewPCG(34, 2))
	for round := 0; round < 1500; round++ {
		g := randomGraph(rng)
		owned := route.Owned{}
		owner := map[string]types.NodeID{}
		for i, node := range g.Nodes() {
			owned[node] = []netip.Prefix{prefixOf(fmt.Sprintf("10.%d.0.0/24", i)), prefixOf(fmt.Sprintf("10.200.0.%d/32", i))}
			for _, p := range owned[node] {
				owner[p.String()] = node
			}
		}
		result := mustCompute(t, g, route.Input{Owned: owned})
		checkNetwork(t, round, g, owned, owner, result)
	}
}

// upEdge returns the working edge from one node to another, if there is one.
func upEdge(g route.Graph, from, to types.NodeID) (route.Edge, bool) {
	for _, edge := range g.Edges(from) {
		if edge.To == to && !edge.Down {
			return edge, true
		}
	}
	return route.Edge{}, false
}

// checkPath checks that a route's path is a real path in the graph: it starts
// at src, ends at last, visits no node twice, crosses only working links,
// passes only through hubs, begins with the route's next hop and link, and
// costs exactly the route's metric.
func checkPath(t *testing.T, round int, g route.Graph, src types.NodeID, r route.Route, last types.NodeID) {
	t.Helper()
	path := r.Path
	if len(path) < 2 || path[0] != src || path[len(path)-1] != last {
		t.Fatalf("round %d: %s on %s has path %v, want it to run from %s to %s", round, rowOf(r), src, path, src, last)
	}
	seen := map[types.NodeID]bool{}
	cost := 0
	for i, node := range path {
		if seen[node] {
			t.Fatalf("round %d: %s on %s visits %s twice: %v", round, rowOf(r), src, node, path)
		}
		seen[node] = true
		if i > 0 && i < len(path)-1 && g.Role(node) != types.RoleHub {
			t.Fatalf("round %d: %s on %s passes through %s, which is not a hub: %v", round, rowOf(r), src, node, path)
		}
		if i > 0 {
			edge, ok := upEdge(g, path[i-1], node)
			if !ok {
				t.Fatalf("round %d: %s on %s crosses %s to %s, which is not a working link", round, rowOf(r), src, path[i-1], node)
			}
			cost += edge.Cost
		}
	}
	if r.NextHop != path[1] || r.Via != types.LinkIDOf(path[0], path[1]) || r.Metric != cost {
		t.Fatalf("round %d: %s on %s disagrees with its path %v (next hop %s, link %s, cost %d)",
			round, rowOf(r), src, path, path[1], types.LinkIDOf(path[0], path[1]), cost)
	}
}

// checkNetwork checks a whole Result against the graph that produced it. For
// every ordered pair of nodes the pair is routed or listed as unreachable,
// never both and never neither, and reachability agrees with the brute-force
// oracle. Every route is well formed, each prefix has at most one active
// route, and every backup is inactive, leaves on a different first link than
// its primary and costs at least as much.
func checkNetwork(t *testing.T, round int, g *fixture.Graph, owned route.Owned, owner map[string]types.NodeID, result route.Result) {
	t.Helper()
	listed := map[[2]types.NodeID]route.Pair{}
	for i, pair := range result.Unreachable {
		key := [2]types.NodeID{pair.From, pair.To}
		if _, twice := listed[key]; twice {
			t.Fatalf("round %d: pair %v listed twice", round, key)
		}
		listed[key] = pair
		if i > 0 {
			previous := result.Unreachable[i-1]
			if previous.From > pair.From || (previous.From == pair.From && previous.To >= pair.To) {
				t.Fatalf("round %d: unreachable pairs are not sorted at %d", round, i)
			}
		}
	}
	for _, src := range g.Nodes() {
		table := result.Tables[src]
		if table == nil {
			t.Fatalf("round %d: no table for %s", round, src)
		}
		reach := bruteForce(g, src)
		for _, dst := range g.Nodes() {
			if dst == src {
				continue
			}
			pair, isListed := listed[[2]types.NodeID{src, dst}]
			_, reachable := reach[dst]
			if reachable == isListed {
				t.Fatalf("round %d: %s to %s: reachable=%v but listed unreachable=%v", round, src, dst, reachable, isListed)
			}
			if isListed {
				if pair.Reason == "" || !slices.Equal(pair.Prefixes, owned[dst]) {
					t.Fatalf("round %d: pair %s to %s has reason %q and prefixes %v", round, src, dst, pair.Reason, pair.Prefixes)
				}
				continue
			}
			for _, prefix := range owned[dst] {
				active := 0
				for _, r := range table.Routes {
					if r.Prefix == prefix && r.Active {
						active++
					}
				}
				if active != 1 {
					t.Fatalf("round %d: %s has %d active routes for %s, want 1", round, src, active, prefix)
				}
			}
		}
		primaries := map[netip.Prefix]route.Route{}
		for _, r := range table.Routes {
			if r.Source == route.SourceComputed {
				primaries[r.Prefix] = r
			}
		}
		for _, r := range table.Routes {
			switch r.Source {
			case route.SourceConnected:
				if owner[r.Prefix.String()] != src || !r.Active || r.Metric != 0 || !slices.Equal(r.Path, []types.NodeID{src}) {
					t.Fatalf("round %d: bad connected route %s on %s", round, rowOf(r), src)
				}
			case route.SourceComputed:
				if !r.Active {
					t.Fatalf("round %d: inactive computed route %s on %s", round, rowOf(r), src)
				}
				checkPath(t, round, g, src, r, owner[r.Prefix.String()])
			case route.SourceBackup:
				primary, ok := primaries[r.Prefix]
				if !ok || r.Active {
					t.Fatalf("round %d: backup %s on %s is active or has no primary", round, rowOf(r), src)
				}
				checkPath(t, round, g, src, r, owner[r.Prefix.String()])
				for i := 1; i < len(r.Path); i++ {
					if types.LinkIDOf(r.Path[i-1], r.Path[i]) == primary.Via {
						t.Fatalf("round %d: backup %s on %s uses the primary's first link %s", round, rowOf(r), src, primary.Via)
					}
				}
				if r.Metric < primary.Metric {
					t.Fatalf("round %d: backup %s on %s is cheaper than its primary %s", round, rowOf(r), src, rowOf(primary))
				}
			default:
				t.Fatalf("round %d: unexpected route %s on %s", round, rowOf(r), src)
			}
		}
	}
}
