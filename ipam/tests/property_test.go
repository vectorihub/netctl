package tests

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// A random program is a list of operations in a tiny language, one per line:
//
//	alloc sites site:s3          Allocate
//	override growth site:s3      Allocate with WithOverride
//	specific 10.42.16.0/20 site:s3   AllocateSpecific
//	reserve 10.42.16.0/20 site:s3    Reserve
//	release 10.42.16.0/20 site:s3    Release
//	tick 25h                     move the clock
//
// Every program is generated from its own seeded generator, so a failure prints
// the seed and the (shrunk) program, and the program can be pasted into a unit
// test. Errors from operations are expected and ignored; broken invariants are not.
type opKind string

const (
	opAlloc    opKind = "alloc"
	opOverride opKind = "override"
	opSpecific opKind = "specific"
	opReserve  opKind = "reserve"
	opRelease  opKind = "release"
	opTick     opKind = "tick"
)

type op struct {
	kind   opKind
	pool   ipam.PoolName
	prefix netip.Prefix
	owner  ipam.Owner
	by     time.Duration
}

// String writes an operation as one line of the program text printed when a property fails.
func (o op) String() string {
	switch o.kind {
	case opAlloc, opOverride:
		return fmt.Sprintf("%s %s %s", o.kind, o.pool, o.owner)
	case opTick:
		return fmt.Sprintf("tick %s", o.by)
	}
	return fmt.Sprintf("%s %s %s", o.kind, o.prefix, o.owner)
}

// program writes a whole list of operations as numbered lines, the form a failing program is
// reported in.
func program(ops []op) string {
	lines := make([]string, len(ops))
	for i, o := range ops {
		lines[i] = fmt.Sprintf("%4d  %s", i, o)
	}
	return strings.Join(lines, "\n")
}

// The cast of owners is small on purpose, so owners collide and repeat, which is
// where idempotency, promotion and quarantine bugs live.
const (
	siteOwners = 10 // more than the 8 /20s in sites, so the pool runs out
	nodeOwners = 40
	linkOwners = 12
)

// randomOwner picks an owner of the given kind from a small fixed set, so owners repeat often
// enough to collide.
func randomOwner(random *rand.Rand, kind string) ipam.Owner {
	switch kind {
	case ipam.KindSite:
		return ipam.Site(fmt.Sprintf("s%d", random.IntN(siteOwners)))
	case ipam.KindLink:
		return ipam.Link(fmt.Sprintf("n%d~n%d", random.IntN(linkOwners), linkOwners+random.IntN(linkOwners)))
	case ipam.KindInfra:
		return ipam.Infra(fmt.Sprintf("i%d", random.IntN(4)))
	}
	return ipam.Node(fmt.Sprintf("n%d", random.IntN(nodeOwners)))
}

// randomOp picks the next operation. Releases usually name something that
// exists, so they succeed often enough to exercise quarantine.
func randomOp(random *rand.Rand, registry *ipam.IPAM) op {
	pools := []struct {
		name ipam.PoolName
		kind string
	}{{"sites", ipam.KindSite}, {"overlay", ipam.KindNode}, {"transit", ipam.KindLink}, {"growth", ipam.KindSite}}

	switch roll := random.IntN(100); {
	case roll < 45:
		pool := pools[random.IntN(len(pools))]
		kind := opAlloc
		if pool.name == "growth" || random.IntN(10) == 0 {
			kind = opOverride
		}
		return op{kind: kind, pool: pool.name, owner: randomOwner(random, pool.kind)}
	case roll < 60:
		kinds := []string{ipam.KindSite, ipam.KindNode, ipam.KindLink, ipam.KindInfra}
		bits := 20 + random.IntN(13)
		span := uint32(1) << (32 - 16)
		address := uint32(10<<24|42<<16) + random.Uint32N(span)
		prefix := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(address >> 24), byte(address >> 16), byte(address >> 8), byte(address)}), bits).Masked()
		kind := opSpecific
		if random.IntN(3) == 0 {
			kind = opReserve
		}
		return op{kind: kind, prefix: prefix, owner: randomOwner(random, kinds[random.IntN(len(kinds))])}
	case roll < 85:
		if live := allocsOf(registry); len(live) > 0 && random.IntN(10) < 9 {
			held := live[random.IntN(len(live))]
			return op{kind: opRelease, prefix: held.Prefix, owner: held.Owner}
		}
		return op{kind: opRelease, prefix: netip.MustParsePrefix("10.42.240.9/32"), owner: randomOwner(random, ipam.KindNode)}
	}
	steps := []time.Duration{time.Minute, time.Hour, 25 * time.Hour}
	return op{kind: opTick, by: steps[random.IntN(len(steps))]}
}

// apply runs one operation. Its errors are the registry saying no, which is fine.
func apply(registry *ipam.IPAM, clock *types.FixedClock, o op) {
	switch o.kind {
	case opAlloc:
		_, _ = registry.Allocate(o.pool, o.owner)
	case opOverride:
		_, _ = registry.Allocate(o.pool, o.owner, ipam.WithOverride("property test"))
	case opSpecific:
		_, _ = registry.AllocateSpecific(o.prefix, o.owner, ipam.WithOverride("property test"))
	case opReserve:
		_, _ = registry.Reserve(o.prefix, o.owner, "property test", ipam.WithOverride("property test"))
	case opRelease:
		_ = registry.Release(o.prefix, o.owner)
	case opTick:
		clock.Advance(o.by)
	}
}

// snapshotInvariants checks a snapshot without relying on Accounting: the
// allocations are sorted and disjoint, every one is valid and inside the
// supernet, and the sizes of all allocations, summed by state, fit in the
// supernet (allocations never overlap, so what is left is the total minus their
// sizes). It returns "" when everything holds, and the sums by state.
func snapshotInvariants(snap ipam.Snapshot) (string, map[ipam.State]uint64) {
	byState := map[ipam.State]uint64{}
	for i, held := range snap.Allocations {
		if _, err := held.Owner.MarshalText(); err != nil || !held.State.Valid() || !cidr.Contains(snap.Supernet, held.Prefix) {
			return fmt.Sprintf("bad allocation %+v", held), nil
		}
		if i > 0 {
			before := snap.Allocations[i-1]
			if cidr.Compare(before.Prefix, held.Prefix) >= 0 {
				return fmt.Sprintf("allocations out of order: %s then %s", before.Prefix, held.Prefix), nil
			}
			if cidr.Overlaps(before.Prefix, held.Prefix) {
				return fmt.Sprintf("overlap: %s (%s) and %s (%s)", before.Prefix, before.Owner, held.Prefix, held.Owner), nil
			}
		}
		byState[held.State] += cidr.Size(held.Prefix)
	}
	if taken := byState[ipam.Allocated] + byState[ipam.Reserved] + byState[ipam.Quarantined]; taken > cidr.Size(snap.Supernet) {
		return fmt.Sprintf("allocations total %d addresses in a supernet of %d", taken, cidr.Size(snap.Supernet)), nil
	}
	return "", byState
}

// invariants checks the registry and returns "" when everything holds. The
// snapshot checks run every time (Snapshot first drops expired quarantines, like
// every real method). With full set it also asks Accounting (P1-IPAM-022) and
// checks it agrees with the independent sums and reports no Problems.
func invariants(registry *ipam.IPAM, full bool) string {
	snap := registry.Snapshot()
	failure, byState := snapshotInvariants(snap)
	if failure != "" || !full {
		return failure
	}
	total := cidr.Size(snap.Supernet)
	taken := byState[ipam.Allocated] + byState[ipam.Reserved] + byState[ipam.Quarantined]
	report := registry.Accounting()
	if len(report.Problems) > 0 {
		return strings.Join(report.Problems, "; ")
	}
	if report.Allocated != byState[ipam.Allocated] || report.Reserved != byState[ipam.Reserved] ||
		report.Quarantined != byState[ipam.Quarantined] || report.Free != total-taken {
		return fmt.Sprintf("Accounting disagrees with the independent sum: %+v, independent %v free %d", report, byState, total-taken)
	}
	return ""
}

// replay runs ops on a fresh default registry and returns the first broken
// invariant and the step it broke at, or "" if the whole program is clean.
func replay(ops []op) (string, int) {
	clock := &types.FixedClock{T: t0}
	registry, err := ipam.New(snapshotWith(), clock, 24*time.Hour)
	if err != nil {
		return "setup: " + err.Error(), -1
	}
	for step, o := range ops {
		apply(registry, clock, o)
		if failure := invariants(registry, true); failure != "" {
			return failure, step
		}
	}
	return "", -1
}

// shrink returns a shortest-looking program that still fails: it drops chunks of
// operations, halving the chunk size, until no single operation can be removed.
func shrink(ops []op, fails func([]op) bool) []op {
	for chunk := len(ops) / 2; chunk >= 1; chunk /= 2 {
		for changed := true; changed; {
			changed = false
			for start := 0; start+chunk <= len(ops); start += chunk {
				candidate := slices.Concat(ops[:start], ops[start+chunk:])
				if fails(candidate) {
					ops, changed = candidate, true
					break
				}
			}
		}
	}
	return ops
}

const (
	propertySeeds = 20
	propertyOps   = 5000
)

// P1-IPAM-014 and P1-IPAM-022: 5,000 random operations for each of seeds 1-20.
// After every step no two non-free prefixes overlap, and allocated + reserved +
// quarantined + free is always the whole supernet with Problems empty.
func TestPropertyNeverOverlapsAndAlwaysAddsUp(t *testing.T) {
	seeds := propertySeeds
	if testing.Short() {
		seeds = 3
	}
	for seed := uint64(1); seed <= uint64(seeds); seed++ {
		random := rand.New(rand.NewPCG(seed, seed))
		clock := &types.FixedClock{T: t0}
		registry, err := ipam.New(snapshotWith(), clock, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		var done []op
		for step := 0; step < propertyOps; step++ {
			next := randomOp(random, registry)
			done = append(done, next)
			apply(registry, clock, next)
			// Full Accounting is the expensive part, so it runs every 20 steps and at
			// the end; the independent checks run on every step.
			if failure := invariants(registry, step%20 == 19 || step == propertyOps-1); failure != "" {
				short := shrink(done, func(ops []op) bool { f, _ := replay(ops); return f != "" })
				t.Fatalf("seed %d, step %d: %s\nshortest failing program (%d ops); paste it into a unit test:\n%s",
					seed, step, failure, len(short), program(short))
			}
		}
	}
}

// The generator must reach the interesting states, or the property proves little.
func TestPropertyProgramsExerciseEverything(t *testing.T) {
	seen := map[string]bool{}
	random := rand.New(rand.NewPCG(1, 1))
	clock := &types.FixedClock{T: t0}
	registry, err := ipam.New(snapshotWith(), clock, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < propertyOps; step++ {
		next := randomOp(random, registry)
		apply(registry, clock, next)
		for _, held := range allocsOf(registry) {
			seen["state "+string(held.State)] = true
			seen["pool "+string(held.Pool)] = true
		}
		seen["op "+string(next.kind)] = true
	}
	for _, want := range []string{
		"state allocated", "state reserved", "state quarantined",
		"pool sites", "pool overlay", "pool transit", "pool growth",
		"op alloc", "op override", "op specific", "op reserve", "op release", "op tick",
	} {
		if !seen[want] {
			t.Errorf("seed 1 never reached %q", want)
		}
	}
}

// The invariant check must actually notice damage, or the property tests prove
// nothing. A Snapshot value can be damaged freely, unlike a live registry.
func TestInvariantsNoticeDamage(t *testing.T) {
	clean := snapshotWith(held("10.42.0.0/20", "sites", ipam.Site("aws")), held("10.42.32.0/20", "sites", ipam.Site("dgx")))
	if failure, _ := snapshotInvariants(clean); failure != "" {
		t.Fatalf("clean snapshot flagged: %s", failure)
	}
	damage := map[string]struct {
		change func(*ipam.Snapshot)
		want   string
	}{
		"overlap": {func(s *ipam.Snapshot) {
			s.Allocations = append(s.Allocations, held("10.42.0.5/32", "sites", ipam.Node("x")))
			s.SortAllocations()
		}, "overlap"},
		"out of order": {func(s *ipam.Snapshot) { s.Allocations[0], s.Allocations[1] = s.Allocations[1], s.Allocations[0] }, "out of order"},
		"outside supernet": {func(s *ipam.Snapshot) {
			s.Allocations = append(s.Allocations, held("192.168.0.0/24", "sites", ipam.Site("x")))
			s.SortAllocations()
		}, "bad allocation"},
		"unknown state": {func(s *ipam.Snapshot) { s.Allocations[0].State = "weird" }, "bad allocation"},
		"zero owner":    {func(s *ipam.Snapshot) { s.Allocations[0].Owner = ipam.Owner{} }, "bad allocation"},
	}
	for name, c := range damage {
		snap := clean
		snap.Allocations = append([]ipam.Allocation(nil), clean.Allocations...)
		c.change(&snap)
		if failure, _ := snapshotInvariants(snap); !strings.Contains(failure, c.want) {
			t.Errorf("%s: got %q, want it to mention %q", name, failure, c.want)
		}
	}
}

// The shrinker keeps exactly what the failure needs.
func TestShrinkFindsShortestFailure(t *testing.T) {
	a := op{kind: opAlloc, pool: "sites", owner: ipam.Site("a")}
	b := op{kind: opTick, by: time.Hour}
	var program []op
	for i := 0; i < 200; i++ {
		program = append(program, op{kind: opAlloc, pool: "overlay", owner: ipam.Node(fmt.Sprintf("n%d", i))})
	}
	program[37], program[151] = a, b
	needsBoth := func(ops []op) bool {
		return slices.Contains(ops, a) && slices.Contains(ops, b) && slices.Index(ops, a) < slices.Index(ops, b)
	}
	got := shrink(program, needsBoth)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("shrunk to %d ops: %v", len(got), got)
	}
}

// Regression: the shape of failure the shrinker is for. A pinned block, a
// release and a long wait must leave the books balanced.
func TestProgramPinReleaseWait(t *testing.T) {
	ops := []op{
		{kind: opSpecific, prefix: pfx("10.42.0.0/20"), owner: ipam.Site("s1")},
		{kind: opAlloc, pool: "sites", owner: ipam.Site("s2")},
		{kind: opRelease, prefix: pfx("10.42.0.0/20"), owner: ipam.Site("s1")},
		{kind: opAlloc, pool: "sites", owner: ipam.Site("s3")},
		{kind: opTick, by: 25 * time.Hour},
		{kind: opAlloc, pool: "sites", owner: ipam.Site("s4")},
	}
	if failure, step := replay(ops); failure != "" {
		t.Fatalf("step %d: %s\n%s", step, failure, program(ops))
	}
}

// P1-IPAM-043: the planner hands IPAM its owners sorted, so the same set of
// owners gives the same snapshot however the caller shuffled them.
func TestPropertyOrderIndependence(t *testing.T) {
	type request struct {
		pool  ipam.PoolName
		owner ipam.Owner
	}
	var requests []request
	for i := 0; i < 8; i++ {
		requests = append(requests, request{"sites", ipam.Site(fmt.Sprintf("site-%d", i))})
	}
	for i := 0; i < 60; i++ {
		requests = append(requests, request{"overlay", ipam.Node(fmt.Sprintf("node-%02d", i))})
	}
	for i := 0; i < 20; i++ {
		requests = append(requests, request{"transit", ipam.Link(fmt.Sprintf("a%02d~b%02d", i, i))})
	}
	run := func(seed uint64) []byte {
		shuffled := slices.Clone(requests)
		random := rand.New(rand.NewPCG(seed, seed))
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		// The planner's step: sort its input before allocating.
		slices.SortFunc(shuffled, func(a, b request) int {
			return strings.Compare(string(a.pool)+a.owner.String(), string(b.pool)+b.owner.String())
		})
		registry := fresh(t)
		for _, r := range shuffled {
			mustAllocate(t, registry, r.pool, r.owner)
		}
		first, err := ipam.Marshal(registry.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		// Idempotency (R6): asking again, in a different order, moves no one.
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for _, r := range shuffled {
			mustAllocate(t, registry, r.pool, r.owner)
		}
		second, err := ipam.Marshal(registry.Snapshot())
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("seed %d: asking again changed the snapshot (%v)", seed, err)
		}
		return first
	}
	want := run(1)
	for seed := uint64(2); seed <= propertySeeds; seed++ {
		if got := run(seed); !bytes.Equal(got, want) {
			t.Fatalf("seed %d gave a different snapshot than seed 1", seed)
		}
	}
}

// naiveLowestFree is the obviously-correct version of the lowest-free search:
// walk every child from the first and take the first that overlaps nothing in
// the snapshot, in any state. It is the reference Allocate is checked against.
func naiveLowestFree(snap ipam.Snapshot, pool ipam.Pool) (netip.Prefix, bool) {
	children, err := cidr.Children(pool.Prefix, pool.ChildBits)
	if err != nil {
		return netip.Prefix{}, false
	}
	for index, child := range children {
		if index < pool.SkipFirst {
			continue
		}
		free := true
		for _, held := range snap.Allocations {
			if cidr.Overlaps(held.Prefix, child) {
				free = false
				break
			}
		}
		if free {
			return child, true
		}
	}
	return netip.Prefix{}, false
}

// The jump-ahead search in Allocate must pick exactly what a plain walk picks,
// through pins, reservations, releases and quarantine, including when full.
func TestAllocateMatchesNaiveWalk(t *testing.T) {
	for seed := uint64(1); seed <= 5; seed++ {
		random := rand.New(rand.NewPCG(seed, 99))
		clock := &types.FixedClock{T: t0}
		registry, err := ipam.New(snapshotWith(), clock, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for step := 0; step < 3000; step++ {
			next := randomOp(random, registry)
			if next.kind == opAlloc || next.kind == opOverride {
				snap := registry.Snapshot() // drops expired quarantines first
				var pool *ipam.Pool
				for i := range snap.Pools {
					if snap.Pools[i].Name == next.pool {
						pool = &snap.Pools[i]
					}
				}
				holds := false
				for _, held := range snap.Allocations {
					holds = holds || held.Pool == next.pool && held.Owner == next.owner && held.State != ipam.Quarantined
				}
				if !holds && pool != nil && pool.ChildBits != 0 {
					want, wantOK := naiveLowestFree(snap, *pool)
					got, err := registry.Allocate(next.pool, next.owner, ipam.WithOverride("test"))
					switch {
					case wantOK && (err != nil || got.Prefix != want):
						t.Fatalf("seed %d step %d: %s %s gave %v, %v; plain walk says %s", seed, step, next.pool, next.owner, got.Prefix, err, want)
					case !wantOK && !types.HasCode(err, types.ErrPoolExhausted):
						t.Fatalf("seed %d step %d: %s full, got %v, %v", seed, step, next.pool, got.Prefix, err)
					}
					checked++
					continue
				}
			}
			apply(registry, clock, next)
		}
		if checked < 200 {
			t.Errorf("seed %d compared only %d allocations", seed, checked)
		}
	}
}
