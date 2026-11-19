package tests

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// referencePlan builds the plan used by the lookup tests: three sites and a few nodes, as in the
// reference lab.
func referencePlan(t *testing.T) *ipam.IPAM {
	t.Helper()
	registry := fresh(t)
	mustAllocate(t, registry, "sites", ipam.Site("aws"))
	mustAllocate(t, registry, "sites", ipam.Site("builder"))
	mustAllocate(t, registry, "overlay", ipam.Node("aws-hub"))
	mustAllocate(t, registry, "overlay", ipam.Node("builder-1"))
	return registry
}

// Done when: OwnerOf(10.42.240.2) is node:builder-1 on the reference plan.
func TestOwnerOf(t *testing.T) {
	registry := referencePlan(t)
	cases := map[string]ipam.Owner{
		"10.42.240.2":  ipam.Node("builder-1"),
		"10.42.240.1":  ipam.Node("aws-hub"),
		"10.42.0.0":    ipam.Site("aws"),
		"10.42.15.255": ipam.Site("aws"),
		"10.42.16.1":   ipam.Site("builder"),
	}
	for addr, want := range cases {
		if got, ok := registry.OwnerOf(netip.MustParseAddr(addr)); !ok || got != want {
			t.Errorf("OwnerOf(%s) = %v, %v; want %v", addr, got, ok, want)
		}
	}
	for _, addr := range []string{"10.42.240.0", "10.42.240.3", "10.42.32.0", "192.168.1.1", "10.43.0.1"} {
		if got, ok := registry.OwnerOf(netip.MustParseAddr(addr)); ok {
			t.Errorf("OwnerOf(%s) = %v, want none", addr, got)
		}
	}
	for _, addr := range []netip.Addr{{}, netip.MustParseAddr("fd00::1"), netip.MustParseAddr("::ffff:10.42.0.1")} {
		if _, ok := registry.OwnerOf(addr); ok {
			t.Errorf("OwnerOf(%v) found something", addr)
		}
	}
}

// TestLookup checks that Lookup returns the live allocation that equals or contains a prefix, and
// nothing otherwise.
func TestLookup(t *testing.T) {
	registry := referencePlan(t)
	cases := []struct {
		prefix string
		owner  string
	}{
		{"10.42.0.0/20", "site:aws"}, // equal
		{"10.42.4.0/24", "site:aws"}, // inside
		{"10.42.4.9/24", "site:aws"}, // host bits ignored
		{"10.42.240.2/32", "node:builder-1"},
		{"10.42.0.0/19", ""},  // wider than the allocation
		{"10.42.32.0/20", ""}, // free
		{"10.42.240.0/32", ""},
	}
	for _, c := range cases {
		got, ok := registry.Lookup(pfx(c.prefix))
		if c.owner == "" && ok || c.owner != "" && (!ok || got.Owner.String() != c.owner) {
			t.Errorf("Lookup(%s) = %v, %v; want %q", c.prefix, got.Owner, ok, c.owner)
		}
	}
	if _, ok := registry.Lookup(netip.Prefix{}); ok {
		t.Error("zero prefix found something")
	}
}

// TestLookupIgnoresQuarantineAndExpiry checks that a quarantined block is not "live" and so is not
// found.
func TestLookupIgnoresQuarantineAndExpiry(t *testing.T) {
	registry, clock := withClock(t, time.Hour)
	got := mustAllocate(t, registry, "overlay", ipam.Node("a"))
	if err := registry.Release(got.Prefix, ipam.Node("a")); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.OwnerOf(got.Prefix.Addr()); ok {
		t.Error("quarantined address still has an owner")
	}
	clock.Advance(time.Hour)
	if len(registry.ByOwner(ipam.Node("a"))) != 0 {
		t.Error("ByOwner returned a released block")
	}
}

// TestByOwner checks that ByOwner lists everything one owner holds, in address order.
func TestByOwner(t *testing.T) {
	registry := referencePlan(t)
	if _, err := registry.Reserve(pfx("10.42.241.0/32"), ipam.Node("aws-hub"), "second"); err != nil {
		t.Fatal(err)
	}
	got := registry.ByOwner(ipam.Node("aws-hub"))
	if len(got) != 2 || got[0].Prefix != pfx("10.42.240.1/32") || got[1].Prefix != pfx("10.42.241.0/32") || got[1].State != ipam.Reserved {
		t.Fatalf("got %+v", got)
	}
	got[0].Owner = ipam.Site("tampered")
	if again := registry.ByOwner(ipam.Node("aws-hub")); again[0].Owner != ipam.Node("aws-hub") {
		t.Error("ByOwner shares memory with the registry")
	}
	if registry.ByOwner(ipam.Node("nobody")) != nil {
		t.Error("want nil for an owner with nothing")
	}
}

// P1-IPAM-004
//
// TestAllocateTransit checks the /30 for a link, the lower address going to the smaller node ID,
// in either argument order.
func TestAllocateTransit(t *testing.T) {
	registry := fresh(t)
	first, err := registry.AllocateTransit("dgx-spark~builder-1") // either order works
	if err != nil {
		t.Fatal(err)
	}
	if first.Allocation.Prefix != pfx("10.42.252.0/30") || first.Allocation.Owner != ipam.Link("builder-1~dgx-spark") {
		t.Errorf("allocation = %+v", first.Allocation)
	}
	want := [2]ipam.TransitEnd{
		{Node: "builder-1", Addr: netip.MustParseAddr("10.42.252.1")},
		{Node: "dgx-spark", Addr: netip.MustParseAddr("10.42.252.2")},
	}
	if first.Ends != want {
		t.Errorf("ends = %+v, want %+v", first.Ends, want)
	}
	again, err := registry.AllocateTransit("builder-1~dgx-spark")
	if err != nil || again != first || len(allocsOf(registry)) != 1 {
		t.Errorf("not idempotent: %+v, %v, %d allocations", again, err, len(allocsOf(registry)))
	}
	second, err := registry.AllocateTransit("aws-hub~builder-1")
	if err != nil || second.Allocation.Prefix != pfx("10.42.252.4/30") ||
		second.Ends[0].Node != "aws-hub" || second.Ends[0].Addr != netip.MustParseAddr("10.42.252.5") {
		t.Errorf("second link = %+v, %v", second, err)
	}
	if owner, _ := registry.OwnerOf(netip.MustParseAddr("10.42.252.2")); owner != ipam.Link("builder-1~dgx-spark") {
		t.Errorf("OwnerOf(10.42.252.2) = %v", owner)
	}
}

// TestAllocateTransitErrors checks that malformed link IDs are refused as INVALID_TOPOLOGY.
func TestAllocateTransitErrors(t *testing.T) {
	registry := fresh(t)
	for _, link := range []types.LinkID{"", "a", "a~", "~b", "a~a", "A~b", "a~b~c", "a b~c"} {
		if _, err := registry.AllocateTransit(link); !types.HasCode(err, types.ErrInvalidTopology) {
			t.Errorf("%q: want INVALID_TOPOLOGY, got %v", link, err)
		}
	}
	snap := snapshotWith()
	snap.Pools[4].ChildBits = 29
	wrong, err := build(t, snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.AllocateTransit("a~b"); !types.HasCode(err, types.ErrInvalidPrefix) {
		t.Errorf("wrong child size: %v", err)
	}
	snap = snapshotWith()
	snap.Pools = snap.Pools[:4]
	missing, err := build(t, snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.AllocateTransit("a~b"); !types.HasCode(err, types.ErrNotFound) {
		t.Errorf("no transit pool: %v", err)
	}
}

// TestAllocateTransitExhaustion checks that the transit pool runs out after 256 links with
// POOL_EXHAUSTED.
func TestAllocateTransitExhaustion(t *testing.T) {
	registry := fresh(t)
	for i := 0; i < 256; i++ {
		link := types.LinkIDOf(types.NodeID("a"+strconv.Itoa(i)), types.NodeID("b"+strconv.Itoa(i)))
		if _, err := registry.AllocateTransit(link); err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	if _, err := registry.AllocateTransit("x~y"); !types.HasCode(err, types.ErrPoolExhausted) {
		t.Errorf("257th link: %v", err)
	}
}
