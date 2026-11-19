package tests

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/ipam"
)

// P1-IPAM-020
//
// TestAccountingFresh checks that an empty plan reports every address as free and nothing else.
func TestAccountingFresh(t *testing.T) {
	report := fresh(t).Accounting()
	if report.Total != 65536 || report.Free != 65536 || report.Allocated+report.Reserved+report.Quarantined != 0 {
		t.Fatalf("%+v", report)
	}
	if len(report.FreeBlocks) != 1 || report.FreeBlocks[0] != pfx("10.42.0.0/16") {
		t.Errorf("free blocks = %v", report.FreeBlocks)
	}
	if report.Problems == nil || len(report.Problems) != 0 {
		t.Errorf("problems = %#v, want empty non-nil", report.Problems)
	}
	if len(report.Pools) != 5 || report.Pools[0].Name != "sites" || report.Pools[0].Free != 32768 {
		t.Errorf("pools = %+v", report.Pools)
	}
}

// P1-IPAM-021: the lab-A1 plan, with the build guide's reference numbers.
func TestAccountingLabA1(t *testing.T) {
	registry := fresh(t)
	for _, name := range []string{"aws", "builder", "dgx"} {
		mustAllocate(t, registry, "sites", ipam.Site(name))
	}
	for _, name := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		mustAllocate(t, registry, "overlay", ipam.Node(name))
	}
	if _, err := registry.AllocateTransit("builder-1~dgx-spark"); err != nil {
		t.Fatal(err)
	}
	report := registry.Accounting()
	if report.Allocated != 12295 || report.Free != 53241 || len(report.FreeBlocks) != 24 || len(report.Problems) != 0 {
		t.Fatalf("allocated %d free %d blocks %d problems %v", report.Allocated, report.Free, len(report.FreeBlocks), report.Problems)
	}
	if report.Reserved != 0 || report.Quarantined != 0 {
		t.Errorf("reserved %d quarantined %d", report.Reserved, report.Quarantined)
	}
	// The unassigned space the plan says must show as free (P1-IPAM-021).
	for _, want := range []netip.Prefix{pfx("10.42.192.0/19"), pfx("10.42.224.0/20")} {
		found := false
		for _, block := range report.FreeBlocks {
			found = found || block == want
		}
		if !found {
			t.Errorf("%s is not in the free blocks %v", want, report.FreeBlocks)
		}
	}
	want := map[ipam.PoolName][2]uint64{"sites": {12288, 32768 - 12288}, "overlay": {3, 2048 - 3}, "transit": {4, 1024 - 4}, "growth": {0, 16384}}
	for _, usage := range report.Pools {
		if w, ok := want[usage.Name]; ok && (usage.Allocated != w[0] || usage.Free != w[1]) {
			t.Errorf("pool %s: allocated %d free %d, want %v", usage.Name, usage.Allocated, usage.Free, w)
		}
	}
}

// TestAccountingCountsEveryState checks that allocated, reserved and quarantined space are each
// counted once and the sums balance.
func TestAccountingCountsEveryState(t *testing.T) {
	quarantined := held("10.42.240.1/32", "overlay", ipam.Node("old"))
	quarantined.State = ipam.Quarantined
	quarantined.QuarantineUntil = t0.Add(time.Hour)
	registry := fresh(t, held("10.42.0.0/20", "sites", ipam.Site("aws")), quarantined)
	if _, err := registry.Reserve(pfx("10.42.16.0/20"), ipam.Site("gcp"), ""); err != nil {
		t.Fatal(err)
	}
	report := registry.Accounting()
	if report.Allocated != 4096 || report.Reserved != 4096 || report.Quarantined != 1 || report.Free != 65536-8193 {
		t.Errorf("%+v", report)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems: %v", report.Problems)
	}
	if usage := report.Pools[0]; usage.Allocated != 4096 || usage.Reserved != 4096 || usage.Free != 32768-8192 {
		t.Errorf("sites usage = %+v", usage)
	}
	if usage := report.Pools[2]; usage.Quarantined != 1 || usage.Free != 2047 {
		t.Errorf("overlay usage = %+v", usage)
	}
}

// Accounting moves the clock too: expired quarantine turns into free space.
func TestAccountingExpiresQuarantine(t *testing.T) {
	registry, clock := withClock(t, time.Hour)
	got := mustAllocate(t, registry, "overlay", ipam.Node("a"))
	if err := registry.Release(got.Prefix, ipam.Node("a")); err != nil {
		t.Fatal(err)
	}
	if report := registry.Accounting(); report.Quarantined != 1 || report.Free != 65535 {
		t.Errorf("during: %+v", report)
	}
	clock.Advance(time.Hour)
	if report := registry.Accounting(); report.Quarantined != 0 || report.Free != 65536 {
		t.Errorf("after: %+v", report)
	}
}

// P1-IPAM-022 (small version; the 5,000-op property test is V22): after every
// operation Problems is empty and the four kinds add up.
func TestAccountingAlwaysAddsUp(t *testing.T) {
	registry, clock := withClock(t, time.Hour)
	check := func(step string) {
		t.Helper()
		r := registry.Accounting()
		if len(r.Problems) != 0 || r.Allocated+r.Reserved+r.Quarantined+r.Free != r.Total {
			t.Fatalf("after %s: %+v", step, r)
		}
	}
	for i := 0; i < 8; i++ {
		mustAllocate(t, registry, "sites", ipam.Site(fmt.Sprintf("s%d", i)))
		check("allocate site")
	}
	for i := 0; i < 40; i++ {
		mustAllocate(t, registry, "overlay", ipam.Node(fmt.Sprintf("n%d", i)))
	}
	check("overlay")
	if _, err := registry.Reserve(pfx("10.42.250.0/24"), ipam.Infra("dns"), "", ipam.WithOverride("x")); err != nil {
		t.Fatal(err)
	}
	check("reserve")
	for i := 0; i < 40; i += 3 {
		if err := registry.Release(netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 42, 240, byte(i + 1)}), 32), ipam.Node(fmt.Sprintf("n%d", i))); err != nil {
			t.Fatal(err)
		}
		check("release")
	}
	clock.Advance(2 * time.Hour)
	check("tick")
	for i := 0; i < 5; i++ {
		mustAllocate(t, registry, "overlay", ipam.Node(fmt.Sprintf("late%d", i)))
	}
	check("reuse")
}

// A rough guard for PERF-002 (accounting at 5,000 allocations < 50 ms); the
// real benchmark arrives in V22.
func TestAccountingAtScale(t *testing.T) {
	snap := ipam.Snapshot{Schema: ipam.Schema, Supernet: pfx("10.0.0.0/8"),
		Pools: []ipam.Pool{{Name: "all", Prefix: pfx("10.0.0.0/8"), ChildBits: 24}}}
	children, err := cidr.Children(pfx("10.0.0.0/8"), 24)
	if err != nil {
		t.Fatal(err)
	}
	for i, child := range children {
		if i >= 5000 {
			break
		}
		snap.Allocations = append(snap.Allocations, held(child.String(), "all", ipam.Node(fmt.Sprintf("n%d", i))))
	}
	registry, err := build(t, snap)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	report := registry.Accounting()
	if len(report.Problems) != 0 || report.Allocated != 5000*256 {
		t.Fatalf("%+v", report.Problems)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("accounting 5,000 allocations took %s", took)
	}
}
