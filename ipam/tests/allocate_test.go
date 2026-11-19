package tests

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// fresh builds a registry on the default plan that already holds allocs, failing the test if the
// plan is refused.
func fresh(t *testing.T, allocs ...ipam.Allocation) *ipam.IPAM {
	t.Helper()
	registry, err := build(t, snapshotWith(allocs...))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// mustAllocate allocates from pool for owner and fails the test on error; it returns the
// allocation.
func mustAllocate(t *testing.T, registry *ipam.IPAM, pool ipam.PoolName, owner ipam.Owner, opts ...ipam.Option) ipam.Allocation {
	t.Helper()
	got, err := registry.Allocate(pool, owner, opts...)
	if err != nil {
		t.Fatalf("Allocate(%s, %s): %v", pool, owner, err)
	}
	return got
}

// P1-IPAM-001
//
// TestAllocateThreeSitesInOrder checks that the first three sites get the first three /20s of the
// sites pool.
func TestAllocateThreeSitesInOrder(t *testing.T) {
	registry := fresh(t)
	for i, c := range []struct {
		owner ipam.Owner
		want  string
	}{
		{ipam.Site("aws"), "10.42.0.0/20"},
		{ipam.Site("builder"), "10.42.16.0/20"},
		{ipam.Site("dgx-spark"), "10.42.32.0/20"},
	} {
		got := mustAllocate(t, registry, "sites", c.owner)
		if got.Prefix != pfx(c.want) || got.Owner != c.owner || got.Pool != "sites" ||
			got.State != ipam.Allocated || !got.CreatedAt.Equal(t0) {
			t.Errorf("allocation %d = %+v, want %s", i, got, c.want)
		}
	}
}

// P1-IPAM-002 and R6: asking again never moves anyone and records nothing new.
func TestAllocateIsIdempotent(t *testing.T) {
	registry := fresh(t)
	first := mustAllocate(t, registry, "sites", ipam.Site("aws"))
	mustAllocate(t, registry, "sites", ipam.Site("builder"))
	again := mustAllocate(t, registry, "sites", ipam.Site("aws"))
	if again != first {
		t.Errorf("second call moved aws: %+v then %+v", first, again)
	}
	if len(allocsOf(registry)) != 2 {
		t.Errorf("%d allocations recorded, want 2", len(allocsOf(registry)))
	}
}

// TestAllocateIdempotentPromotesReservation checks that asking again returns the same block, and
// that a reserved block becomes allocated.
func TestAllocateIdempotentPromotesReservation(t *testing.T) {
	reserved := held("10.42.0.0/20", "sites", ipam.Site("aws"))
	reserved.State = ipam.Reserved
	registry := fresh(t, reserved)
	got := mustAllocate(t, registry, "sites", ipam.Site("aws"))
	if got.Prefix != pfx("10.42.0.0/20") || got.State != ipam.Allocated || len(allocsOf(registry)) != 1 {
		t.Errorf("got %+v, %d allocations", got, len(allocsOf(registry)))
	}
}

// P1-IPAM-003 (R5)
//
// TestAllocateOverlaySkipsFirst checks that overlay addresses start at .1 because the first child
// of the pool is never handed out.
func TestAllocateOverlaySkipsFirst(t *testing.T) {
	registry := fresh(t)
	for i, want := range []string{"10.42.240.1/32", "10.42.240.2/32", "10.42.240.3/32"} {
		got := mustAllocate(t, registry, "overlay", ipam.Node(fmt.Sprintf("n%d", i)))
		if got.Prefix != pfx(want) {
			t.Errorf("overlay %d = %s, want %s", i, got.Prefix, want)
		}
	}
}

// P1-IPAM-004
//
// TestAllocateFirstTransitLink checks that the first link gets the first /30 of the transit pool.
func TestAllocateFirstTransitLink(t *testing.T) {
	got := mustAllocate(t, fresh(t), "transit", ipam.Link("builder-1~dgx-spark"))
	if got.Prefix != pfx("10.42.252.0/30") {
		t.Errorf("got %s, want 10.42.252.0/30", got.Prefix)
	}
}

// P1-IPAM-006 (R7)
//
// TestAllocateReservedNeedsOverride checks that a reserved pool refuses an allocation without a
// non-empty override reason.
func TestAllocateReservedNeedsOverride(t *testing.T) {
	registry := fresh(t)
	for _, option := range []ipam.Option{nil, ipam.WithOverride(""), ipam.WithNote("just a note")} {
		var opts []ipam.Option
		if option != nil {
			opts = append(opts, option)
		}
		_, err := registry.Allocate("growth", ipam.Site("emea"), opts...)
		if !types.HasCode(err, types.ErrReserved) {
			t.Errorf("want RESERVED, got %v", err)
		}
	}
	if len(allocsOf(registry)) != 0 {
		t.Errorf("a refused call recorded %d allocations", len(allocsOf(registry)))
	}
}

// P1-IPAM-007
//
// TestAllocateReservedWithOverride checks that an override reason lets an allocation from a
// reserved pool through and is recorded.
func TestAllocateReservedWithOverride(t *testing.T) {
	registry := fresh(t)
	got := mustAllocate(t, registry, "growth", ipam.Site("emea"), ipam.WithOverride("approved in CHG-12"))
	if got.Prefix != pfx("10.42.128.0/20") {
		t.Errorf("prefix = %s, want 10.42.128.0/20", got.Prefix)
	}
	if got.Note != "override: approved in CHG-12" {
		t.Errorf("note = %q", got.Note)
	}
	both := mustAllocate(t, registry, "growth", ipam.Site("apac"), ipam.WithOverride("why"), ipam.WithNote("extra"))
	if both.Note != "override: why; extra" {
		t.Errorf("note = %q", both.Note)
	}
	plain := mustAllocate(t, registry, "sites", ipam.Site("aws"), ipam.WithNote("hello"))
	if plain.Note != "hello" {
		t.Errorf("note = %q", plain.Note)
	}
}

// P1-IPAM-008
//
// TestAllocateExhaustion checks that a full pool returns POOL_EXHAUSTED and that releasing a block
// does not hand it out again during quarantine.
func TestAllocateExhaustion(t *testing.T) {
	registry := fresh(t)
	var eight []ipam.Allocation
	for i := 0; i < 8; i++ {
		eight = append(eight, mustAllocate(t, registry, "sites", ipam.Site(fmt.Sprintf("s%d", i))))
	}
	if last := eight[7].Prefix; last != pfx("10.42.112.0/20") {
		t.Errorf("eighth site = %s", last)
	}
	_, err := registry.Allocate("sites", ipam.Site("ninth"))
	if !types.HasCode(err, types.ErrPoolExhausted) {
		t.Fatalf("want POOL_EXHAUSTED, got %v", err)
	}
	for i, want := range eight {
		if got := mustAllocate(t, registry, "sites", ipam.Site(fmt.Sprintf("s%d", i))); got != want {
			t.Errorf("site s%d changed: %+v -> %+v", i, want, got)
		}
	}
	if len(allocsOf(registry)) != 8 {
		t.Errorf("%d allocations, want 8", len(allocsOf(registry)))
	}
}

// TestAllocateFillsGapsAndRoutesAroundOtherSizes checks that the lowest free child is chosen,
// skipping children that hold a block of another size.
func TestAllocateFillsGapsAndRoutesAroundOtherSizes(t *testing.T) {
	registry := fresh(t,
		held("10.42.16.0/20", "sites", ipam.Site("pinned")), // second child taken
		held("10.42.32.7/32", "sites", ipam.Node("odd")),    // a /32 inside the third child
	)
	first := mustAllocate(t, registry, "sites", ipam.Site("a"))
	second := mustAllocate(t, registry, "sites", ipam.Site("b"))
	if first.Prefix != pfx("10.42.0.0/20") || second.Prefix != pfx("10.42.48.0/20") {
		t.Errorf("got %s and %s, want 10.42.0.0/20 and 10.42.48.0/20", first.Prefix, second.Prefix)
	}
}

// TestAllocateErrors checks the error code for an unknown pool, an invalid owner and similar bad
// requests.
func TestAllocateErrors(t *testing.T) {
	registry := fresh(t)
	if _, err := registry.Allocate("nope", ipam.Site("aws")); !types.HasCode(err, types.ErrNotFound) {
		t.Errorf("unknown pool: %v", err)
	}
	if _, err := registry.Allocate("infra", ipam.Site("aws"), ipam.WithOverride("x")); !types.HasCode(err, types.ErrInvalidPrefix) {
		t.Errorf("pool without child size: %v", err)
	}
	for _, owner := range []ipam.Owner{{}, {Kind: "host", ID: "x"}, {Kind: ipam.KindSite}} {
		if _, err := registry.Allocate("sites", owner); err == nil {
			t.Errorf("owner %#v accepted", owner)
		}
	}
	if len(allocsOf(registry)) != 0 {
		t.Errorf("failed calls recorded %d allocations", len(allocsOf(registry)))
	}
}

// A quarantined block is not free until its quarantine ends; the clock decides.
func TestAllocateRespectsQuarantineAndExpiry(t *testing.T) {
	released := held("10.42.0.0/20", "sites", ipam.Site("old"))
	released.State = ipam.Quarantined
	released.QuarantineUntil = t0.Add(time.Hour)
	clock := &types.FixedClock{T: t0}
	registry, err := ipam.New(snapshotWith(released), clock, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustAllocate(t, registry, "sites", ipam.Site("new")); got.Prefix != pfx("10.42.16.0/20") {
		t.Errorf("during quarantine got %s, want 10.42.16.0/20", got.Prefix)
	}
	// The old owner is not "holding" it either: they get a fresh block.
	if got := mustAllocate(t, registry, "sites", ipam.Site("old")); got.Prefix != pfx("10.42.32.0/20") {
		t.Errorf("old owner got %s, want 10.42.32.0/20", got.Prefix)
	}
	clock.Advance(time.Hour) // quarantine ends exactly now
	if got := mustAllocate(t, registry, "sites", ipam.Site("third")); got.Prefix != pfx("10.42.0.0/20") {
		t.Errorf("after quarantine got %s, want 10.42.0.0/20", got.Prefix)
	}
}

// R-determinism: same snapshot and calls, same answers, whatever the snapshot order.
func TestAllocateDeterministic(t *testing.T) {
	run := func(allocs ...ipam.Allocation) []ipam.Allocation {
		registry := fresh(t, allocs...)
		var out []ipam.Allocation
		for i := 0; i < 5; i++ {
			out = append(out, mustAllocate(t, registry, "sites", ipam.Site(fmt.Sprintf("s%d", i))))
		}
		return out
	}
	one := held("10.42.16.0/20", "sites", ipam.Site("x"))
	two := held("10.42.48.0/20", "sites", ipam.Site("y"))
	if a, b := run(one, two), run(two, one); !reflect.DeepEqual(a, b) {
		t.Errorf("order of snapshot changed the result:\n%v\n%v", a, b)
	}
}

// 500 machines, as in P1-E2E-101, never overlap and stay in address order.
func TestAllocateManyOverlayAddresses(t *testing.T) {
	registry := fresh(t)
	for i := 0; i < 500; i++ {
		got := mustAllocate(t, registry, "overlay", ipam.Node(fmt.Sprintf("n%d", i)))
		if want := pfx(fmt.Sprintf("10.42.%d.%d/32", 240+(i+1)/256, (i+1)%256)); got.Prefix != want {
			t.Fatalf("node %d = %s, want %s", i, got.Prefix, want)
		}
	}
	for i := 1; i < len(allocsOf(registry)); i++ {
		before, after := allocsOf(registry)[i-1].Prefix, allocsOf(registry)[i].Prefix
		if cidr.Compare(before, after) >= 0 || cidr.Overlaps(before, after) {
			t.Fatalf("allocations %s and %s are out of order or overlap", before, after)
		}
	}
	if _, err := registry.Allocate("overlay", ipam.Node("n-last")); err != nil {
		t.Errorf("overlay should still have room: %v", err)
	}
	if strings.Contains(fmt.Sprint(allocsOf(registry)[0].Prefix), "240.0/") {
		t.Error("10.42.240.0 was handed out")
	}
}

// AllocateSpecific must see an overlap with the neighbour on either side, a
// containing block, or a block inside it, and nothing else.
func TestClaimOverlapNeighbours(t *testing.T) {
	existing := []ipam.Allocation{
		held("10.42.0.0/20", "sites", ipam.Site("a")),
		held("10.42.32.0/20", "sites", ipam.Site("c")),
	}
	cases := []struct {
		prefix string
		owner  string // "" means the claim succeeds
	}{
		{"10.42.0.0/19", "site:a"},  // contains the first
		{"10.42.0.0/20", "site:a"},  // equal
		{"10.42.4.0/24", "site:a"},  // inside
		{"10.42.16.0/20", ""},       // the gap
		{"10.42.31.255/32", ""},     // last address before c
		{"10.42.32.0/32", "site:c"}, // first address of c
		{"10.42.47.255/32", "site:c"},
		{"10.42.48.0/20", ""},
		{"10.42.32.0/19", "site:c"}, // contains c
	}
	for _, c := range cases {
		registry := fresh(t, existing...)
		_, err := registry.AllocateSpecific(pfx(c.prefix), ipam.Node("probe"))
		if c.owner == "" {
			if err != nil {
				t.Errorf("claim of %s: %v", c.prefix, err)
			}
			continue
		}
		if !types.HasCode(err, types.ErrOverlap) || !strings.Contains(err.Error(), c.owner) {
			t.Errorf("claim of %s: want OVERLAP naming %s, got %v", c.prefix, c.owner, err)
		}
	}
}
