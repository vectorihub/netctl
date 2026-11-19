package tests

import (
	"testing"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// withClock builds a registry with a controllable clock and the given quarantine, and returns
// both.
func withClock(t *testing.T, quarantine time.Duration, allocs ...ipam.Allocation) (*ipam.IPAM, *types.FixedClock) {
	t.Helper()
	clock := &types.FixedClock{T: t0}
	registry, err := ipam.New(snapshotWith(allocs...), clock, quarantine)
	if err != nil {
		t.Fatal(err)
	}
	return registry, clock
}

// P1-IPAM-030 and -031
//
// TestReleaseQuarantinesThenFrees checks that a released block is quarantined, then becomes free
// once the quarantine passes.
func TestReleaseQuarantinesThenFrees(t *testing.T) {
	registry, clock := withClock(t, ipam.DefaultQuarantine)
	first := mustAllocate(t, registry, "overlay", ipam.Node("a"))
	if err := registry.Release(first.Prefix, ipam.Node("a")); err != nil {
		t.Fatal(err)
	}
	if got := mustAllocate(t, registry, "overlay", ipam.Node("b")); got.Prefix != pfx("10.42.240.2/32") {
		t.Fatalf("new node got %s, want 10.42.240.2/32 (not the released one)", got.Prefix)
	}
	clock.Advance(ipam.DefaultQuarantine - time.Second)
	if got := mustAllocate(t, registry, "overlay", ipam.Node("c")); got.Prefix != pfx("10.42.240.3/32") {
		t.Fatalf("1s early: got %s, want 10.42.240.3/32", got.Prefix)
	}
	clock.Advance(time.Second)
	if got := mustAllocate(t, registry, "overlay", ipam.Node("d")); got.Prefix != pfx("10.42.240.1/32") {
		t.Fatalf("after quarantine got %s, want 10.42.240.1/32", got.Prefix)
	}
}

// TestReleaseRecordsQuarantineEnd checks that the quarantine end time is the release time plus the
// quarantine period.
func TestReleaseRecordsQuarantineEnd(t *testing.T) {
	registry, _ := withClock(t, time.Hour)
	got := mustAllocate(t, registry, "overlay", ipam.Node("a"))
	if err := registry.Release(got.Prefix, ipam.Node("a")); err != nil {
		t.Fatal(err)
	}
	released := allocsOf(registry)[0]
	if released.State != ipam.Quarantined || !released.QuarantineUntil.Equal(t0.Add(time.Hour)) || released.Owner != ipam.Node("a") {
		t.Errorf("released = %+v", released)
	}
	if _, ok := registry.Lookup(got.Prefix); ok {
		t.Error("a quarantined block was found by Lookup")
	}
}

// TestReleaseZeroQuarantineFreesAtOnce checks that a zero quarantine frees the block immediately.
func TestReleaseZeroQuarantineFreesAtOnce(t *testing.T) {
	registry, _ := withClock(t, 0)
	got := mustAllocate(t, registry, "overlay", ipam.Node("a"))
	if err := registry.Release(got.Prefix, ipam.Node("a")); err != nil {
		t.Fatal(err)
	}
	if again := mustAllocate(t, registry, "overlay", ipam.Node("b")); again.Prefix != got.Prefix {
		t.Errorf("got %s, want %s", again.Prefix, got.Prefix)
	}
}

// P1-IPAM-032 (R9)
//
// TestReleaseNotHeldIsNotFound checks that releasing a block you do not hold, or that is not
// allocated, is NOT_FOUND.
func TestReleaseNotHeldIsNotFound(t *testing.T) {
	registry, _ := withClock(t, time.Hour)
	mine := mustAllocate(t, registry, "overlay", ipam.Node("a"))
	cases := map[string]struct {
		prefix string
		owner  ipam.Owner
	}{
		"someone else's": {"10.42.240.1/32", ipam.Node("b")},
		"nothing there":  {"10.42.240.9/32", ipam.Node("a")},
		"too wide":       {"10.42.240.0/24", ipam.Node("a")},
	}
	for name, c := range cases {
		if err := registry.Release(pfx(c.prefix), c.owner); !types.HasCode(err, types.ErrNotFound) {
			t.Errorf("%s: want NOT_FOUND, got %v", name, err)
		}
	}
	// Releasing twice: the second finds it already quarantined.
	if err := registry.Release(mine.Prefix, ipam.Node("a")); err != nil {
		t.Fatal(err)
	}
	if err := registry.Release(mine.Prefix, ipam.Node("a")); !types.HasCode(err, types.ErrNotFound) {
		t.Errorf("second release: %v", err)
	}
	if err := registry.Release(mine.Prefix, ipam.Owner{}); err == nil {
		t.Error("zero owner accepted")
	}
}

// TestReleaseNotHeldChangesNothing checks that a failed release leaves every allocation as it was.
func TestReleaseNotHeldChangesNothing(t *testing.T) {
	registry, _ := withClock(t, time.Hour)
	mustAllocate(t, registry, "overlay", ipam.Node("a"))
	before := append([]ipam.Allocation(nil), allocsOf(registry)...)
	_ = registry.Release(pfx("10.42.240.1/32"), ipam.Node("b"))
	if len(before) != len(allocsOf(registry)) || before[0] != allocsOf(registry)[0] {
		t.Errorf("state changed: %+v -> %+v", before, allocsOf(registry))
	}
}

// TestReleaseMasksPrefix checks that a prefix with host bits set still releases the right block.
func TestReleaseMasksPrefix(t *testing.T) {
	registry, _ := withClock(t, time.Hour)
	site := mustAllocate(t, registry, "sites", ipam.Site("aws"))
	if err := registry.Release(pfx("10.42.0.7/20"), ipam.Site("aws")); err != nil {
		t.Fatalf("release of %s given with host bits: %v", site.Prefix, err)
	}
}
