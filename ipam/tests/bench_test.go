package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// P1-IPAM-PERF-001: 2,000 overlay allocations in under 200 ms.
// Run with: make bench
func BenchmarkAllocateOverlay2000(b *testing.B) {
	owners := make([]ipam.Owner, 2000)
	for i := range owners {
		owners[i] = ipam.Node(fmt.Sprintf("node-%04d", i))
	}
	best := time.Duration(1<<63 - 1)
	for b.Loop() {
		registry, err := ipam.New(snapshotWith(), &types.FixedClock{T: t0}, time.Hour)
		if err != nil {
			b.Fatal(err)
		}
		start := time.Now()
		for _, owner := range owners {
			if _, err := registry.Allocate("overlay", owner); err != nil {
				b.Fatal(err)
			}
		}
		best = min(best, time.Since(start))
	}
	// Judge the best run, so a busy machine does not fail a correct implementation.
	if best > 200*time.Millisecond {
		b.Fatalf("2,000 overlay allocations took %s at best, want under 200ms", best)
	}
}

// P1-IPAM-PERF-002: accounting with 5,000 allocations in under 50 ms.
func BenchmarkAccounting5000(b *testing.B) {
	snap := ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: pfx("10.0.0.0/8"),
		Pools:    []ipam.Pool{{Name: "overlay", Prefix: pfx("10.0.0.0/16"), ChildBits: 32}},
	}
	registry, err := ipam.New(snap, &types.FixedClock{T: t0}, time.Hour)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		if _, err := registry.Allocate("overlay", ipam.Node(fmt.Sprintf("node-%04d", i))); err != nil {
			b.Fatal(err)
		}
	}
	best := time.Duration(1<<63 - 1)
	for b.Loop() {
		start := time.Now()
		report := registry.Accounting()
		best = min(best, time.Since(start))
		if len(report.Problems) != 0 || report.Allocated != 5000 {
			b.Fatalf("%+v", report.Problems)
		}
	}
	if best > 50*time.Millisecond {
		b.Fatalf("accounting 5,000 allocations took %s at best, want under 50ms", best)
	}
}
