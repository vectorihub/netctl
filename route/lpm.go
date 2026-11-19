package route

import "net/netip"

// Lookup returns the route a packet for dst follows: the active route whose
// prefix contains dst and is longest. It is longest-prefix matching, so a route
// for one site beats a route for the whole network, and a route for one machine
// beats both.
//
// The rules, in order:
//   - Inactive routes are never returned, so a backup is not chosen while its
//     primary is healthy.
//   - The longest prefix wins.
//   - Among routes with the same prefix length, the lower metric wins.
//   - Anything still tied is settled by Compare, so the answer never depends on
//     the order of the routes in the table.
//
// It returns false when no active route contains dst, including when dst is
// the zero Addr. There is no default: a table with no 0.0.0.0/0 route has no
// answer for an address outside every prefix, and the caller decides what that
// means. An IPv4-mapped IPv6 address such as ::ffff:10.42.0.1 is an IPv6
// address and does not match an IPv4 prefix. A nil table has no routes.
//
// The returned Route shares its Path with the table; do not change it.
//
// Lookup scans the whole table, which is simple to check by eye and fast enough
// for the hundreds of routes a node holds.
//
// Example: with active routes 10.42.0.0/16 via aws-hub and 10.42.32.0/20 via
// dgx-spark, Lookup(10.42.33.7) returns the /20 route, and Lookup(10.42.1.1)
// returns the /16 route.
func (table *Table) Lookup(dst netip.Addr) (Route, bool) {
	if table == nil {
		return Route{}, false
	}
	var best Route
	found := false
	for _, candidate := range table.Routes {
		if !candidate.Active || !candidate.Prefix.Contains(dst) {
			continue
		}
		if !found || better(candidate, best) {
			best, found = candidate, true
		}
	}
	return best, found
}

// better reports whether candidate should replace current as the answer to a
// lookup. Both routes contain the destination. A longer prefix is better, then
// a lower metric, then whichever sorts first under Compare.
func better(candidate, current Route) bool {
	if candidate.Prefix.Bits() != current.Prefix.Bits() {
		return candidate.Prefix.Bits() > current.Prefix.Bits()
	}
	if candidate.Metric != current.Metric {
		return candidate.Metric < current.Metric
	}
	return Compare(candidate, current) < 0
}
