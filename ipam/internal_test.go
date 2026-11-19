package ipam

// These are the only tests that live inside package ipam. Every other test is in
// ipam/tests and reaches the package through its exported names. These two have
// to corrupt the registry's private state, because they check defences that no
// call to the public API can trigger: a corrupted registry is the whole point.

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/types"
)

var testStart = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

// block builds an allocated Allocation for prefix, pool and owner, created at the fixed test start
// time.
func block(prefix string, pool PoolName, owner Owner) Allocation {
	return Allocation{Prefix: netip.MustParsePrefix(prefix), Pool: pool, Owner: owner, State: Allocated, CreatedAt: testStart}
}

// registryWith builds a registry on the default plan holding allocs.
func registryWith(t *testing.T, quarantine time.Duration, allocs ...Allocation) *IPAM {
	t.Helper()
	pools := []Pool{
		{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
		{Name: "growth", Prefix: netip.MustParsePrefix("10.42.128.0/18"), ChildBits: 20, Reserved: true},
		{Name: "overlay", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
		{Name: "infra", Prefix: netip.MustParsePrefix("10.42.248.0/22"), Reserved: true},
		{Name: "transit", Prefix: netip.MustParsePrefix("10.42.252.0/22"), ChildBits: 30},
	}
	snap := Snapshot{Schema: Schema, Supernet: netip.MustParsePrefix("10.42.0.0/16"), Pools: pools, Allocations: allocs}
	registry, err := New(snap, &types.FixedClock{T: testStart}, quarantine)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// sortAllocs keeps a damaged registry sorted, as it would be live.
func (registry *IPAM) sortAllocs() {
	snap := Snapshot{Allocations: registry.allocs}
	snap.SortAllocations()
	registry.allocs = snap.Allocations
}

// P1-IPAM-033 (R10). R4 forbids any allocation overlapping a site's block, so
// through the public API a node can never sit inside one and the guard cannot
// fire. It is defence in depth against a corrupted registry, so this test
// injects the inner allocations directly to prove the guard works.
func TestReleaseSiteWithChildrenRefused(t *testing.T) {
	registry := registryWith(t, time.Hour, block("10.42.0.0/20", "sites", Site("aws")))
	registry.allocs = append(registry.allocs,
		block("10.42.0.2/32", "sites", Node("hub")),
		block("10.42.0.3/32", "sites", Node("spoke")),
	)
	err := registry.Release(netip.MustParsePrefix("10.42.0.0/20"), Site("aws"))
	if !types.HasCode(err, types.ErrInvalidTopology) {
		t.Fatalf("want INVALID_TOPOLOGY, got %v", err)
	}
	for _, child := range []string{"node:hub", "node:spoke"} {
		if !strings.Contains(err.Error(), child) {
			t.Errorf("error does not list %s: %v", child, err)
		}
	}
	if registry.allocs[0].State == Quarantined {
		t.Error("a refused release still quarantined the block")
	}
	// Once the children are gone the site can be released.
	registry.allocs = registry.allocs[:1]
	if err := registry.Release(netip.MustParsePrefix("10.42.0.0/20"), Site("aws")); err != nil {
		t.Errorf("release without children: %v", err)
	}
}

// Accounting must say so in Problems when the registry is damaged. These states
// can only be reached by corrupting the registry, which is the point of the check.
func TestAccountingReportsDamage(t *testing.T) {
	cases := map[string]struct {
		damage func(*IPAM)
		want   string
	}{
		"overlap": {func(r *IPAM) {
			r.allocs = append(r.allocs, block("10.42.0.5/32", "sites", Node("x")))
			r.sortAllocs()
		}, "overlap"},
		"overlap after a sibling": {func(r *IPAM) {
			r.allocs = append(r.allocs, block("10.42.0.5/32", "sites", Node("x")), block("10.42.0.9/32", "sites", Node("y")))
			r.sortAllocs()
		}, "overlap"},
		"outside supernet": {func(r *IPAM) {
			r.allocs = append(r.allocs, block("192.168.0.0/24", "sites", Site("x")))
			r.sortAllocs()
		}, "outside the supernet"},
		"outside pools": {func(r *IPAM) {
			r.allocs = append(r.allocs, block("10.42.192.0/20", "sites", Site("x")))
			r.sortAllocs()
		}, "outside every pool"},
		"unknown state": {func(r *IPAM) { r.allocs[0].State = "weird" }, "unknown state"},
	}
	for name, c := range cases {
		registry := registryWith(t, time.Hour, block("10.42.0.0/20", "sites", Site("aws")))
		c.damage(registry)
		report := registry.Accounting()
		if joined := strings.Join(report.Problems, "\n"); !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems %q do not mention %q", name, joined, c.want)
		}
	}
}
