package tests

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

// defaultPools is the D14 address plan.
func defaultPools() []ipam.Pool {
	return []ipam.Pool{
		{Name: "sites", Prefix: pfx("10.42.0.0/17"), ChildBits: 20},
		{Name: "growth", Prefix: pfx("10.42.128.0/18"), ChildBits: 20, Reserved: true},
		{Name: "overlay", Prefix: pfx("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
		{Name: "infra", Prefix: pfx("10.42.248.0/22"), Reserved: true},
		{Name: "transit", Prefix: pfx("10.42.252.0/22"), ChildBits: 30},
	}
}

// allocsOf returns the registry's allocations, sorted, as a copy. The tests
// reach the registry only through its exported methods, and Snapshot is the
// public way to see its state.
func allocsOf(registry *ipam.IPAM) []ipam.Allocation { return registry.Snapshot().Allocations }

// snapshotWith returns a snapshot of the default plan (10.42.0.0/16 and its five pools) holding
// allocs.
func snapshotWith(allocs ...ipam.Allocation) ipam.Snapshot {
	return ipam.Snapshot{Schema: ipam.Schema, Supernet: pfx("10.42.0.0/16"), Pools: defaultPools(), Allocations: allocs}
}

// held returns an allocated Allocation for prefix, pool and owner, created at the fixed test time.
func held(prefix string, pool ipam.PoolName, owner ipam.Owner) ipam.Allocation {
	return ipam.Allocation{Prefix: pfx(prefix), Pool: pool, Owner: owner, State: ipam.Allocated, CreatedAt: t0}
}

// build constructs a registry from snap with a fixed clock and a 24 hour quarantine.
func build(t *testing.T, snap ipam.Snapshot) (*ipam.IPAM, error) {
	t.Helper()
	return ipam.New(snap, &types.FixedClock{T: t0}, 24*time.Hour)
}

// codes returns the code of every problem in err, in order.
func codes(t *testing.T, err error) []types.Code {
	t.Helper()
	var list types.List
	if !errors.As(err, &list) {
		t.Fatalf("error is %T, want types.List: %v", err, err)
	}
	out := make([]types.Code, len(list))
	for i, problem := range list {
		out[i] = problem.Code
	}
	return out
}

// TestNewAcceptsGoodSnapshots checks that valid snapshots are accepted, with and without
// allocations.
func TestNewAcceptsGoodSnapshots(t *testing.T) {
	empty, err := build(t, snapshotWith())
	if err != nil || len(empty.Snapshot().Pools) != 5 || len(allocsOf(empty)) != 0 {
		t.Fatalf("empty plan: %v, %+v", err, empty)
	}
	// Out of order on purpose; non-canonical prefix gets masked.
	registry, err := build(t, snapshotWith(
		held("10.42.240.1/32", "overlay", ipam.Node("builder-1")),
		held("10.42.16.0/20", "sites", ipam.Site("gcp")),
		held("10.42.0.5/20", "sites", ipam.Site("aws")),
	))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, alloc := range allocsOf(registry) {
		got = append(got, alloc.Prefix.String())
	}
	if want := "10.42.0.0/20 10.42.16.0/20 10.42.240.1/32"; strings.Join(got, " ") != want {
		t.Errorf("allocs = %v, want %s", got, want)
	}
}

// P1-IPAM-012: both allocations named in the problem.
func TestNewRefusesOverlappingAllocations(t *testing.T) {
	_, err := build(t, snapshotWith(
		held("10.42.0.0/20", "sites", ipam.Site("aws")),
		held("10.42.0.3/32", "sites", ipam.Node("builder-1")),
	))
	if !types.HasCode(err, types.ErrOverlap) {
		t.Fatalf("want OVERLAP, got %v", err)
	}
	for _, name := range []string{"site:aws", "node:builder-1"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s: %v", name, err)
		}
	}
}

// P1-IPAM-013
//
// TestNewRefusesOverlappingPools checks that two pools whose blocks overlap are refused and the
// error names a pool.
func TestNewRefusesOverlappingPools(t *testing.T) {
	snap := snapshotWith()
	snap.Pools = append(snap.Pools, ipam.Pool{Name: "extra", Prefix: pfx("10.42.0.0/20"), ChildBits: 24})
	_, err := build(t, snap)
	if !types.HasCode(err, types.ErrOverlap) || !strings.Contains(err.Error(), "sites") {
		t.Fatalf("want OVERLAP naming sites, got %v", err)
	}
}

// P1-IPAM-023
//
// TestNewRefusesOutOfSupernet checks that an allocation or pool outside the supernet is
// OUT_OF_SUPERNET.
func TestNewRefusesOutOfSupernet(t *testing.T) {
	_, err := build(t, snapshotWith(held("192.168.1.0/24", "sites", ipam.Site("rogue"))))
	if !types.HasCode(err, types.ErrOutOfSupernet) {
		t.Fatalf("allocation: want OUT_OF_SUPERNET, got %v", err)
	}
	snap := snapshotWith()
	snap.Pools[0].Prefix = pfx("10.43.0.0/17")
	if _, err := build(t, snap); !types.HasCode(err, types.ErrOutOfSupernet) {
		t.Fatalf("pool: want OUT_OF_SUPERNET, got %v", err)
	}
	// A prefix that sticks out of the supernet is not "inside" it.
	if _, err := build(t, snapshotWith(held("10.40.0.0/14", "sites", ipam.Site("wide")))); !types.HasCode(err, types.ErrOutOfSupernet) {
		t.Fatalf("wider than supernet: want OUT_OF_SUPERNET, got %v", err)
	}
}

// P1-IPAM-041
//
// TestNewRefusesUnknownSchema checks that any schema string other than the current one is
// UNSUPPORTED_SCHEMA.
func TestNewRefusesUnknownSchema(t *testing.T) {
	for _, schema := range []string{"beaver.ipam/v9", "", "beaver.ipam/v1 "} {
		snap := snapshotWith()
		snap.Schema = schema
		registry, err := build(t, snap)
		if registry != nil || !types.HasCode(err, types.ErrUnsupportedSchema) {
			t.Errorf("schema %q: got %v, %v", schema, registry, err)
		}
		if !strings.Contains(err.Error(), "beaver.ipam/v1") {
			t.Errorf("error does not say what is wanted: %v", err)
		}
		if got := codes(t, err); len(got) != 1 {
			t.Errorf("schema %q: want exactly one problem, got %v", schema, got)
		}
	}
}

// Done when: a snapshot with three faults reports three errors.
func TestNewReportsEveryProblem(t *testing.T) {
	snap := snapshotWith(
		held("10.42.0.0/20", "sites", ipam.Site("aws")),
		held("10.42.0.0/21", "sites", ipam.Site("dupe")),     // overlaps aws
		held("172.16.0.0/24", "sites", ipam.Site("rogue")),   // outside the supernet
		held("10.42.100.0/22", "overlay", ipam.Site("liar")), // names the wrong pool
	)
	registry, err := build(t, snap)
	if registry != nil {
		t.Fatal("want nil registry on error")
	}
	got := codes(t, err)
	want := []types.Code{types.ErrOverlap, types.ErrOutOfSupernet, types.ErrInvalidPrefix}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v\n%v", got, want, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("problem %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestNewRefusesBadPools checks each way a pool definition can be wrong (duplicate or empty name,
// bad child size, and so on).
func TestNewRefusesBadPools(t *testing.T) {
	cases := map[string]func(*ipam.Snapshot){
		"duplicate name":  func(s *ipam.Snapshot) { s.Pools[1].Name = "sites" },
		"empty name":      func(s *ipam.Snapshot) { s.Pools[1].Name = "" },
		"child too short": func(s *ipam.Snapshot) { s.Pools[0].ChildBits = 8 },
		"child over 32":   func(s *ipam.Snapshot) { s.Pools[0].ChildBits = 33 },
		"negative skip":   func(s *ipam.Snapshot) { s.Pools[0].SkipFirst = -1 },
		"zero prefix":     func(s *ipam.Snapshot) { s.Pools[0].Prefix = netip.Prefix{} },
		"ipv6 pool":       func(s *ipam.Snapshot) { s.Pools[0].Prefix = pfx("fd00::/64") },
	}
	for name, mutate := range cases {
		snap := snapshotWith()
		mutate(&snap)
		if _, err := build(t, snap); !types.HasCode(err, types.ErrInvalidPrefix) {
			t.Errorf("%s: want INVALID_PREFIX, got %v", name, err)
		}
	}
}

// TestNewRefusesBadAllocations checks each way a stored allocation can be wrong (unknown state,
// wrong size, overlap, and so on).
func TestNewRefusesBadAllocations(t *testing.T) {
	bad := held("10.42.0.0/20", "sites", ipam.Site("aws"))
	bad.State = "free"
	cases := map[string]ipam.Allocation{
		"unknown state":   bad,
		"wrong pool name": held("10.42.0.0/20", "overlay", ipam.Site("aws")),
		"unknown pool":    held("10.42.0.0/20", "nope", ipam.Site("aws")),
		"zero prefix":     {Pool: "sites", Owner: ipam.Site("aws"), State: ipam.Allocated},
		"ipv6":            held("fd00::/64", "sites", ipam.Site("aws")),
	}
	for name, alloc := range cases {
		if _, err := build(t, snapshotWith(alloc)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// Inside the supernet but in no pool: 10.42.192.0/19 is unplanned space.
	if _, err := build(t, snapshotWith(held("10.42.192.0/20", "sites", ipam.Site("aws")))); !types.HasCode(err, types.ErrInvalidPrefix) {
		t.Errorf("unplanned space: want INVALID_PREFIX, got %v", err)
	}
}

// TestNewRefusesBadSupernetAndMisuse checks a missing supernet, a nil clock and a negative
// quarantine.
func TestNewRefusesBadSupernetAndMisuse(t *testing.T) {
	snap := snapshotWith()
	snap.Supernet = netip.Prefix{}
	if _, err := build(t, snap); !types.HasCode(err, types.ErrInvalidPrefix) {
		t.Errorf("zero supernet: got %v", err)
	}
	if _, err := ipam.New(snapshotWith(), nil, time.Hour); err == nil {
		t.Error("nil clock: want an error")
	}
	if _, err := ipam.New(snapshotWith(), &types.FixedClock{T: t0}, -time.Second); err == nil {
		t.Error("negative quarantine: want an error")
	}
}

// TestNewDoesNotAliasSnapshot checks that changing the snapshot after New does not change the
// registry.
func TestNewDoesNotAliasSnapshot(t *testing.T) {
	snap := snapshotWith(held("10.42.0.0/20", "sites", ipam.Site("aws")))
	registry, err := build(t, snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.Pools[0].Name = "changed"
	snap.Allocations[0].Owner = ipam.Site("changed")
	if registry.Snapshot().Pools[0].Name != "sites" || allocsOf(registry)[0].Owner != ipam.Site("aws") {
		t.Error("IPAM shares memory with the snapshot it was built from")
	}
}
