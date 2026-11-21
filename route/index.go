package route

import (
	"net/netip"
	"slices"
)

// Index is a read-only snapshot of a table's active routes, built for fast
// longest-prefix lookup. It answers exactly what Table.Lookup answers, but
// instead of scanning every route it asks, for each prefix length the table
// uses, whether one route has that prefix: a handful of hash lookups however
// many routes the table holds.
//
// Use it when the same table is asked many times, as the packet simulator does.
// It is a copy: changing the table afterwards is not seen by an Index built
// before, so build a new one. An Index is safe for concurrent use, because
// nothing in it changes.
//
// Example: table.Index() returns an Index; Index.Lookup(10.42.33.7) answers as table.Lookup
// would, without scanning the table.
type Index struct {
	// v4 and v6 index the IPv4 and IPv6 routes. Keeping them apart means an
	// IPv4-mapped IPv6 address, which is an IPv6 address, never matches an IPv4
	// prefix, as in Table.Lookup.
	v4, v6 family
}

// family indexes the routes of one address family.
type family struct {
	// routes holds the winning route for each prefix, in no particular order.
	routes []Route
	// byPrefix maps a masked prefix to the position of its route in routes.
	byPrefix map[netip.Prefix]int32
	// lengths lists the distinct prefix lengths present, longest first.
	lengths []int
}

// Index returns an Index over the table's active routes. Inactive routes are not
// in it, as they are not in a lookup. Where several active routes have the same
// prefix the one Table.Lookup would choose is kept: the lower metric, then
// whichever sorts first under Compare. Every route is masked to its canonical
// block (cidr.Canonical) as it enters, exactly as in Table.Lookup, so a route
// written 10.1.2.3/24 is stored, compared and returned as 10.1.2.0/24 and both
// lookups give the same route. A nil table gives an Index that finds nothing.
//
// Example: an index of a table with routes for 10.42.0.0/16 and 10.42.32.0/20
// answers 10.42.33.7 with the /20 route, as Table.Lookup does.
func (table *Table) Index() *Index {
	index := &Index{}
	if table == nil {
		return index
	}
	for _, given := range table.Routes {
		candidate := canonical(given)
		if !candidate.Active || !candidate.Prefix.IsValid() {
			continue
		}
		fam := &index.v6
		if candidate.Prefix.Addr().Is4() {
			fam = &index.v4
		}
		fam.add(candidate)
	}
	index.v4.finish()
	index.v6.finish()
	return index
}

// add records a candidate route, whose prefix is already masked, replacing the
// route already held for the same prefix only if the candidate is better.
func (fam *family) add(candidate Route) {
	if fam.byPrefix == nil {
		fam.byPrefix = map[netip.Prefix]int32{}
	}
	key := candidate.Prefix
	if at, held := fam.byPrefix[key]; held {
		if better(candidate, fam.routes[at]) {
			fam.routes[at] = candidate
		}
		return
	}
	fam.byPrefix[key] = int32(len(fam.routes))
	fam.routes = append(fam.routes, candidate)
}

// finish works out which prefix lengths are in use, longest first.
func (fam *family) finish() {
	seen := map[int]bool{}
	for key := range fam.byPrefix {
		if !seen[key.Bits()] {
			seen[key.Bits()] = true
			fam.lengths = append(fam.lengths, key.Bits())
		}
	}
	slices.SortFunc(fam.lengths, func(a, b int) int { return b - a })
}

// Lookup returns the route a packet for dst follows, with the same rules and the
// same answer as Table.Lookup on the table the index was built from: the longest
// matching prefix, no route at all for an address nothing matches, the zero Addr
// or an address with an IPv6 zone. The returned Route shares its Path with the
// table; do not change it.
//
// Example: Lookup(10.42.33.7) on the index above returns the /20 route and
// Lookup(192.168.1.1) returns false.
func (index *Index) Lookup(dst netip.Addr) (Route, bool) {
	if index == nil || !dst.IsValid() || dst.Zone() != "" {
		return Route{}, false
	}
	fam := &index.v6
	if dst.Is4() {
		fam = &index.v4
	}
	for _, bits := range fam.lengths {
		key, err := dst.Prefix(bits)
		if err != nil {
			continue
		}
		if at, ok := fam.byPrefix[key]; ok {
			return fam.routes[at], true
		}
	}
	return Route{}, false
}
