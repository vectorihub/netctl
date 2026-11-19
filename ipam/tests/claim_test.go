package tests

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// P1-IPAM-005
//
// TestAllocateSpecificThenAllocateSkipsIt checks that a pinned block is found in its pool and that
// normal allocation then steps around it.
func TestAllocateSpecificThenAllocateSkipsIt(t *testing.T) {
	registry := fresh(t)
	got, err := registry.AllocateSpecific(pfx("10.42.48.0/20"), ipam.Site("new"))
	if err != nil || got.Prefix != pfx("10.42.48.0/20") || got.Pool != "sites" || got.State != ipam.Allocated {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, want := range []string{"10.42.0.0/20", "10.42.16.0/20", "10.42.32.0/20", "10.42.64.0/20"} {
		if next := mustAllocate(t, registry, "sites", ipam.Site("s-"+want)); next.Prefix != pfx(want) {
			t.Fatalf("next = %s, want %s", next.Prefix, want)
		}
	}
}

// P1-IPAM-009 (R1)
//
// TestAllocateSpecificMasksAndWarns checks that a prefix with host bits is cleaned up and a
// warning is recorded.
func TestAllocateSpecificMasksAndWarns(t *testing.T) {
	registry := fresh(t)
	got, err := registry.AllocateSpecific(pfx("10.42.16.5/20"), ipam.Site("gcp"))
	if err != nil || got.Prefix != pfx("10.42.16.0/20") {
		t.Fatalf("got %+v, %v", got, err)
	}
	warnings := registry.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "10.42.16.5/20") || !strings.Contains(warnings[0], "10.42.16.0/20") {
		t.Errorf("warnings = %v", warnings)
	}
	// Clean input adds no warning; the returned slice is a copy.
	if _, err := registry.AllocateSpecific(pfx("10.42.32.0/20"), ipam.Site("dgx")); err != nil {
		t.Fatal(err)
	}
	registry.Warnings()[0] = "tampered"
	if w := registry.Warnings(); len(w) != 1 || w[0] == "tampered" {
		t.Errorf("warnings not isolated: %v", w)
	}
	// A refused call records no warning.
	if _, err := registry.AllocateSpecific(pfx("10.42.16.9/20"), ipam.Site("other")); err == nil {
		t.Fatal("want OVERLAP")
	}
	if len(registry.Warnings()) != 1 {
		t.Errorf("failed call added a warning: %v", registry.Warnings())
	}
}

// P1-IPAM-010 and the PDF's discussion example (P1-GOLD-004 will lock it).
func TestAllocateSpecificInsideExisting(t *testing.T) {
	registry := fresh(t)
	mustAllocate(t, registry, "sites", ipam.Site("aws"))
	_, err := registry.AllocateSpecific(pfx("10.42.0.3/32"), ipam.Node("builder-1"))
	if !types.HasCode(err, types.ErrOverlap) {
		t.Fatalf("want OVERLAP, got %v", err)
	}
	for _, name := range []string{"site:aws", "node:builder-1"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s: %v", name, err)
		}
	}
	if len(allocsOf(registry)) != 1 {
		t.Errorf("a refused claim recorded something: %d allocations", len(allocsOf(registry)))
	}
}

// P1-IPAM-011
//
// TestAllocateSpecificContainingExisting checks that claiming a block that contains an existing
// allocation is an OVERLAP.
func TestAllocateSpecificContainingExisting(t *testing.T) {
	registry := fresh(t, held("10.42.0.5/32", "sites", ipam.Node("x")))
	if _, err := registry.AllocateSpecific(pfx("10.42.0.0/20"), ipam.Site("aws")); !types.HasCode(err, types.ErrOverlap) {
		t.Fatalf("want OVERLAP, got %v", err)
	}
}

// P1-IPAM-034
//
// TestReserveThenAllocateSamePromotes checks that allocating the exact block you reserved promotes
// it instead of failing.
func TestReserveThenAllocateSamePromotes(t *testing.T) {
	registry := fresh(t)
	reserved, err := registry.Reserve(pfx("10.42.16.0/20"), ipam.Site("gcp"), "planned")
	if err != nil || reserved.State != ipam.Reserved || reserved.Note != "planned" {
		t.Fatalf("got %+v, %v", reserved, err)
	}
	promoted, err := registry.AllocateSpecific(pfx("10.42.16.0/20"), ipam.Site("gcp"))
	if err != nil || promoted.State != ipam.Allocated || promoted.Prefix != reserved.Prefix || len(allocsOf(registry)) != 1 {
		t.Fatalf("got %+v, %v, %d allocations", promoted, err, len(allocsOf(registry)))
	}
	// Reserving something already allocated does not demote it.
	again, err := registry.Reserve(pfx("10.42.16.0/20"), ipam.Site("gcp"), "again")
	if err != nil || again.State != ipam.Allocated {
		t.Errorf("Reserve demoted: %+v, %v", again, err)
	}
}

// TestReserveThenAllocateByPoolPromotes checks that allocating from the pool picks up the owner's
// own reservation.
func TestReserveThenAllocateByPoolPromotes(t *testing.T) {
	registry := fresh(t)
	if _, err := registry.Reserve(pfx("10.42.0.0/20"), ipam.Site("aws"), ""); err != nil {
		t.Fatal(err)
	}
	got := mustAllocate(t, registry, "sites", ipam.Site("aws"))
	if got.Prefix != pfx("10.42.0.0/20") || got.State != ipam.Allocated || len(allocsOf(registry)) != 1 {
		t.Errorf("got %+v, %d allocations", got, len(allocsOf(registry)))
	}
}

// P1-IPAM-035
//
// TestReserveBlocksOthersAndNamesHolder checks that a reservation blocks other owners and the
// error names who holds it.
func TestReserveBlocksOthersAndNamesHolder(t *testing.T) {
	registry := fresh(t)
	if _, err := registry.Reserve(pfx("10.42.0.0/20"), ipam.Site("a"), ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"10.42.0.0/20", "10.42.0.0/24", "10.42.0.0/19"} {
		_, err := registry.AllocateSpecific(pfx(p), ipam.Site("b"))
		if !types.HasCode(err, types.ErrReserved) || !strings.Contains(err.Error(), "site:a") {
			t.Errorf("%s: want RESERVED naming site:a, got %v", p, err)
		}
	}
	// Allocate skips a reserved block too.
	if got := mustAllocate(t, registry, "sites", ipam.Site("b")); got.Prefix != pfx("10.42.16.0/20") {
		t.Errorf("Allocate got %s", got.Prefix)
	}
}

// TestClaimInReservedPoolNeedsOverride checks that claiming inside a reserved pool needs an
// override reason.
func TestClaimInReservedPoolNeedsOverride(t *testing.T) {
	registry := fresh(t)
	if _, err := registry.Reserve(pfx("10.42.248.0/24"), ipam.Infra("dns"), "anycast"); !types.HasCode(err, types.ErrReserved) {
		t.Fatalf("want RESERVED, got %v", err)
	}
	got, err := registry.Reserve(pfx("10.42.248.0/24"), ipam.Infra("dns"), "anycast", ipam.WithOverride("ADR 7"))
	if err != nil || got.Pool != "infra" || got.Note != "override: ADR 7; anycast" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := registry.AllocateSpecific(pfx("10.42.128.0/20"), ipam.Site("x")); !types.HasCode(err, types.ErrReserved) {
		t.Errorf("growth without override: %v", err)
	}
	if got, err := registry.AllocateSpecific(pfx("10.42.128.0/20"), ipam.Site("x"), ipam.WithOverride("ok")); err != nil || got.Pool != "growth" {
		t.Errorf("growth with override: %+v, %v", got, err)
	}
}

// TestClaimErrors checks the error code for each way a claim can be invalid (outside the supernet,
// no pool, wrong size, overlap).
func TestClaimErrors(t *testing.T) {
	registry := fresh(t)
	cases := map[string]struct {
		prefix string
		code   types.Code
	}{
		"outside supernet": {"192.168.0.0/24", types.ErrOutOfSupernet},
		"wider supernet":   {"10.0.0.0/8", types.ErrOutOfSupernet},
		"no pool":          {"10.42.192.0/20", types.ErrInvalidPrefix},
		"spans two pools":  {"10.42.0.0/16", types.ErrInvalidPrefix},
		"ipv6":             {"fd00::/64", types.ErrInvalidPrefix},
	}
	for name, c := range cases {
		if _, err := registry.AllocateSpecific(pfx(c.prefix), ipam.Site("x")); !types.HasCode(err, c.code) {
			t.Errorf("%s: want %s, got %v", name, c.code, err)
		}
	}
	if _, err := registry.AllocateSpecific(netipZero(), ipam.Site("x")); !types.HasCode(err, types.ErrInvalidPrefix) {
		t.Errorf("zero prefix: %v", err)
	}
	if _, err := registry.AllocateSpecific(pfx("10.42.0.0/20"), ipam.Owner{}); err == nil {
		t.Error("zero owner accepted")
	}
	if len(allocsOf(registry)) != 0 {
		t.Errorf("%d allocations after failures", len(allocsOf(registry)))
	}
}

// TestClaimOverlapsQuarantined checks that a quarantined block cannot be claimed by anyone until
// it expires.
func TestClaimOverlapsQuarantined(t *testing.T) {
	quarantined := held("10.42.0.0/20", "sites", ipam.Site("old"))
	quarantined.State = ipam.Quarantined
	quarantined.QuarantineUntil = t0.Add(1)
	registry := fresh(t, quarantined)
	if _, err := registry.AllocateSpecific(pfx("10.42.0.0/20"), ipam.Site("old")); !types.HasCode(err, types.ErrOverlap) {
		t.Errorf("even the old owner must wait: %v", err)
	}
}

// netipZero returns the zero netip.Prefix, which is how the tests write "no prefix at all".
func netipZero() netip.Prefix { return netip.Prefix{} }
