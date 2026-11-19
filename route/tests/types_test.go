package tests

import (
	"encoding/json"
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// prefixOf parses a prefix, panicking on bad text because test data is fixed.
var prefixOf = netip.MustParsePrefix

// addrOf parses an address, panicking on bad text because test data is fixed.
var addrOf = netip.MustParseAddr

// computed builds an active computed route for a prefix, with a next hop, a
// link and a metric. Tests that need other fields change them on the result.
func computed(prefix string, nextHop types.NodeID, via types.LinkID, metric int) route.Route {
	return route.Route{
		Prefix:  prefixOf(prefix),
		NextHop: nextHop,
		Via:     via,
		Metric:  metric,
		Source:  route.SourceComputed,
		Active:  true,
	}
}

// connected builds an active connected route for a prefix: it belongs to this
// node, so it has no next hop and no link.
func connected(prefix string) route.Route {
	return route.Route{Prefix: prefixOf(prefix), Source: route.SourceConnected, Active: true}
}

// routesOf collects the routes of a table as a plain slice, for comparisons.
func routesOf(table *route.Table) []route.Route { return slices.Clone(table.Routes) }

// threeSiteBuilder returns builder-1's table from the reference three-site
// network, with the direct link to dgx-spark healthy. Its routes are
// deliberately out of order so tests of Sort have something to do.
func threeSiteBuilder() *route.Table {
	backup := computed("10.42.32.0/20", "aws-hub", "aws-hub~builder-1", 40)
	backup.Source = route.SourceBackup
	backup.Active = false
	backup.Path = []types.NodeID{"builder-1", "aws-hub", "dgx-spark"}

	primary := computed("10.42.32.0/20", "dgx-spark", "builder-1~dgx-spark", 10)
	primary.Path = []types.NodeID{"builder-1", "dgx-spark"}

	toHub := computed("10.42.0.0/20", "aws-hub", "aws-hub~builder-1", 20)
	toHub.Path = []types.NodeID{"builder-1", "aws-hub"}

	return &route.Table{
		Node:   "builder-1",
		Routes: []route.Route{backup, primary, connected("10.42.16.0/20"), toHub},
	}
}

// P1-ROUTE-SORT-001
//
// TestSortOrder checks the order the reference table must have: by prefix, and
// for the same prefix by metric, so the active metric-10 route comes before the
// inactive metric-40 backup.
func TestSortOrder(t *testing.T) {
	table := threeSiteBuilder()
	table.Sort()

	var got []string
	for _, r := range table.Routes {
		got = append(got, r.Prefix.String()+" "+r.Source.String())
	}
	want := []string{
		"10.42.0.0/20 computed",
		"10.42.16.0/20 connected",
		"10.42.32.0/20 computed",
		"10.42.32.0/20 backup",
	}
	if !slices.Equal(got, want) {
		t.Errorf("sorted order:\n got %v\nwant %v", got, want)
	}
}

// P1-ROUTE-SORT-002
//
// TestSortDeterministic checks that sorting is total and deterministic: fifty
// shuffles of the same routes all sort to the identical sequence.
func TestSortDeterministic(t *testing.T) {
	reference := threeSiteBuilder()
	reference.Sort()
	want := routesOf(reference)

	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 50; i++ {
		table := threeSiteBuilder()
		rng.Shuffle(len(table.Routes), func(a, b int) {
			table.Routes[a], table.Routes[b] = table.Routes[b], table.Routes[a]
		})
		table.Sort()
		if !slices.EqualFunc(table.Routes, want, routesEqual) {
			t.Fatalf("seed (1,2) shuffle %d: got\n%v\nwant\n%v", i, table.Routes, want)
		}
	}
}

// routesEqual reports whether two routes are identical in every field.
func routesEqual(a, b route.Route) bool { return route.Compare(a, b) == 0 }

// P1-ROUTE-SORT-003
//
// TestCompareFields checks, one field at a time, that Compare puts the smaller
// value first and that the first field that differs decides. Each case differs
// from the base route in exactly one field, then a later field is set the other
// way round to prove the earlier field wins.
func TestCompareFields(t *testing.T) {
	base := route.Route{
		Prefix: prefixOf("10.42.0.0/20"), NextHop: "b", Via: "a~b", Metric: 10,
		Source: route.SourceComputed, Path: []types.NodeID{"a", "b"}, Active: false,
	}
	cases := []struct {
		name    string
		smaller route.Route
		larger  route.Route
	}{
		{"lower address first", with(base, func(r *route.Route) { r.Prefix = prefixOf("10.42.0.0/20") }),
			with(base, func(r *route.Route) { r.Prefix = prefixOf("10.42.16.0/20"); r.Metric = 0 })},
		{"shorter prefix first", with(base, func(r *route.Route) { r.Prefix = prefixOf("10.42.0.0/16") }),
			with(base, func(r *route.Route) { r.Prefix = prefixOf("10.42.0.0/20"); r.Metric = 0 })},
		{"lower metric first", with(base, func(r *route.Route) { r.Metric = 10 }),
			with(base, func(r *route.Route) { r.Metric = 40; r.NextHop = "a" })},
		{"lower next hop first", with(base, func(r *route.Route) { r.NextHop = "a" }),
			with(base, func(r *route.Route) { r.NextHop = "b"; r.Via = "a~a" })},
		{"lower link first", with(base, func(r *route.Route) { r.Via = "a~a" }),
			with(base, func(r *route.Route) { r.Via = "a~b"; r.Source = route.SourceConnected })},
		{"lower source first", with(base, func(r *route.Route) { r.Source = route.SourceConnected }),
			with(base, func(r *route.Route) { r.Source = route.SourceBackup; r.Active = false })},
		{"inactive before active", with(base, func(r *route.Route) { r.Active = false }),
			with(base, func(r *route.Route) { r.Active = true; r.Path = []types.NodeID{"a"} })},
		{"shorter path first when a prefix", with(base, func(r *route.Route) { r.Path = []types.NodeID{"a"} }),
			with(base, func(r *route.Route) { r.Path = []types.NodeID{"a", "b"} })},
		{"lower path node first", with(base, func(r *route.Route) { r.Path = []types.NodeID{"a", "b"} }),
			with(base, func(r *route.Route) { r.Path = []types.NodeID{"a", "c"} })},
	}
	for _, tc := range cases {
		if got := route.Compare(tc.smaller, tc.larger); got != -1 {
			t.Errorf("%s: Compare(smaller, larger) = %d, want -1", tc.name, got)
		}
		if got := route.Compare(tc.larger, tc.smaller); got != 1 {
			t.Errorf("%s: Compare(larger, smaller) = %d, want 1", tc.name, got)
		}
		if got := route.Compare(tc.smaller, tc.smaller); got != 0 {
			t.Errorf("%s: Compare(x, x) = %d, want 0", tc.name, got)
		}
	}
}

// with returns a copy of r after change has edited it, leaving r untouched.
func with(r route.Route, change func(*route.Route)) route.Route {
	r.Path = slices.Clone(r.Path)
	change(&r)
	return r
}

// P1-ROUTE-SORT-004
//
// TestCompareIsATotalOrder checks the three laws of an order on random routes:
// it is antisymmetric, it is transitive, and it returns 0 only for identical
// routes. Together with the fixed-order test this is what "total" means.
func TestCompareIsATotalOrder(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	routes := make([]route.Route, 60)
	for i := range routes {
		routes[i] = randomRoute(rng)
	}
	for _, a := range routes {
		for _, b := range routes {
			ab, ba := route.Compare(a, b), route.Compare(b, a)
			if ab != -ba {
				t.Fatalf("seed (3,4): Compare not antisymmetric for %v and %v: %d and %d", a, b, ab, ba)
			}
			if ab == 0 && !identical(a, b) {
				t.Fatalf("seed (3,4): Compare returned 0 for different routes %v and %v", a, b)
			}
			for _, c := range routes {
				if ab <= 0 && route.Compare(b, c) <= 0 && route.Compare(a, c) > 0 {
					t.Fatalf("seed (3,4): Compare not transitive for %v, %v, %v", a, b, c)
				}
			}
		}
	}
}

// identical reports whether two routes hold the same value in every field.
func identical(a, b route.Route) bool {
	return a.Prefix == b.Prefix && a.NextHop == b.NextHop && a.Via == b.Via && a.Metric == b.Metric &&
		a.Source == b.Source && a.Active == b.Active && slices.Equal(a.Path, b.Path)
}

// randomRoute builds a route from a small pool of values, so random routes
// often tie on early fields and exercise the later ones.
func randomRoute(rng *rand.Rand) route.Route {
	prefixes := []string{"10.42.0.0/16", "10.42.0.0/20", "10.42.16.0/20", "10.42.32.0/20", "10.42.240.1/32"}
	nodes := []types.NodeID{"", "a", "b", "c"}
	links := []types.LinkID{"", "a~b", "a~c", "b~c"}
	sources := []route.Source{route.SourceConnected, route.SourceComputed, route.SourceBackup, route.SourceStatic}
	var path []types.NodeID
	for n := rng.IntN(4); n > 0; n-- {
		path = append(path, nodes[1+rng.IntN(3)])
	}
	return route.Route{
		Prefix:  prefixOf(prefixes[rng.IntN(len(prefixes))]),
		NextHop: nodes[rng.IntN(len(nodes))],
		Via:     links[rng.IntN(len(links))],
		Metric:  []int{0, 10, 20, 40}[rng.IntN(4)],
		Source:  sources[rng.IntN(len(sources))],
		Path:    path,
		Active:  rng.IntN(2) == 0,
	}
}

// TestSortNilAndEmpty checks that Sort, FIB and Lookup are safe on a nil table
// and an empty one.
func TestSortNilAndEmpty(t *testing.T) {
	var nilTable *route.Table
	nilTable.Sort()
	if got := nilTable.FIB(); got != nil {
		t.Errorf("nil table FIB() = %v, want nil", got)
	}
	if _, ok := nilTable.Lookup(addrOf("10.42.0.1")); ok {
		t.Error("nil table Lookup found a route")
	}

	empty := &route.Table{Node: "x"}
	empty.Sort()
	if got := empty.FIB(); got != nil {
		t.Errorf("empty table FIB() = %v, want nil", got)
	}
	if _, ok := empty.Lookup(addrOf("10.42.0.1")); ok {
		t.Error("empty table Lookup found a route")
	}
}

// TestFIBIsTheActiveRoutes checks that FIB returns exactly the active routes,
// in table order, as a copy: the backup is left out, and changing the result
// does not touch the table.
func TestFIBIsTheActiveRoutes(t *testing.T) {
	table := threeSiteBuilder()
	table.Sort()

	fib := table.FIB()
	if len(fib) != 3 {
		t.Fatalf("FIB has %d routes, want 3: %v", len(fib), fib)
	}
	for _, r := range fib {
		if !r.Active || r.Source == route.SourceBackup {
			t.Errorf("FIB holds a route that is not in use: %v", r)
		}
	}
	if len(table.Routes) != 4 {
		t.Errorf("the table lost routes: %d, want 4 (the backup stays in the full list)", len(table.Routes))
	}

	fib[0].Metric = 999
	for _, r := range table.Routes {
		if r.Metric == 999 {
			t.Error("changing the FIB result changed the table")
		}
	}
}

// TestTablesNodesSorted checks that Tables.Nodes returns the node IDs sorted,
// whatever order the map was filled in.
func TestTablesNodesSorted(t *testing.T) {
	tables := route.Tables{
		"dgx-spark": {Node: "dgx-spark"},
		"aws-hub":   {Node: "aws-hub"},
		"builder-1": {Node: "builder-1"},
	}
	got := tables.Nodes()
	want := []types.NodeID{"aws-hub", "builder-1", "dgx-spark"}
	if !slices.Equal(got, want) {
		t.Errorf("Nodes() = %v, want %v", got, want)
	}
	if got := (route.Tables{}).Nodes(); len(got) != 0 {
		t.Errorf("empty Tables gave %v, want no nodes", got)
	}
}

// TestSourceNames checks every source's text, that Parse reverses String, and
// that the zero value and out-of-range values read as unknown.
func TestSourceNames(t *testing.T) {
	names := map[route.Source]string{
		route.SourceConnected: "connected",
		route.SourceComputed:  "computed",
		route.SourceBackup:    "backup",
		route.SourceStatic:    "static",
	}
	for source, name := range names {
		if got := source.String(); got != name {
			t.Errorf("Source(%d).String() = %q, want %q", int(source), got, name)
		}
		parsed, ok := route.ParseSource(name)
		if !ok || parsed != source {
			t.Errorf("ParseSource(%q) = %v, %v, want %v, true", name, parsed, ok, source)
		}
	}
	for _, source := range []route.Source{route.SourceUnknown, route.Source(99), route.Source(-1)} {
		if got := source.String(); got != "unknown" {
			t.Errorf("Source(%d).String() = %q, want unknown", int(source), got)
		}
	}
	for _, text := range []string{"", "Connected", "COMPUTED", " backup", "learned", "unknown"} {
		if parsed, ok := route.ParseSource(text); ok || parsed != route.SourceUnknown {
			t.Errorf("ParseSource(%q) = %v, %v, want unknown, false", text, parsed, ok)
		}
	}
}

// TestRouteJSON checks the JSON form of a route: field names, the source as a
// word, empty next hop and link left out for a connected route, and a round
// trip back to an identical route.
func TestRouteJSON(t *testing.T) {
	primary := computed("10.42.32.0/20", "dgx-spark", "builder-1~dgx-spark", 10)
	primary.Path = []types.NodeID{"builder-1", "dgx-spark"}
	local := connected("10.42.16.0/20")

	cases := []struct {
		name  string
		route route.Route
		want  string
	}{
		{"computed", primary, `{"prefix":"10.42.32.0/20","next_hop":"dgx-spark","via":"builder-1~dgx-spark","metric":10,"source":"computed","path":["builder-1","dgx-spark"],"active":true}`},
		{"connected", local, `{"prefix":"10.42.16.0/20","metric":0,"source":"connected","active":true}`},
	}
	for _, tc := range cases {
		data, err := json.Marshal(tc.route)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", tc.name, err)
		}
		if string(data) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, data, tc.want)
		}
		var back route.Route
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("%s: Unmarshal: %v", tc.name, err)
		}
		if !identical(back, tc.route) {
			t.Errorf("%s: round trip changed the route:\n got %v\nwant %v", tc.name, back, tc.route)
		}
	}
}

// TestSourceJSONRefusesUnknown checks that a route with no source cannot be
// written, and that an unknown source name is refused on reading and leaves
// the route as it was.
func TestSourceJSONRefusesUnknown(t *testing.T) {
	if _, err := json.Marshal(route.Route{Prefix: prefixOf("10.42.0.0/20")}); err == nil {
		t.Error("Marshal accepted a route with no source")
	}
	if _, err := json.Marshal(route.Route{Source: route.Source(99)}); err == nil {
		t.Error("Marshal accepted source 99")
	}

	start := connected("10.42.0.0/20")
	r := start
	err := json.Unmarshal([]byte(`{"prefix":"10.42.0.0/20","metric":0,"source":"learned","active":true}`), &r)
	if err == nil {
		t.Fatal("Unmarshal accepted an unknown source name")
	}
	if r.Source != route.SourceConnected {
		t.Errorf("a refused source changed the field to %v", r.Source)
	}
}
