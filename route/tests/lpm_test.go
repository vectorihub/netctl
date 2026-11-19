package tests

import (
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// lookupWants runs Lookup for one destination and checks the result: whether
// a route was found, and if so which prefix and next hop it has. An empty
// wantPrefix means "no route expected".
func lookupWants(t *testing.T, name string, table *route.Table, dst, wantPrefix string, wantNextHop types.NodeID) {
	t.Helper()
	got, ok := table.Lookup(addrOf(dst))
	if wantPrefix == "" {
		if ok {
			t.Errorf("%s: Lookup(%s) = %v, want no route", name, dst, got)
		}
		return
	}
	if !ok {
		t.Errorf("%s: Lookup(%s) found nothing, want %s via %q", name, dst, wantPrefix, wantNextHop)
		return
	}
	if got.Prefix != prefixOf(wantPrefix) || got.NextHop != wantNextHop {
		t.Errorf("%s: Lookup(%s) = %s via %q, want %s via %q", name, dst, got.Prefix, got.NextHop, wantPrefix, wantNextHop)
	}
}

// inEveryOrder runs check against the same routes in their given order, in
// reverse, and in thirty shuffles, so a result that secretly depends on the
// order of the table is caught.
func inEveryOrder(t *testing.T, routes []route.Route, check func(table *route.Table)) {
	t.Helper()
	forward := &route.Table{Node: "x", Routes: slices.Clone(routes)}
	check(forward)

	reversed := &route.Table{Node: "x", Routes: slices.Clone(routes)}
	slices.Reverse(reversed.Routes)
	check(reversed)

	rng := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 30; i++ {
		shuffled := &route.Table{Node: "x", Routes: slices.Clone(routes)}
		rng.Shuffle(len(shuffled.Routes), func(a, b int) {
			shuffled.Routes[a], shuffled.Routes[b] = shuffled.Routes[b], shuffled.Routes[a]
		})
		check(shuffled)
	}
}

// P1-ROUTE-001
//
// TestLongerPrefixWins checks that a /20 route beats the /16 that contains it:
// 10.42.33.7 goes to dgx-spark, while an address in the /16 but outside the
// /20 still goes to aws-hub.
func TestLongerPrefixWins(t *testing.T) {
	routes := []route.Route{
		computed("10.42.0.0/16", "aws-hub", "aws-hub~builder-1", 20),
		computed("10.42.32.0/20", "dgx-spark", "builder-1~dgx-spark", 10),
	}
	inEveryOrder(t, routes, func(table *route.Table) {
		lookupWants(t, "inside the /20", table, "10.42.33.7", "10.42.32.0/20", "dgx-spark")
		lookupWants(t, "inside the /16 only", table, "10.42.1.1", "10.42.0.0/16", "aws-hub")
	})
}

// P1-ROUTE-002
//
// TestConnectedBeatsDefault checks that a connected route for the node's own
// site wins over a default route, because it is more specific.
func TestConnectedBeatsDefault(t *testing.T) {
	routes := []route.Route{
		computed("0.0.0.0/0", "aws-hub", "aws-hub~builder-1", 20),
		connected("10.42.16.0/20"),
	}
	inEveryOrder(t, routes, func(table *route.Table) {
		got, ok := table.Lookup(addrOf("10.42.16.9"))
		if !ok || got.Source != route.SourceConnected || got.NextHop != "" {
			t.Errorf("Lookup(10.42.16.9) = %v, %v, want the connected route", got, ok)
		}
		lookupWants(t, "outside the site", table, "8.8.8.8", "0.0.0.0/0", "aws-hub")
	})
}

// P1-ROUTE-003
//
// TestHostRouteOverridesBlock checks that a /32 route beats the /20 that
// contains it, even when that looks wrong: Lookup does what the table says and
// leaves the judgement to the validator.
func TestHostRouteOverridesBlock(t *testing.T) {
	override := computed("10.42.240.3/32", "aws-hub", "aws-hub~builder-1", 20)
	override.Source = route.SourceStatic
	routes := []route.Route{
		computed("10.42.240.0/20", "dgx-spark", "builder-1~dgx-spark", 10),
		override,
	}
	inEveryOrder(t, routes, func(table *route.Table) {
		lookupWants(t, "the overridden host", table, "10.42.240.3", "10.42.240.3/32", "aws-hub")
		lookupWants(t, "a neighbour of the host", table, "10.42.240.4", "10.42.240.0/20", "dgx-spark")
	})
}

// P1-ROUTE-004
//
// TestLowerMetricBreaksTie checks that two active routes for the same prefix
// are settled by metric: the metric-10 route wins over the metric-40 one,
// whichever is listed first.
func TestLowerMetricBreaksTie(t *testing.T) {
	routes := []route.Route{
		computed("10.42.32.0/20", "aws-hub", "aws-hub~builder-1", 40),
		computed("10.42.32.0/20", "dgx-spark", "builder-1~dgx-spark", 10),
	}
	inEveryOrder(t, routes, func(table *route.Table) {
		got, ok := table.Lookup(addrOf("10.42.32.1"))
		if !ok || got.Metric != 10 || got.NextHop != "dgx-spark" {
			t.Errorf("Lookup(10.42.32.1) = %v, %v, want the metric-10 route", got, ok)
		}
	})
}

// P1-ROUTE-005
//
// TestNoMatchNoDefault checks that an address outside every prefix has no
// route when the table has no default route, and that an empty table never
// matches anything.
func TestNoMatchNoDefault(t *testing.T) {
	routes := []route.Route{
		computed("10.42.0.0/20", "aws-hub", "aws-hub~builder-1", 20),
		connected("10.42.16.0/20"),
	}
	inEveryOrder(t, routes, func(table *route.Table) {
		lookupWants(t, "outside every prefix", table, "192.168.1.1", "", "")
		lookupWants(t, "just past the last prefix", table, "10.42.32.0", "", "")
	})
	lookupWants(t, "empty table", &route.Table{Node: "x"}, "10.42.0.1", "", "")
}

// P1-ROUTE-006
//
// TestBoundaryAddresses checks the edges of a prefix: the first and last
// address match, the address just before the first and just after the last do
// not. It repeats the check for a /32, a /31, a default route, and the very
// first and last addresses of the whole IPv4 space.
func TestBoundaryAddresses(t *testing.T) {
	cases := []struct {
		prefix                     string
		before, first, last, after string
	}{
		{"10.42.32.0/20", "10.42.31.255", "10.42.32.0", "10.42.47.255", "10.42.48.0"},
		{"10.42.240.7/32", "10.42.240.6", "10.42.240.7", "10.42.240.7", "10.42.240.8"},
		{"10.42.252.4/31", "10.42.252.3", "10.42.252.4", "10.42.252.5", "10.42.252.6"},
		{"0.0.0.0/1", "", "0.0.0.0", "127.255.255.255", "128.0.0.0"},
		{"128.0.0.0/1", "127.255.255.255", "128.0.0.0", "255.255.255.255", ""},
	}
	for _, tc := range cases {
		table := &route.Table{Node: "x", Routes: []route.Route{computed(tc.prefix, "n", "x~n", 1)}}
		if tc.before != "" {
			lookupWants(t, tc.prefix+" before", table, tc.before, "", "")
		}
		lookupWants(t, tc.prefix+" first", table, tc.first, tc.prefix, "n")
		lookupWants(t, tc.prefix+" last", table, tc.last, tc.prefix, "n")
		if tc.after != "" {
			lookupWants(t, tc.prefix+" after", table, tc.after, "", "")
		}
	}

	everything := &route.Table{Node: "x", Routes: []route.Route{computed("0.0.0.0/0", "n", "x~n", 1)}}
	lookupWants(t, "default first", everything, "0.0.0.0", "0.0.0.0/0", "n")
	lookupWants(t, "default last", everything, "255.255.255.255", "0.0.0.0/0", "n")
}

// P1-ROUTE-008
//
// TestInactiveRoutesIgnored checks that Lookup never returns an inactive
// route: a backup with a better metric and a longer prefix is skipped while
// inactive, the table answers with the active route, and once the backup is
// marked active it is returned.
func TestInactiveRoutesIgnored(t *testing.T) {
	backup := computed("10.42.32.0/24", "aws-hub", "aws-hub~builder-1", 1)
	backup.Source = route.SourceBackup
	backup.Active = false
	routes := []route.Route{computed("10.42.32.0/20", "dgx-spark", "builder-1~dgx-spark", 10), backup}

	inEveryOrder(t, routes, func(table *route.Table) {
		lookupWants(t, "backup inactive", table, "10.42.32.5", "10.42.32.0/20", "dgx-spark")
	})

	onlyBackup := &route.Table{Node: "x", Routes: []route.Route{backup}}
	lookupWants(t, "only an inactive route", onlyBackup, "10.42.32.5", "", "")

	backup.Active = true
	failedOver := &route.Table{Node: "x", Routes: []route.Route{routes[0], backup}}
	lookupWants(t, "backup active", failedOver, "10.42.32.5", "10.42.32.0/24", "aws-hub")
}

// TestLookupTieIsSettledByCompare checks that when two active routes have the
// same prefix and the same metric, the answer is the one Compare sorts first,
// in every order of the table, so a tie never depends on list position.
func TestLookupTieIsSettledByCompare(t *testing.T) {
	routes := []route.Route{
		computed("10.42.32.0/20", "zeta", "builder-1~zeta", 10),
		computed("10.42.32.0/20", "alpha", "alpha~builder-1", 10),
		computed("10.42.32.0/20", "mid", "builder-1~mid", 10),
	}
	inEveryOrder(t, routes, func(table *route.Table) {
		got, ok := table.Lookup(addrOf("10.42.33.1"))
		if !ok || got.NextHop != "alpha" {
			t.Errorf("Lookup = %v, %v, want next hop alpha", got, ok)
		}
	})
}

// TestLookupSpellingsOfOnePrefix checks that a prefix written with host bits
// set (10.42.16.5/20) is matched like its canonical form, and that two spellings
// of one block tie on length and metric, then fall to Compare's order.
func TestLookupSpellingsOfOnePrefix(t *testing.T) {
	table := &route.Table{Node: "x", Routes: []route.Route{computed("10.42.16.5/20", "n", "x~n", 5)}}
	lookupWants(t, "host bits set", table, "10.42.31.200", "10.42.16.5/20", "n")
	lookupWants(t, "outside", table, "10.42.32.1", "", "")

	routes := []route.Route{
		computed("10.42.16.5/20", "second", "x~second", 5),
		computed("10.42.16.0/20", "first", "x~first", 5),
	}
	inEveryOrder(t, routes, func(table *route.Table) {
		got, ok := table.Lookup(addrOf("10.42.20.1"))
		if !ok || got.NextHop != "first" {
			t.Errorf("Lookup = %v, %v, want the canonical spelling (next hop first)", got, ok)
		}
	})
}

// TestLookupBadDestinations checks that destinations that are not plain IPv4
// addresses never match an IPv4 route and never panic: the zero Addr, an IPv6
// address, an IPv4-mapped IPv6 address, and an address with a zone.
func TestLookupBadDestinations(t *testing.T) {
	table := &route.Table{Node: "x", Routes: []route.Route{
		computed("0.0.0.0/0", "n", "x~n", 1),
		computed("10.42.0.0/16", "m", "x~m", 1),
	}}
	cases := []struct {
		name string
		dst  netip.Addr
	}{
		{"zero Addr", netip.Addr{}},
		{"IPv6", addrOf("2001:db8::1")},
		{"IPv4-mapped IPv6", addrOf("::ffff:10.42.0.1")},
		{"IPv6 with zone", addrOf("fe80::1%eth0")},
	}
	for _, tc := range cases {
		if got, ok := table.Lookup(tc.dst); ok {
			t.Errorf("%s: Lookup returned %v, want no route", tc.name, got)
		}
	}
}

// TestLookupIgnoresInvalidPrefixes checks that a route whose prefix is the zero
// value never matches, and does not stop a valid route from matching.
func TestLookupIgnoresInvalidPrefixes(t *testing.T) {
	broken := route.Route{Active: true, NextHop: "broken", Source: route.SourceComputed}
	routes := []route.Route{broken, computed("10.42.0.0/16", "good", "x~good", 5)}
	inEveryOrder(t, routes, func(table *route.Table) {
		lookupWants(t, "valid route beside a broken one", table, "10.42.9.9", "10.42.0.0/16", "good")
	})
}

// TestLookupKeepsPathAndLeavesTableAlone checks that the returned route carries
// the path and the other fields of the table's route, and that a lookup does
// not reorder or change the table.
func TestLookupKeepsPathAndLeavesTableAlone(t *testing.T) {
	table := threeSiteBuilder()
	before := routesOf(table)

	got, ok := table.Lookup(addrOf("10.42.33.7"))
	if !ok {
		t.Fatal("Lookup(10.42.33.7) found nothing")
	}
	wantPath := []types.NodeID{"builder-1", "dgx-spark"}
	if got.NextHop != "dgx-spark" || got.Via != "builder-1~dgx-spark" || got.Metric != 10 ||
		got.Source != route.SourceComputed || !slices.Equal(got.Path, wantPath) {
		t.Errorf("Lookup(10.42.33.7) = %+v, want the metric-10 route with path %v", got, wantPath)
	}
	if !slices.EqualFunc(table.Routes, before, routesEqual) {
		t.Error("Lookup changed the table")
	}
}

// TestReferenceTableLookups checks Lookup against the reference three-site
// tables for builder-1 (healthy direct link): each machine's address is routed
// to the next hop the reference table gives, and the backup is not used.
func TestReferenceTableLookups(t *testing.T) {
	table := threeSiteBuilder()
	table.Sort()
	lookupWants(t, "aws site", table, "10.42.1.1", "10.42.0.0/20", "aws-hub")
	lookupWants(t, "own site", table, "10.42.16.9", "10.42.16.0/20", "")
	lookupWants(t, "dgx site", table, "10.42.33.7", "10.42.32.0/20", "dgx-spark")
	lookupWants(t, "off the plan", table, "192.168.1.1", "", "")
}
