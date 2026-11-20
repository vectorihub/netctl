package tests

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// P1-ROUTE-030
//
// TestDiffHealthyToFailedOver checks the diff between the healthy three-site
// tables and the tables with the direct link down. On builder-1 and dgx-spark,
// for each of the two prefixes behind the other spoke, exactly one route goes
// out of use (the direct primary, which is removed) and one comes into use
// (the hub backup, which is now the computed route). aws-hub is not listed,
// and nothing else changes.
func TestDiffHealthyToFailedOver(t *testing.T) {
	healthy := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	failed := mustCompute(t, fixture.ThreeSiteDirectDown(), fixture.ThreeSiteInput())
	diffs := route.Diff(healthy.Tables, failed.Tables)
	if len(diffs) != 2 || diffs[0].Node != "builder-1" || diffs[1].Node != "dgx-spark" {
		t.Fatalf("diff lists %d nodes %v, want builder-1 and dgx-spark only", len(diffs), diffs)
	}
	wants := map[types.NodeID][2]string{
		"builder-1": {"10.42.32.0/20", "10.42.240.3/32"},
		"dgx-spark": {"10.42.16.0/20", "10.42.240.2/32"},
	}
	for _, diff := range diffs {
		if len(diff.Added) != 0 {
			t.Errorf("%s: added %v, want none", diff.Node, diff.Added)
		}
		if len(diff.Removed) != 2 || len(diff.Changed) != 2 {
			t.Fatalf("%s: %d removed and %d changed, want 2 and 2", diff.Node, len(diff.Removed), len(diff.Changed))
		}
		for i, prefix := range wants[diff.Node] {
			gone, took := diff.Removed[i], diff.Changed[i]
			if gone.Prefix != prefixOf(prefix) || !gone.Active || gone.Source != route.SourceComputed || gone.Metric != 10 {
				t.Errorf("%s: removed %s, want the active direct primary for %s", diff.Node, rowOf(gone), prefix)
			}
			if !took.Activated() || took.Deactivated() || took.New.Prefix != prefixOf(prefix) ||
				took.Old.Source != route.SourceBackup || took.New.Source != route.SourceComputed || took.New.Metric != 40 {
				t.Errorf("%s: changed %s to %s, want the hub backup taking over", diff.Node, rowOf(took.Old), rowOf(took.New))
			}
		}
	}
}

// P1-ROUTE-031
//
// TestDiffOfIdenticalTablesIsEmpty checks that identical tables give an empty
// diff, whether they are the same object, two computations of the same input or
// the same routes in a different order.
func TestDiffOfIdenticalTablesIsEmpty(t *testing.T) {
	first := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	second := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	if diffs := route.Diff(first.Tables, first.Tables); len(diffs) != 0 {
		t.Errorf("a table against itself: %v", diffs)
	}
	if diffs := route.Diff(first.Tables, second.Tables); len(diffs) != 0 {
		t.Errorf("two computations of one input: %v", diffs)
	}
	rng := rand.New(rand.NewPCG(36, 2))
	for _, table := range second.Tables {
		rng.Shuffle(len(table.Routes), func(i, j int) { table.Routes[i], table.Routes[j] = table.Routes[j], table.Routes[i] })
	}
	if diffs := route.Diff(first.Tables, second.Tables); len(diffs) != 0 {
		t.Errorf("the same routes in another order: %v", diffs)
	}
	if diffs := route.Diff(nil, nil); len(diffs) != 0 {
		t.Errorf("nil against nil: %v", diffs)
	}
}

// TestDiffNodeAppearsOrDisappears checks that a node with a table on only one
// side has all its routes added or removed.
func TestDiffNodeAppearsOrDisappears(t *testing.T) {
	full := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	fewer := route.Tables{"aws-hub": full.Tables["aws-hub"], "builder-1": full.Tables["builder-1"]}
	removed := route.Diff(full.Tables, fewer)
	if len(removed) != 1 || removed[0].Node != "dgx-spark" || len(removed[0].Removed) != len(full.Tables["dgx-spark"].Routes) ||
		len(removed[0].Added) != 0 || len(removed[0].Changed) != 0 {
		t.Errorf("a node leaving: %+v", removed)
	}
	added := route.Diff(fewer, full.Tables)
	if len(added) != 1 || added[0].Node != "dgx-spark" || len(added[0].Added) != len(full.Tables["dgx-spark"].Routes) ||
		len(added[0].Removed) != 0 {
		t.Errorf("a node joining: %+v", added)
	}
	withNil := route.Tables{"aws-hub": full.Tables["aws-hub"], "builder-1": nil, "dgx-spark": full.Tables["dgx-spark"]}
	if diffs := route.Diff(full.Tables, withNil); len(diffs) != 1 || diffs[0].Node != "builder-1" || len(diffs[0].Removed) == 0 {
		t.Errorf("a nil table counts as no routes: %+v", diffs)
	}
}

// TestDiffNoticesEveryField checks that a change to the metric, source, path or
// active flag of a route is a Changed, and that a change to the prefix, next
// hop or link is a remove plus an add, since those identify the route.
func TestDiffNoticesEveryField(t *testing.T) {
	base := computed("10.1.0.0/24", "hub", "hub~a", 20)
	base.Path = []types.NodeID{"a", "hub", "b"}
	table := func(r route.Route) route.Tables { return route.Tables{"a": {Node: "a", Routes: []route.Route{r}}} }

	for name, change := range map[string]func(*route.Route){
		"metric": func(r *route.Route) { r.Metric = 30 },
		"source": func(r *route.Route) { r.Source = route.SourceStatic },
		"path":   func(r *route.Route) { r.Path = []types.NodeID{"a", "hub", "c"} },
		"active": func(r *route.Route) { r.Active = false },
	} {
		changed := with(base, change)
		diffs := route.Diff(table(base), table(changed))
		if len(diffs) != 1 || len(diffs[0].Changed) != 1 || len(diffs[0].Added) != 0 || len(diffs[0].Removed) != 0 {
			t.Errorf("%s: %+v, want exactly one changed route", name, diffs)
		}
	}
	for name, change := range map[string]func(*route.Route){
		"prefix":   func(r *route.Route) { r.Prefix = prefixOf("10.2.0.0/24") },
		"next hop": func(r *route.Route) { r.NextHop = "hub2" },
		"link":     func(r *route.Route) { r.Via = "hub~z" },
	} {
		moved := with(base, change)
		diffs := route.Diff(table(base), table(moved))
		if len(diffs) != 1 || len(diffs[0].Changed) != 0 || len(diffs[0].Added) != 1 || len(diffs[0].Removed) != 1 {
			t.Errorf("%s: %+v, want one added and one removed", name, diffs)
		}
	}
}

// TestDiffActivatedAndDeactivated checks the two direction helpers on all four
// combinations of old and new activity.
func TestDiffActivatedAndDeactivated(t *testing.T) {
	for _, c := range []struct {
		old, now               bool
		activated, deactivated bool
	}{
		{false, true, true, false},
		{true, false, false, true},
		{true, true, false, false},
		{false, false, false, false},
	} {
		change := route.RouteChange{Old: route.Route{Active: c.old}, New: route.Route{Active: c.now}}
		if change.Activated() != c.activated || change.Deactivated() != c.deactivated {
			t.Errorf("old %v new %v: activated %v deactivated %v", c.old, c.now, change.Activated(), change.Deactivated())
		}
	}
}

// TestDiffHandlesRepeatedKeys checks that two routes with the same prefix, next
// hop and link are told apart rather than merged, and that the answer does not
// depend on their order.
func TestDiffHandlesRepeatedKeys(t *testing.T) {
	first := computed("10.1.0.0/24", "hub", "hub~a", 20)
	second := with(first, func(r *route.Route) { r.Metric = 30 })
	old := route.Tables{"a": {Node: "a", Routes: []route.Route{first, second}}}
	same := route.Tables{"a": {Node: "a", Routes: []route.Route{second, first}}}
	if diffs := route.Diff(old, same); len(diffs) != 0 {
		t.Errorf("same two routes in another order: %+v", diffs)
	}
	one := route.Tables{"a": {Node: "a", Routes: []route.Route{first}}}
	diffs := route.Diff(old, one)
	if len(diffs) != 1 || len(diffs[0].Removed) != 1 || diffs[0].Removed[0].Metric != 30 {
		t.Errorf("losing the second of two: %+v", diffs)
	}
}

// TestDiffIsDeterministicAndKeepsItsInput checks that the diff is sorted, is the
// same for shuffled input and for repeated calls, and leaves both inputs alone.
func TestDiffIsDeterministicAndKeepsItsInput(t *testing.T) {
	healthy := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	failed := mustCompute(t, fixture.ThreeSiteDirectDown(), fixture.ThreeSiteInput())
	want := route.Diff(healthy.Tables, failed.Tables)
	rng := rand.New(rand.NewPCG(36, 3))
	for run := 0; run < 30; run++ {
		a := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
		b := mustCompute(t, fixture.ThreeSiteDirectDown(), fixture.ThreeSiteInput())
		for _, tables := range []route.Tables{a.Tables, b.Tables} {
			for _, table := range tables {
				rng.Shuffle(len(table.Routes), func(i, j int) { table.Routes[i], table.Routes[j] = table.Routes[j], table.Routes[i] })
			}
		}
		if got := route.Diff(a.Tables, b.Tables); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: shuffled tables gave a different diff", run)
		}
	}
	for _, diff := range want {
		if !slices.IsSortedFunc(diff.Added, route.Compare) || !slices.IsSortedFunc(diff.Removed, route.Compare) {
			t.Errorf("%s: diff lists are not sorted", diff.Node)
		}
	}
	untouched := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	if !reflect.DeepEqual(healthy, untouched) {
		t.Error("Diff changed the tables it was given")
	}
}

// applyDiff replays a diff on a list of routes: it takes out the removed routes,
// swaps each changed route for its new form and adds the added ones, and
// returns the result sorted. It is written separately from Diff so the two
// check each other.
func applyDiff(routes []route.Route, diff route.TableDiff) []route.Route {
	out := slices.Clone(routes)
	find := func(want route.Route) int {
		return slices.IndexFunc(out, func(r route.Route) bool { return route.Compare(r, want) == 0 })
	}
	for _, gone := range diff.Removed {
		if at := find(gone); at >= 0 {
			out = slices.Delete(out, at, at+1)
		}
	}
	for _, change := range diff.Changed {
		if at := find(change.Old); at >= 0 {
			out[at] = change.New
		}
	}
	out = append(out, diff.Added...)
	slices.SortFunc(out, route.Compare)
	return out
}

// TestDiffAppliedToOldGivesNew checks, on 20,000 pairs of random tables, that
// the diff is complete and exact: replaying it on the old routes gives
// precisely the new routes. The random tables contain repeated and inactive
// routes, so the awkward cases come up often.
func TestDiffAppliedToOldGivesNew(t *testing.T) {
	rng := rand.New(rand.NewPCG(36, 4))
	for round := 0; round < 20000; round++ {
		before, after := randomTable(rng), randomTable(rng)
		diffs := route.Diff(route.Tables{"x": before}, route.Tables{"x": after})
		want := sortedRoutes(after.Routes)
		got := sortedRoutes(before.Routes)
		if len(diffs) == 1 {
			got = applyDiff(before.Routes, diffs[0])
		} else if len(diffs) > 1 {
			t.Fatalf("round %d: %d diffs for one node", round, len(diffs))
		}
		if len(got) != len(want) {
			t.Fatalf("round %d: replaying the diff gives %d routes, want %d", round, len(got), len(want))
		}
		for i := range got {
			if route.Compare(got[i], want[i]) != 0 {
				t.Fatalf("round %d: route %d is %s, want %s", round, i, rowOf(got[i]), rowOf(want[i]))
			}
		}
	}
}

// sortedRoutes returns a sorted copy of routes.
func sortedRoutes(routes []route.Route) []route.Route {
	out := slices.Clone(routes)
	slices.SortFunc(out, route.Compare)
	return out
}
