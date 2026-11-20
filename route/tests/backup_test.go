package tests

import (
	"slices"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// P1-ROUTE-011
//
// TestDirectLinkDown checks the failed-over network: with the direct link
// down, builder-1 and dgx-spark reach each other through aws-hub at metric
// 40, and there is no backup, because there is no second path left. The
// network is tried both with the direct link marked down and with it absent.
func TestDirectLinkDown(t *testing.T) {
	for name, g := range map[string]*fixture.Graph{
		"marked down": fixture.ThreeSiteDirectDown(),
		"absent":      fixture.ThreeSite(false),
	} {
		result := mustCompute(t, g, fixture.ThreeSiteInput())
		for _, c := range []struct {
			from types.NodeID
			dst  string
		}{{"builder-1", "10.42.33.7"}, {"dgx-spark", "10.42.17.5"}} {
			found, ok := result.Tables[c.from].Lookup(addrOf(c.dst))
			if !ok || found.NextHop != "aws-hub" || found.Metric != 40 || found.Source != route.SourceComputed {
				t.Errorf("%s: %s to %s = %s (found %v), want a computed route via aws-hub at metric 40", name, c.from, c.dst, rowOf(found), ok)
			}
		}
		for _, node := range []types.NodeID{"builder-1", "dgx-spark"} {
			for _, r := range result.Tables[node].Routes {
				if r.Source == route.SourceBackup || !r.Active {
					t.Errorf("%s: %s has a backup or inactive route: %s", name, node, rowOf(r))
				}
			}
		}
	}
}

// TestBackupIsInactiveUntilNeeded checks that a backup is in the table but not
// in use: it is in the RIB, not in the FIB, and Lookup never returns it while
// its primary is healthy.
func TestBackupIsInactiveUntilNeeded(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	table := result.Tables["builder-1"]
	backups := 0
	for _, r := range table.Routes {
		if r.Source == route.SourceBackup {
			backups++
			if r.Active {
				t.Errorf("backup %s is active", rowOf(r))
			}
		}
	}
	if backups != 2 {
		t.Errorf("builder-1 has %d backups, want 2 (dgx-spark's block and overlay address)", backups)
	}
	for _, r := range table.FIB() {
		if r.Source == route.SourceBackup {
			t.Errorf("the FIB holds a backup: %s", rowOf(r))
		}
	}
	found, _ := table.Lookup(addrOf("10.42.33.7"))
	if found.Source != route.SourceComputed || found.NextHop != "dgx-spark" {
		t.Errorf("Lookup returned %s, want the computed route via dgx-spark", rowOf(found))
	}
}

// TestBackupAvoidsThePrimaryLink checks that the backup leaves on a different
// link from the primary and shares no link with it. With two hubs, a spoke
// reaches another spoke through the smaller hub and keeps the other hub as the
// backup.
func TestBackupAvoidsThePrimaryLink(t *testing.T) {
	g := fixture.NewGraph().Hub("hub-a", "hub-b").Spoke("s1", "s2")
	g.Link("hub-a", "s1", 20)
	g.Link("hub-a", "s2", 20)
	g.Link("hub-b", "s1", 30)
	g.Link("hub-b", "s2", 30)
	input := route.Input{Owned: route.Owned{"s2": {prefixOf("10.2.0.0/24")}}}
	result := mustCompute(t, g, input)
	wantRows(t, result.Tables["s1"],
		"10.2.0.0/24 via hub-a over hub-a~s1 metric 40 computed active",
		"10.2.0.0/24 via hub-b over hub-b~s1 metric 60 backup inactive",
	)
	for _, r := range result.Tables["s1"].Routes {
		if r.Source == route.SourceBackup && !slices.Equal(r.Path, []types.NodeID{"s1", "hub-b", "s2"}) {
			t.Errorf("backup path = %v, want s1, hub-b, s2", r.Path)
		}
	}
}

// TestOneBackupPerPrefix checks that three possible paths still give one
// backup: the best alternative to the primary, not every alternative.
func TestOneBackupPerPrefix(t *testing.T) {
	g := fixture.NewGraph().Hub("hub-a", "hub-b", "hub-c").Spoke("s1", "s2")
	for i, hub := range []types.NodeID{"hub-a", "hub-b", "hub-c"} {
		g.Link(hub, "s1", 10*(i+1))
		g.Link(hub, "s2", 10*(i+1))
	}
	result := mustCompute(t, g, route.Input{Owned: route.Owned{"s2": {prefixOf("10.2.0.0/24")}}})
	wantRows(t, result.Tables["s1"],
		"10.2.0.0/24 via hub-a over hub-a~s1 metric 20 computed active",
		"10.2.0.0/24 via hub-b over hub-b~s1 metric 40 backup inactive",
	)
}

// TestHubBackupCanUseASecondHub checks that a hub gets a backup too when
// another hub offers a way round.
func TestHubBackupCanUseASecondHub(t *testing.T) {
	g := fixture.NewGraph().Hub("hub-a", "hub-b").Spoke("s1")
	g.Link("hub-a", "s1", 10)
	g.Link("hub-b", "s1", 10)
	g.Link("hub-a", "hub-b", 5)
	result := mustCompute(t, g, route.Input{Owned: route.Owned{"s1": {prefixOf("10.1.0.0/24")}}})
	wantRows(t, result.Tables["hub-a"],
		"10.1.0.0/24 via s1 over hub-a~s1 metric 10 computed active",
		"10.1.0.0/24 via hub-b over hub-a~hub-b metric 15 backup inactive",
	)
}

// P1-ROUTE-015
//
// TestUnreachablePairIsReported checks that a node with no working link is
// listed as unreachable from every other node, and the other way round, with
// the prefixes that have no route and a reason, and that no route is emitted.
func TestUnreachablePairIsReported(t *testing.T) {
	g := fixture.ThreeSite(true)
	g.Down(types.LinkIDOf("aws-hub", "dgx-spark")).Down(types.LinkIDOf("builder-1", "dgx-spark"))
	result := mustCompute(t, g, fixture.ThreeSiteInput())

	var got []string
	for _, pair := range result.Unreachable {
		got = append(got, string(pair.From)+" > "+string(pair.To)+" : "+string(pair.Reason))
	}
	want := []string{
		"aws-hub > dgx-spark : no healthy path",
		"builder-1 > dgx-spark : no healthy path",
		"dgx-spark > aws-hub : no healthy path",
		"dgx-spark > builder-1 : no healthy path",
	}
	if !slices.Equal(got, want) {
		t.Errorf("unreachable pairs\n got: %v\nwant: %v", got, want)
	}
	if pair := result.Unreachable[0]; !slices.Equal(pair.Prefixes, fixture.ThreeSiteOwned()["dgx-spark"]) {
		t.Errorf("the pair lists prefixes %v, want dgx-spark's two", pair.Prefixes)
	}
	if _, ok := result.Tables["aws-hub"].Lookup(addrOf("10.42.33.7")); ok {
		t.Error("aws-hub has a route to the block of an unreachable node")
	}
	if _, ok := result.Tables["dgx-spark"].Lookup(addrOf("10.42.0.1")); ok {
		t.Error("dgx-spark has a route to a block it cannot reach")
	}
	if found, ok := result.Tables["dgx-spark"].Lookup(addrOf("10.42.32.9")); !ok || found.Source != route.SourceConnected {
		t.Error("an isolated node lost its own connected route")
	}
}

// TestUnreachableReasonNamesTheSpokeRule checks that the reason tells apart a
// node that has no path at all from one whose only path crosses a spoke. In
// the chain a, b, c of spokes, a cannot reach c because b would have to
// forward; d has no link, so there is simply no path.
func TestUnreachableReasonNamesTheSpokeRule(t *testing.T) {
	g := fixture.NewGraph().Spoke("a", "b", "c", "d")
	g.Link("a", "b", 10)
	g.Link("b", "c", 10)
	result := mustCompute(t, g, route.Input{})
	reasons := map[[2]types.NodeID]route.Reason{}
	for _, pair := range result.Unreachable {
		reasons[[2]types.NodeID{pair.From, pair.To}] = pair.Reason
	}
	if reasons[[2]types.NodeID{"a", "c"}] != route.ReasonSpokeTransit {
		t.Errorf("a to c: %q, want the spoke-transit reason", reasons[[2]types.NodeID{"a", "c"}])
	}
	if reasons[[2]types.NodeID{"c", "a"}] != route.ReasonSpokeTransit {
		t.Errorf("c to a: %q, want the spoke-transit reason", reasons[[2]types.NodeID{"c", "a"}])
	}
	if reasons[[2]types.NodeID{"a", "d"}] != route.ReasonNoPath {
		t.Errorf("a to d: %q, want the no-path reason", reasons[[2]types.NodeID{"a", "d"}])
	}
	if _, listed := reasons[[2]types.NodeID{"a", "b"}]; listed {
		t.Error("a can reach b, but the pair is listed as unreachable")
	}
	if len(result.Unreachable) != 8 {
		t.Errorf("%d unreachable pairs, want 8: a-c, c-a, and d with each of a, b, c both ways", len(result.Unreachable))
	}
}

// P1-ROUTE-020
//
// TestOverrideToANonNeighbourIsRejected checks that an override pointing at a
// node that is not a neighbour is not accepted, and comes back with an
// explanation that names the node and the next hop. builder-1 is joined only
// to aws-hub and dgx-spark, so "nowhere" is no neighbour; neither is a
// neighbour of itself.
func TestOverrideToANonNeighbourIsRejected(t *testing.T) {
	g := fixture.ThreeSite(false)
	result := mustCompute(t, g, route.Input{Owned: fixture.ThreeSiteOwned(), Overrides: []route.Override{
		{Node: "builder-1", Prefix: prefixOf("10.50.0.0/16"), NextHop: "dgx-spark"},
		{Node: "builder-1", Prefix: prefixOf("10.51.0.0/16"), NextHop: "nowhere"},
		{Node: "builder-1", Prefix: prefixOf("10.52.0.0/16"), NextHop: "builder-1"},
	}})
	if len(result.Rejected) != 3 {
		t.Fatalf("%d overrides rejected, want 3 (no link builder-1 to dgx-spark exists): %v", len(result.Rejected), result.Rejected)
	}
	for _, rejection := range result.Rejected {
		if rejection.Reason != route.RejectNotNeighbour {
			t.Errorf("override %v rejected for %q, want %q", rejection.Override, rejection.Reason, route.RejectNotNeighbour)
		}
		if rejection.Detail == "" {
			t.Errorf("override %v was rejected without an explanation", rejection.Override)
		}
	}
	if want := `builder-1 has no link to "nowhere", so it cannot send 10.51.0.0/16 there`; result.Rejected[1].Detail != want {
		t.Errorf("explanation = %q, want %q", result.Rejected[1].Detail, want)
	}
	for _, r := range result.Tables["builder-1"].Routes {
		if r.Source == route.SourceStatic {
			t.Errorf("a rejected override left a static route: %s", rowOf(r))
		}
	}
}

// TestOverrideIsAcceptedAndFlagged checks that an override to a real neighbour
// becomes an active route whose source is static, with the link and cost of
// the link it leaves on, or the metric it asks for.
func TestOverrideIsAcceptedAndFlagged(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), route.Input{Owned: fixture.ThreeSiteOwned(), Overrides: []route.Override{
		{Node: "builder-1", Prefix: prefixOf("192.168.0.0/16"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("172.16.0.0/12"), NextHop: "dgx-spark", Metric: 99},
	}})
	if len(result.Rejected) != 0 {
		t.Fatalf("rejected %v", result.Rejected)
	}
	var statics []string
	for _, r := range result.Tables["builder-1"].Routes {
		if r.Source == route.SourceStatic {
			statics = append(statics, rowOf(r))
			if !slices.Equal(r.Path, []types.NodeID{"builder-1", r.NextHop}) {
				t.Errorf("static route %s has path %v", r.Prefix, r.Path)
			}
		}
	}
	want := []string{
		"172.16.0.0/12 via dgx-spark over builder-1~dgx-spark metric 99 static active",
		"192.168.0.0/16 via aws-hub over aws-hub~builder-1 metric 20 static active",
	}
	if !slices.Equal(statics, want) {
		t.Errorf("static routes\n got: %v\nwant: %v", statics, want)
	}
}

// TestHostOverrideBeatsTheBlock checks the plan's example: a /32 override
// beats the /20 that contains it, and does not disturb the rest of the block.
func TestHostOverrideBeatsTheBlock(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), route.Input{Owned: fixture.ThreeSiteOwned(), Overrides: []route.Override{
		{Node: "builder-1", Prefix: prefixOf("10.42.32.7/32"), NextHop: "aws-hub"},
	}})
	table := result.Tables["builder-1"]
	if found, _ := table.Lookup(addrOf("10.42.32.7")); found.Source != route.SourceStatic || found.NextHop != "aws-hub" {
		t.Errorf("10.42.32.7 = %s, want the static route via aws-hub", rowOf(found))
	}
	if found, _ := table.Lookup(addrOf("10.42.32.8")); found.Source != route.SourceComputed || found.NextHop != "dgx-spark" {
		t.Errorf("10.42.32.8 = %s, want the computed route via dgx-spark", rowOf(found))
	}
}

// TestOverrideReplacesTheComputedRouteForTheSamePrefix checks that an override
// for a prefix the engine also routes takes over: the computed route stays in
// the table but inactive, so there is never more than one active route for a
// prefix. This is the fixture of the failover-rejected scenario: an override
// that equals the computed route is harmless while the link is healthy.
func TestOverrideReplacesTheComputedRouteForTheSamePrefix(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), route.Input{Owned: fixture.ThreeSiteOwned(), Overrides: []route.Override{
		{Node: "dgx-spark", Prefix: prefixOf("10.42.16.0/20"), NextHop: "builder-1"},
	}})
	var rows []string
	for _, r := range result.Tables["dgx-spark"].Routes {
		if r.Prefix == prefixOf("10.42.16.0/20") {
			rows = append(rows, rowOf(r))
		}
	}
	want := []string{
		"10.42.16.0/20 via aws-hub over aws-hub~dgx-spark metric 40 backup inactive",
		"10.42.16.0/20 via builder-1 over builder-1~dgx-spark metric 10 computed inactive",
		"10.42.16.0/20 via builder-1 over builder-1~dgx-spark metric 10 static active",
	}
	slices.Sort(rows)
	if !slices.Equal(rows, want) {
		t.Errorf("routes for 10.42.16.0/20\n got: %v\nwant: %v", rows, want)
	}
}

// TestOverrideDoesNotFollowLinkHealth checks that an override whose link is
// down is still accepted and stays active. The planner relies on that to
// refuse a failover that would leave the override pointing at a dead link
// (V-ROUTE-002). Compute must not quietly heal it.
func TestOverrideDoesNotFollowLinkHealth(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSiteDirectDown(), route.Input{Owned: fixture.ThreeSiteOwned(), Overrides: []route.Override{
		{Node: "dgx-spark", Prefix: prefixOf("10.42.16.0/20"), NextHop: "builder-1"},
	}})
	if len(result.Rejected) != 0 {
		t.Fatalf("an override over a down link was rejected: %v", result.Rejected)
	}
	found, ok := result.Tables["dgx-spark"].Lookup(addrOf("10.42.16.9"))
	if !ok || found.Source != route.SourceStatic || found.NextHop != "builder-1" || !found.Active {
		t.Errorf("10.42.16.9 = %s, want the static route to stay active over the down link", rowOf(found))
	}
}

// TestOverrideLinkChoice checks which link an override leaves on: the link
// it names, or else a working one before a down one.
func TestOverrideLinkChoice(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), route.Input{Owned: fixture.ThreeSiteOwned(), Overrides: []route.Override{
		{Node: "builder-1", Prefix: prefixOf("10.60.0.0/16"), NextHop: "aws-hub", Link: "aws-hub~builder-1"},
		{Node: "builder-1", Prefix: prefixOf("10.61.0.0/16"), NextHop: "aws-hub", Link: "aws-hub~dgx-spark"},
		{Node: "builder-1", Prefix: prefixOf("10.62.0.0/16"), NextHop: "aws-hub", Link: "nonsense"},
	}})
	if len(result.Rejected) != 2 {
		t.Fatalf("%d rejected, want 2: %v", len(result.Rejected), result.Rejected)
	}
	for _, rejection := range result.Rejected {
		if rejection.Reason != route.RejectWrongLink {
			t.Errorf("override %v rejected for %q, want %q", rejection.Override, rejection.Reason, route.RejectWrongLink)
		}
	}
	found, ok := result.Tables["builder-1"].Lookup(addrOf("10.60.1.1"))
	if !ok || found.Via != "aws-hub~builder-1" {
		t.Errorf("10.60.1.1 = %s, want it on aws-hub~builder-1", rowOf(found))
	}
}

// TestOverridePrefersAWorkingLink checks that with two links to the same
// neighbour, one down, an override that names neither leaves on the working
// one even though the down one has the smaller ID.
func TestOverridePrefersAWorkingLink(t *testing.T) {
	g := &twoLinkGraph{}
	result := mustCompute(t, g, route.Input{Overrides: []route.Override{
		{Node: "a", Prefix: prefixOf("10.9.0.0/24"), NextHop: "b"},
	}})
	found, ok := result.Tables["a"].Lookup(addrOf("10.9.0.1"))
	if !ok || found.Via != "link-up" || found.Metric != 7 {
		t.Errorf("10.9.0.1 = %s, want it on link-up at metric 7", rowOf(found))
	}
}

// twoLinkGraph is a graph of two nodes joined by two links: "link-down", which
// is down and cheap, and "link-up", which is up and dearer.
type twoLinkGraph struct{}

// Nodes returns the two nodes.
func (twoLinkGraph) Nodes() []types.NodeID { return []types.NodeID{"b", "a"} }

// Role returns hub for both.
func (twoLinkGraph) Role(types.NodeID) types.Role { return types.RoleHub }

// Edges returns both links, from either end.
func (twoLinkGraph) Edges(node types.NodeID) []route.Edge {
	other := types.NodeID("b")
	if node == "b" {
		other = "a"
	}
	return []route.Edge{
		{Link: "link-up", To: other, Cost: 7},
		{Link: "link-down", To: other, Cost: 1, Down: true},
	}
}

// TestOverrideRejectionReasons checks every other reason an override can be
// refused for, one case each, and that the rest of the batch is unaffected.
func TestOverrideRejectionReasons(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), route.Input{Owned: fixture.ThreeSiteOwned(), Local: fixture.ThreeSiteLocal(), Overrides: []route.Override{
		{Node: "ghost", Prefix: prefixOf("10.70.0.0/16"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("fd00::/64"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("10.71.0.0/16"), NextHop: "aws-hub", Metric: -3},
		{Node: "builder-1", Prefix: prefixOf("10.42.16.0/20"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("10.42.252.0/30"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("10.72.0.0/16"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("10.72.0.9/16"), NextHop: "dgx-spark"},
		{Node: "builder-1", Prefix: prefixOf("10.73.0.0/16"), NextHop: "aws-hub"},
	}})
	got := map[string]route.RejectReason{}
	for _, rejection := range result.Rejected {
		got[string(rejection.Override.Node)+" "+rejection.Override.Prefix.String()] = rejection.Reason
		if rejection.Detail == "" {
			t.Errorf("override %v was rejected without an explanation", rejection.Override)
		}
	}
	want := map[string]route.RejectReason{
		"ghost 10.70.0.0/16":       route.RejectUnknownNode,
		"builder-1 fd00::/64":      route.RejectBadPrefix,
		"builder-1 10.71.0.0/16":   route.RejectBadMetric,
		"builder-1 10.42.16.0/20":  route.RejectConnected,
		"builder-1 10.42.252.0/30": route.RejectConnected,
		"builder-1 10.72.0.9/16":   route.RejectDuplicate,
	}
	if len(got) != len(want) {
		t.Errorf("rejections\n got: %v\nwant: %v", got, want)
	}
	for key, reason := range want {
		if got[key] != reason {
			t.Errorf("%s: rejected for %q, want %q", key, got[key], reason)
		}
	}
	var statics []string
	for _, r := range result.Tables["builder-1"].Routes {
		if r.Source == route.SourceStatic {
			statics = append(statics, r.Prefix.String()+">"+string(r.NextHop))
		}
	}
	if wantStatics := []string{"10.72.0.0/16>aws-hub", "10.73.0.0/16>aws-hub"}; !slices.Equal(statics, wantStatics) {
		t.Errorf("accepted statics = %v, want %v (the first duplicate by sort order wins)", statics, wantStatics)
	}
}

// TestRejectedAndAcceptedTogether checks that no override is lost: accepted
// plus rejected is everything given.
func TestRejectedAndAcceptedTogether(t *testing.T) {
	overrides := []route.Override{
		{Node: "builder-1", Prefix: prefixOf("10.80.0.0/16"), NextHop: "aws-hub"},
		{Node: "builder-1", Prefix: prefixOf("10.81.0.0/16"), NextHop: "dgx-spark"},
		{Node: "dgx-spark", Prefix: prefixOf("10.82.0.0/16"), NextHop: "ghost"},
		{Node: "aws-hub", Prefix: prefixOf("10.83.0.0/16"), NextHop: "builder-1"},
	}
	result := mustCompute(t, fixture.ThreeSite(true), route.Input{Owned: fixture.ThreeSiteOwned(), Overrides: overrides})
	accepted := 0
	for _, node := range result.Tables.Nodes() {
		for _, r := range result.Tables[node].Routes {
			if r.Source == route.SourceStatic {
				accepted++
			}
		}
	}
	if accepted+len(result.Rejected) != len(overrides) {
		t.Errorf("%d accepted and %d rejected, want %d in all", accepted, len(result.Rejected), len(overrides))
	}
}
