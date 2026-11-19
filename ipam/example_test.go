package ipam_test

// This file holds the runnable examples shown on the package documentation.
// They live beside the code, not in tests/, because pkg.go.dev only shows
// examples from the package's own directory. They use only the exported API.

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// exampleSnapshot returns the starting state used by the examples: a /16
// supernet with a pool of /20 site blocks, a pool of /32 machine addresses,
// a pool of /30 link networks and a reserved pool for shared infrastructure.
func exampleSnapshot() ipam.Snapshot {
	return ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: netip.MustParsePrefix("10.42.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
			{Name: "nodes", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
			{Name: "infra", Prefix: netip.MustParsePrefix("10.42.248.0/22"), Reserved: true},
			{Name: ipam.TransitPool, Prefix: netip.MustParsePrefix("10.42.252.0/22"), ChildBits: 30},
		},
	}
}

// exampleClock returns a clock fixed at a known time, so example output is the
// same on every run.
func exampleClock() *types.FixedClock {
	return &types.FixedClock{T: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// newExampleRegistry builds a registry over exampleSnapshot, panicking on
// error because the snapshot is known to be valid.
func newExampleRegistry(clock types.Clock) *ipam.IPAM {
	registry, err := ipam.New(exampleSnapshot(), clock, ipam.DefaultQuarantine)
	if err != nil {
		panic(err)
	}
	return registry
}

// Example walks through the usual life of a registry: create it, hand out
// addresses, look one up, and save the state for next time.
func Example() {
	registry, err := ipam.New(exampleSnapshot(), exampleClock(), ipam.DefaultQuarantine)
	if err != nil {
		fmt.Println(err)
		return
	}

	site, _ := registry.Allocate("sites", ipam.Site("london"))
	web, _ := registry.Allocate("nodes", ipam.Node("web-1"))
	db, _ := registry.Allocate("nodes", ipam.Node("db-1"))
	fmt.Println(site.Owner, site.Prefix)
	fmt.Println(web.Owner, web.Prefix)
	fmt.Println(db.Owner, db.Prefix)

	owner, _ := registry.OwnerOf(netip.MustParseAddr("10.42.240.2"))
	fmt.Println("10.42.240.2 belongs to", owner)

	// Save the state; pass it back to ipam.New next time.
	data, _ := ipam.Marshal(registry.Snapshot())
	restored, _ := ipam.Unmarshal(data)
	fmt.Println(len(restored.Allocations), "allocations saved")
	// Output:
	// site:london 10.42.0.0/20
	// node:web-1 10.42.240.1/32
	// node:db-1 10.42.240.2/32
	// 10.42.240.2 belongs to node:db-1
	// 3 allocations saved
}

// ExampleNew shows that New refuses an invalid snapshot and reports every
// problem at once, each with a code that types.HasCode can match.
func ExampleNew() {
	snapshot := exampleSnapshot()
	snapshot.Allocations = []ipam.Allocation{
		{Prefix: netip.MustParsePrefix("10.42.0.0/20"), Pool: "sites", Owner: ipam.Site("london"), State: ipam.Allocated},
		{Prefix: netip.MustParsePrefix("10.42.0.0/24"), Pool: "sites", Owner: ipam.Site("paris"), State: ipam.Allocated},
	}

	_, err := ipam.New(snapshot, exampleClock(), ipam.DefaultQuarantine)
	fmt.Println(types.HasCode(err, types.ErrOverlap))
	// Output: true
}

// ExampleIPAM_Allocate shows that Allocate hands out the lowest free block and
// returns the same block when the same owner asks again.
func ExampleIPAM_Allocate() {
	registry := newExampleRegistry(exampleClock())

	first, _ := registry.Allocate("sites", ipam.Site("london"))
	second, _ := registry.Allocate("sites", ipam.Site("paris"))
	again, _ := registry.Allocate("sites", ipam.Site("london"))

	fmt.Println(first.Prefix, second.Prefix, again.Prefix)
	// Output: 10.42.0.0/20 10.42.16.0/20 10.42.0.0/20
}

// ExampleIPAM_AllocateSpecific shows claiming an exact prefix, and the OVERLAP
// error when it collides with a block someone else holds.
func ExampleIPAM_AllocateSpecific() {
	registry := newExampleRegistry(exampleClock())

	_, err := registry.AllocateSpecific(netip.MustParsePrefix("10.42.32.0/20"), ipam.Site("berlin"))
	fmt.Println(err)

	_, err = registry.AllocateSpecific(netip.MustParsePrefix("10.42.40.0/24"), ipam.Site("madrid"))
	fmt.Println(types.HasCode(err, types.ErrOverlap))
	// Output:
	// <nil>
	// true
}

// ExampleIPAM_AllocateTransit shows numbering a direct link between two nodes.
// The node whose ID sorts first gets the lower address.
func ExampleIPAM_AllocateTransit() {
	registry := newExampleRegistry(exampleClock())

	transit, err := registry.AllocateTransit(types.LinkIDOf("web-1", "db-1"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(transit.Allocation.Owner, transit.Allocation.Prefix)
	for _, end := range transit.Ends {
		fmt.Println(end.Node, end.Addr)
	}
	// Output:
	// link:db-1~web-1 10.42.252.0/30
	// db-1 10.42.252.1
	// web-1 10.42.252.2
}

// ExampleIPAM_Reserve shows holding a prefix in a reserved pool. Reserved pools
// need WithOverride and a reason, which is kept in the allocation's note.
func ExampleIPAM_Reserve() {
	registry := newExampleRegistry(exampleClock())

	_, err := registry.Reserve(netip.MustParsePrefix("10.42.248.0/24"), ipam.Infra("dns"), "DNS anycast")
	fmt.Println(types.HasCode(err, types.ErrReserved))

	held, err := registry.Reserve(netip.MustParsePrefix("10.42.248.0/24"), ipam.Infra("dns"), "DNS anycast",
		ipam.WithOverride("approved by network team"))
	fmt.Println(err)
	fmt.Println(held.Owner, held.Prefix, held.State)
	// Output:
	// true
	// <nil>
	// infra:dns 10.42.248.0/24 reserved
}

// ExampleIPAM_Release shows that a released address is quarantined: it is not
// handed out again until the quarantine period has passed.
func ExampleIPAM_Release() {
	clock := exampleClock()
	registry := newExampleRegistry(clock)

	old, _ := registry.Allocate("nodes", ipam.Node("web-1"))
	_ = registry.Release(old.Prefix, ipam.Node("web-1"))

	next, _ := registry.Allocate("nodes", ipam.Node("web-2"))
	fmt.Println("during quarantine:", next.Prefix)

	clock.Advance(ipam.DefaultQuarantine)
	reused, _ := registry.Allocate("nodes", ipam.Node("web-3"))
	fmt.Println("after quarantine:", reused.Prefix)
	// Output:
	// during quarantine: 10.42.240.2/32
	// after quarantine: 10.42.240.1/32
}

// ExampleIPAM_Lookup shows finding the allocation that contains a prefix.
func ExampleIPAM_Lookup() {
	registry := newExampleRegistry(exampleClock())
	_, _ = registry.Allocate("sites", ipam.Site("london"))

	found, ok := registry.Lookup(netip.MustParsePrefix("10.42.4.0/24"))
	fmt.Println(ok, found.Owner, found.Prefix)

	_, ok = registry.Lookup(netip.MustParsePrefix("10.42.16.0/20"))
	fmt.Println(ok)
	// Output:
	// true site:london 10.42.0.0/20
	// false
}

// ExampleIPAM_OwnerOf shows answering who holds an address.
func ExampleIPAM_OwnerOf() {
	registry := newExampleRegistry(exampleClock())
	_, _ = registry.Allocate("nodes", ipam.Node("web-1"))

	owner, ok := registry.OwnerOf(netip.MustParseAddr("10.42.240.1"))
	fmt.Println(owner, ok)

	_, ok = registry.OwnerOf(netip.MustParseAddr("10.42.240.9"))
	fmt.Println(ok)
	// Output:
	// node:web-1 true
	// false
}

// ExampleIPAM_ByOwner shows listing every block an owner holds.
func ExampleIPAM_ByOwner() {
	registry := newExampleRegistry(exampleClock())
	_, _ = registry.AllocateSpecific(netip.MustParsePrefix("10.42.32.0/24"), ipam.Site("london"))
	_, _ = registry.AllocateSpecific(netip.MustParsePrefix("10.42.48.0/24"), ipam.Site("london"))

	for _, allocation := range registry.ByOwner(ipam.Site("london")) {
		fmt.Println(allocation.Prefix)
	}
	// Output:
	// 10.42.32.0/24
	// 10.42.48.0/24
}

// ExampleIPAM_Accounting shows the report that accounts for every address in
// the supernet, overall and per pool.
func ExampleIPAM_Accounting() {
	registry := newExampleRegistry(exampleClock())
	_, _ = registry.Allocate("sites", ipam.Site("london"))

	report := registry.Accounting()
	fmt.Println("total:", report.Total)
	fmt.Println("allocated:", report.Allocated)
	fmt.Println("free:", report.Free)
	fmt.Println("problems:", len(report.Problems))
	for _, pool := range report.Pools {
		fmt.Printf("%s: %d of %d free\n", pool.Name, pool.Free, pool.Total)
	}
	// Output:
	// total: 65536
	// allocated: 4096
	// free: 61440
	// problems: 0
	// sites: 28672 of 32768 free
	// nodes: 2048 of 2048 free
	// infra: 1024 of 1024 free
	// transit: 1024 of 1024 free
}

// ExampleMarshal shows saving a registry's state as JSON and loading it back.
// The output is byte-stable, so equal states always give equal bytes.
func ExampleMarshal() {
	registry := newExampleRegistry(exampleClock())
	_, _ = registry.Allocate("sites", ipam.Site("london"))

	data, err := ipam.Marshal(registry.Snapshot())
	if err != nil {
		fmt.Println(err)
		return
	}

	snapshot, err := ipam.Unmarshal(data)
	if err != nil {
		fmt.Println(err)
		return
	}
	restored, err := ipam.New(snapshot, exampleClock(), ipam.DefaultQuarantine)
	if err != nil {
		fmt.Println(err)
		return
	}
	owner, _ := restored.OwnerOf(netip.MustParseAddr("10.42.0.1"))
	fmt.Println(owner)
	// Output: site:london
}

// ExampleOwner shows how an owner is written as text.
func ExampleOwner() {
	fmt.Println(ipam.Site("london"))
	fmt.Println(ipam.Node("web-1"))
	fmt.Println(ipam.Link(string(types.LinkIDOf("web-1", "db-1"))))
	fmt.Println(ipam.Infra("dns"))
	// Output:
	// site:london
	// node:web-1
	// link:db-1~web-1
	// infra:dns
}
