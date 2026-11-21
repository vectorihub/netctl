package tests

import (
	"math/rand/v2"
	"net/netip"
	"testing"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// indexPrefix returns a random IPv4 or IPv6 prefix from a small pool of
// addresses, so that many routes overlap, share a prefix, or nest. About one in
// five is left unmasked (host bits set), which a hand-built table can hold.
func indexPrefix(rng *rand.Rand) netip.Prefix {
	if rng.IntN(6) == 0 {
		addr := netip.AddrFrom16([16]byte{0x20, 0x01, 0xd, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(rng.IntN(4))})
		return netip.PrefixFrom(addr, 112+rng.IntN(17))
	}
	addr := netip.AddrFrom4([4]byte{10, byte(rng.IntN(3)), byte(rng.IntN(4)), byte(rng.IntN(4))})
	prefix := netip.PrefixFrom(addr, []int{0, 8, 16, 20, 24, 28, 32}[rng.IntN(7)])
	if rng.IntN(5) != 0 {
		prefix = prefix.Masked()
	}
	return prefix
}

// indexTable returns a table of random routes: active and inactive, with
// repeated prefixes at different metrics, next hops and links.
func indexTable(rng *rand.Rand) *route.Table {
	table := &route.Table{Node: "n"}
	for i, count := 0, rng.IntN(40); i < count; i++ {
		hop := types.NodeID([]string{"a", "b", "c"}[rng.IntN(3)])
		table.Routes = append(table.Routes, route.Route{
			Prefix: indexPrefix(rng), NextHop: hop, Via: types.LinkIDOf("n", hop),
			Metric: rng.IntN(4), Source: route.SourceComputed, Active: rng.IntN(3) != 0,
		})
	}
	return table
}

// indexAddr returns a random address from the same pool as indexPrefix, or now
// and then an awkward one: the zero address, an IPv4-mapped address, or an IPv6
// address with a zone.
func indexAddr(rng *rand.Rand) netip.Addr {
	switch rng.IntN(12) {
	case 0:
		return netip.Addr{}
	case 1:
		return netip.AddrFrom16([16]byte{10: 0xff, 11: 0xff, 12: 10, 13: byte(rng.IntN(3)), 14: byte(rng.IntN(4)), 15: byte(rng.IntN(4))})
	case 2:
		return netip.AddrFrom16([16]byte{0x20, 0x01, 0xd, 0xb8, 15: byte(rng.IntN(4))}).WithZone("eth0")
	case 3, 4:
		return netip.AddrFrom16([16]byte{0x20, 0x01, 0xd, 0xb8, 15: byte(rng.IntN(4))})
	}
	return netip.AddrFrom4([4]byte{10, byte(rng.IntN(3)), byte(rng.IntN(4)), byte(rng.IntN(4))})
}

// TestIndexAgreesWithLookup is the property that makes the index safe to use: for
// 10,000 random tables (overlapping prefixes, duplicates at different metrics,
// inactive routes, unmasked prefixes, IPv6, empty tables) and a spread of random
// and awkward addresses, the index returns exactly the route, or the absence of
// one, that Table.Lookup does.
func TestIndexAgreesWithLookup(t *testing.T) {
	rng := rand.New(rand.NewPCG(32, 7))
	for round := 0; round < 10000; round++ {
		table := indexTable(rng)
		index := table.Index()
		for probe := 0; probe < 20; probe++ {
			dst := indexAddr(rng)
			want, wantOK := table.Lookup(dst)
			got, gotOK := index.Lookup(dst)
			if gotOK != wantOK || route.Compare(got, want) != 0 {
				t.Fatalf("round %d, %v: index gave %+v %v, table gave %+v %v\ntable: %+v", round, dst, got, gotOK, want, wantOK, table.Routes)
			}
		}
	}
}

// TestIndexIsASnapshot checks that changing the table after the index was built
// does not change the index, and that a new index sees the change.
func TestIndexIsASnapshot(t *testing.T) {
	table := &route.Table{Node: "n", Routes: []route.Route{
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: "a", Via: "a~n", Metric: 1, Source: route.SourceComputed, Active: true},
	}}
	index := table.Index()
	table.Routes[0].NextHop = "b"
	table.Routes = append(table.Routes, route.Route{Prefix: netip.MustParsePrefix("10.1.0.0/16"), NextHop: "c", Active: true})
	got, ok := index.Lookup(netip.MustParseAddr("10.1.2.3"))
	if !ok || got.NextHop != "a" || got.Prefix.Bits() != 8 {
		t.Errorf("the old index saw the change: %+v %v", got, ok)
	}
	got, _ = table.Index().Lookup(netip.MustParseAddr("10.1.2.3"))
	if got.NextHop != "c" {
		t.Errorf("a new index did not see the change: %+v", got)
	}
}

// TestIndexOfNothing checks a nil table, a nil index and an empty table all find
// nothing and do not panic.
func TestIndexOfNothing(t *testing.T) {
	addr := netip.MustParseAddr("10.0.0.1")
	var nilTable *route.Table
	var nilIndex *route.Index
	for name, index := range map[string]*route.Index{"nil table": nilTable.Index(), "nil index": nilIndex, "empty table": (&route.Table{}).Index()} {
		if _, ok := index.Lookup(addr); ok {
			t.Errorf("%s: found a route in nothing", name)
		}
	}
}

// BenchmarkIndexLookup measures one lookup in a 2,000-route table through the
// index, for comparison with BenchmarkTableLookup.
func BenchmarkIndexLookup(b *testing.B) {
	table, addrs := spokeTable()
	index := table.Index()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index.Lookup(addrs[i%len(addrs)])
	}
}

// BenchmarkTableLookup measures one lookup in the same 2,000-route table by
// scanning it.
func BenchmarkTableLookup(b *testing.B) {
	table, addrs := spokeTable()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		table.Lookup(addrs[i%len(addrs)])
	}
}

// spokeTable returns a table of 2,000 routes, half of them inactive backups, like a
// spoke in a 500-node network, and a set of addresses to look up in it.
func spokeTable() (*route.Table, []netip.Addr) {
	table := &route.Table{Node: "n"}
	var addrs []netip.Addr
	for i := 0; i < 500; i++ {
		site := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 0}), 24)
		host := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 200, byte(i >> 8), byte(i)}), 32)
		for _, prefix := range []netip.Prefix{site, host} {
			table.Routes = append(table.Routes,
				route.Route{Prefix: prefix, NextHop: "a", Via: "a~n", Metric: 10, Source: route.SourceComputed, Active: true},
				route.Route{Prefix: prefix, NextHop: "b", Via: "b~n", Metric: 40, Source: route.SourceBackup})
		}
		addrs = append(addrs, host.Addr())
	}
	return table, addrs
}
