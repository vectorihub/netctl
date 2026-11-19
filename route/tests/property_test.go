package tests

import (
	"cmp"
	"encoding/binary"
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// propertyCases is how many random tables TestLookupAgainstOracle checks. The
// spec asks for 100,000 random tables and destinations when a trie is added;
// the same budget is used here for the scan.
const propertyCases = 100_000

// ipNumber turns an IPv4 address into its number, for the oracle's arithmetic.
func ipNumber(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

// oracle is an independent answer to "which route does dst take?". It does not
// share code with Lookup: it tests containment with integer masks instead of
// netip's Contains, and it picks the winner by sorting every candidate by
// prefix length (longest first), then metric, then Compare, and taking the
// first. It returns false when no active route contains dst.
func oracle(routes []route.Route, dst netip.Addr) (route.Route, bool) {
	target := ipNumber(dst)
	var candidates []route.Route
	for _, r := range routes {
		if !r.Active {
			continue
		}
		bits := r.Prefix.Bits()
		var mask uint32
		if bits > 0 {
			mask = ^uint32(0) << (32 - bits)
		}
		if ipNumber(r.Prefix.Addr())&mask == target&mask {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return route.Route{}, false
	}
	slices.SortFunc(candidates, func(a, b route.Route) int {
		return cmp.Or(
			cmp.Compare(b.Prefix.Bits(), a.Prefix.Bits()),
			cmp.Compare(a.Metric, b.Metric),
			route.Compare(a, b),
		)
	})
	return candidates[0], true
}

// randomTable builds a table of up to twelve random routes. Prefixes are
// nested blocks around one address so lookups often have several candidates,
// and a few are written with host bits set. Metrics come from a tiny range so
// ties are common, and about a third of the routes are inactive.
func randomTable(rng *rand.Rand) *route.Table {
	centre := netip.AddrFrom4([4]byte{10, 42, byte(rng.IntN(4) * 16), byte(rng.IntN(256))})
	routes := make([]route.Route, rng.IntN(13))
	for i := range routes {
		bits := []int{0, 8, 16, 20, 24, 28, 31, 32}[rng.IntN(8)]
		prefix := netip.PrefixFrom(centre, bits)
		if rng.IntN(4) != 0 {
			prefix = prefix.Masked()
		}
		if rng.IntN(5) == 0 {
			// A block next to the centre, so some routes match and some do not.
			neighbour := netip.AddrFrom4([4]byte{10, 42, byte(rng.IntN(64)), byte(rng.IntN(256))})
			prefix = netip.PrefixFrom(neighbour, bits).Masked()
		}
		hop := types.NodeID([]string{"a", "b", "c", "d"}[rng.IntN(4)])
		routes[i] = route.Route{
			Prefix:  prefix,
			NextHop: hop,
			Via:     types.LinkIDOf("x", hop),
			Metric:  rng.IntN(4),
			Source:  route.SourceComputed,
			Active:  rng.IntN(3) != 0,
		}
	}
	return &route.Table{Node: "x", Routes: routes}
}

// randomDestination picks a destination near the table's routes about half the
// time, and anywhere at all the rest of the time, so both hits and misses are
// common.
func randomDestination(rng *rand.Rand, table *route.Table) netip.Addr {
	if len(table.Routes) > 0 && rng.IntN(2) == 0 {
		chosen := table.Routes[rng.IntN(len(table.Routes))]
		if chosen.Prefix.Bits() == 0 {
			return netip.AddrFrom4([4]byte{byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256)), byte(rng.IntN(256))})
		}
		start := ipNumber(chosen.Prefix.Masked().Addr())
		size := uint32(1) << (32 - chosen.Prefix.Bits())
		offset := uint32(rng.Uint64()) % size
		var bytes [4]byte
		binary.BigEndian.PutUint32(bytes[:], start+offset)
		return netip.AddrFrom4(bytes)
	}
	return netip.AddrFrom4([4]byte{10, 42, byte(rng.IntN(64)), byte(rng.IntN(256))})
}

// TestLookupAgainstOracle compares Lookup with an independent oracle on 100,000
// random tables and destinations, and checks that shuffling the table does not
// change the answer. Each case has its own seeded generator, so a failure
// prints the case number and the routes needed to reproduce it. It stands in
// for P1-ROUTE-007 until a trie exists to be compared with the scan.
func TestLookupAgainstOracle(t *testing.T) {
	for i := 0; i < propertyCases; i++ {
		rng := rand.New(rand.NewPCG(7, uint64(i)))
		table := randomTable(rng)
		dst := randomDestination(rng, table)

		want, wantOK := oracle(table.Routes, dst)
		got, gotOK := table.Lookup(dst)
		if gotOK != wantOK || (gotOK && !identical(got, want)) {
			t.Fatalf("seed (7,%d): Lookup(%s) = %+v, %v; oracle says %+v, %v\nroutes: %+v", i, dst, got, gotOK, want, wantOK, table.Routes)
		}

		shuffled := slices.Clone(table.Routes)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		again, againOK := (&route.Table{Node: "x", Routes: shuffled}).Lookup(dst)
		if againOK != gotOK || (gotOK && !identical(again, got)) {
			t.Fatalf("seed (7,%d): shuffling the table changed Lookup(%s): %+v then %+v\nroutes: %+v", i, dst, got, again, table.Routes)
		}
	}
}

// TestLookupHitsAndMissesBothHappen checks that the random generator really
// exercises both outcomes, so the oracle test above cannot pass by only ever
// seeing misses (or only hits).
func TestLookupHitsAndMissesBothHappen(t *testing.T) {
	hits, misses := 0, 0
	for i := 0; i < 2000; i++ {
		rng := rand.New(rand.NewPCG(7, uint64(i)))
		table := randomTable(rng)
		if _, ok := table.Lookup(randomDestination(rng, table)); ok {
			hits++
		} else {
			misses++
		}
	}
	if hits < 400 || misses < 400 {
		t.Errorf("the generator is lopsided: %d hits, %d misses in 2000 cases", hits, misses)
	}
}
