package ipam

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"time"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// IPAM is the in-memory registry built from a Snapshot. It is not safe for
// concurrent use; netctl is single-threaded.
//
// The only way to get one is New, which refuses a snapshot that breaks any
// rule, so a live IPAM never holds two overlapping allocations.
type IPAM struct {
	supernet   netip.Prefix
	pools      []Pool
	allocs     []Allocation // always sorted by cidr.Compare
	clock      types.Clock
	quarantine time.Duration
	warnings   []string
}

// New checks a snapshot and returns an IPAM over it.
//
// A snapshot is refused unless:
//   - its schema is Schema (otherwise UNSUPPORTED_SCHEMA, and nothing else is checked);
//   - the supernet is a valid IPv4 prefix;
//   - every pool is a valid IPv4 prefix inside the supernet (OUT_OF_SUPERNET),
//     has a unique non-empty name and a sensible ChildBits and SkipFirst
//     (INVALID_PREFIX), and overlaps no other pool (OVERLAP);
//   - every allocation is inside the supernet (OUT_OF_SUPERNET), inside the
//     pool it names (INVALID_PREFIX), in a known state (INVALID_PREFIX), and
//     overlaps no other allocation (OVERLAP, naming both owners).
//
// Every problem is reported, not just the first: the error is a types.List
// that types.HasCode searches. Prefixes with host bits set are masked rather
// than refused. Allocations may arrive in any order; the IPAM keeps them
// sorted. The snapshot's slices are copied, so later changes to the snapshot
// do not touch the IPAM.
//
// clock and quarantine are used by later operations. clock must not be nil and
// quarantine must not be negative.
//
// Example: a snapshot holding site:aws and node:builder-1 on 10.42.0.0/20 and
// 10.42.0.3/32 returns an OVERLAP error that names both owners.
func New(snapshot Snapshot, clock types.Clock, quarantine time.Duration) (*IPAM, error) {
	if snapshot.Schema != Schema {
		var refused types.List
		refused.Add(types.Errf(types.ErrUnsupportedSchema, "schema", "got %q, want %q", snapshot.Schema, Schema))
		return nil, refused.Err()
	}
	var misuse []error
	if clock == nil {
		misuse = append(misuse, errors.New("ipam: clock must not be nil"))
	}
	if quarantine < 0 {
		misuse = append(misuse, fmt.Errorf("ipam: quarantine must not be negative, got %s", quarantine))
	}
	if len(misuse) > 0 {
		return nil, errors.Join(misuse...)
	}

	var problems types.List
	if !validIPv4(snapshot.Supernet) {
		problems.Add(types.Errf(types.ErrInvalidPrefix, "supernet", "%q is not a valid IPv4 prefix", snapshot.Supernet))
		return nil, problems.Err()
	}
	registry := &IPAM{
		supernet:   snapshot.Supernet.Masked(),
		clock:      clock,
		quarantine: quarantine,
	}
	registry.addPools(snapshot.Pools, &problems)
	registry.addAllocations(snapshot.Allocations, &problems)
	if err := problems.Err(); err != nil {
		return nil, err
	}
	return registry, nil
}

// validIPv4 reports whether prefix is a valid IPv4 prefix.
func validIPv4(prefix netip.Prefix) bool { return prefix.IsValid() && prefix.Addr().Is4() }

// addPools checks each pool and records the ones it can read, adding every
// problem it finds to problems.
func (registry *IPAM) addPools(pools []Pool, problems *types.List) {
	for _, pool := range pools {
		if !validIPv4(pool.Prefix) {
			problems.Add(types.Errf(types.ErrInvalidPrefix, string(pool.Name), "pool %q has invalid prefix %q", pool.Name, pool.Prefix))
			continue
		}
		pool.Prefix = pool.Prefix.Masked()
		subject := pool.Prefix.String()

		if pool.Name == "" {
			problems.Add(types.Errf(types.ErrInvalidPrefix, subject, "a pool has no name"))
		} else if registry.pool(pool.Name) != nil {
			problems.Add(types.Errf(types.ErrInvalidPrefix, subject, "two pools are named %q", pool.Name))
		}
		if !cidr.Contains(registry.supernet, pool.Prefix) {
			problems.Add(types.Errf(types.ErrOutOfSupernet, subject, "pool %s is outside %s", pool.Name, registry.supernet))
		}
		if pool.ChildBits != 0 && (pool.ChildBits < pool.Prefix.Bits() || pool.ChildBits > 32) {
			problems.Add(types.Errf(types.ErrInvalidPrefix, subject,
				"pool %s has child size /%d, which does not fit inside %s", pool.Name, pool.ChildBits, pool.Prefix))
		}
		if pool.SkipFirst < 0 {
			problems.Add(types.Errf(types.ErrInvalidPrefix, subject, "pool %s has a negative skip_first (%d)", pool.Name, pool.SkipFirst))
		}
		for _, earlier := range registry.pools {
			if cidr.Overlaps(earlier.Prefix, pool.Prefix) {
				problems.Add(types.Errf(types.ErrOverlap, subject, "pool %s overlaps pool %s (%s)", pool.Name, earlier.Name, earlier.Prefix))
			}
		}
		registry.pools = append(registry.pools, pool)
	}
}

// addAllocations checks each allocation against the supernet, its pool and the
// allocations already accepted, and keeps the good ones sorted.
func (registry *IPAM) addAllocations(allocations []Allocation, problems *types.List) {
	for _, alloc := range allocations {
		if !validIPv4(alloc.Prefix) {
			problems.Add(types.Errf(types.ErrInvalidPrefix, alloc.Owner.String(), "%s holds invalid prefix %q", alloc.Owner, alloc.Prefix))
			continue
		}
		alloc.Prefix = alloc.Prefix.Masked()
		subject := alloc.Prefix.String()

		if !alloc.State.Valid() {
			problems.Add(types.Errf(types.ErrInvalidPrefix, subject, "%s has unknown state %q", alloc.Owner, alloc.State))
		}
		if !cidr.Contains(registry.supernet, alloc.Prefix) {
			problems.Add(types.Errf(types.ErrOutOfSupernet, subject, "%s holds a prefix outside the supernet %s", alloc.Owner, registry.supernet))
			continue
		}
		home := registry.poolFor(alloc.Prefix)
		switch {
		case home == nil:
			problems.Add(types.Errf(types.ErrInvalidPrefix, subject, "%s holds a prefix outside every pool", alloc.Owner))
			continue
		case home.Name != alloc.Pool:
			problems.Add(types.Errf(types.ErrInvalidPrefix, subject,
				"%s says pool %q but the prefix lies in pool %q", alloc.Owner, alloc.Pool, home.Name))
			continue
		}
		clash := false
		for _, held := range registry.allocs {
			if cidr.Overlaps(held.Prefix, alloc.Prefix) {
				problems.Add(types.Errf(types.ErrOverlap, subject, "%s (%s) overlaps %s (%s)", alloc.Owner, alloc.Prefix, held.Owner, held.Prefix))
				clash = true
			}
		}
		if !clash {
			registry.insert(alloc)
		}
	}
}

// insert adds alloc, keeping allocs sorted by cidr.Compare.
func (registry *IPAM) insert(alloc Allocation) {
	registry.allocs = slices.Insert(registry.allocs, registry.firstNotBefore(alloc.Prefix), alloc)
}

// firstNotBefore returns the index of the first allocation that sorts at or
// after prefix under cidr.Compare (len(allocs) if none). It indexes in place:
// Allocation is a large struct, and a comparison function that took it by value
// would copy it on every step.
func (registry *IPAM) firstNotBefore(prefix netip.Prefix) int {
	return sort.Search(len(registry.allocs), func(i int) bool {
		return cidr.Compare(registry.allocs[i].Prefix, prefix) >= 0
	})
}

// poolFor returns the pool that contains prefix, or nil.
func (registry *IPAM) poolFor(prefix netip.Prefix) *Pool {
	for i := range registry.pools {
		if cidr.Contains(registry.pools[i].Prefix, prefix) {
			return &registry.pools[i]
		}
	}
	return nil
}

// pool returns the pool called name, or nil.
func (registry *IPAM) pool(name PoolName) *Pool {
	for i := range registry.pools {
		if registry.pools[i].Name == name {
			return &registry.pools[i]
		}
	}
	return nil
}

// overlapping returns an allocation that shares addresses with prefix, or nil.
// Quarantined allocations count: their addresses are not free yet.
//
// Allocations are disjoint and sorted, so only the two neighbours of prefix's
// sort position can overlap it: the one just before (it may contain prefix) and
// the one at or after (it may sit inside prefix). That is a binary search, not a
// scan.
func (registry *IPAM) overlapping(prefix netip.Prefix) *Allocation {
	at := registry.firstNotBefore(prefix)
	for _, i := range [2]int{at - 1, at} {
		if i >= 0 && i < len(registry.allocs) && cidr.Overlaps(registry.allocs[i].Prefix, prefix) {
			return &registry.allocs[i]
		}
	}
	return nil
}

// expire drops quarantined allocations whose quarantine has ended, so their
// addresses become free again. Every operation that reads or changes the
// registry calls it first, which is how the injected clock moves time forward.
func (registry *IPAM) expire() {
	now := registry.clock.Now()
	registry.allocs = slices.DeleteFunc(registry.allocs, func(held Allocation) bool {
		return held.State == Quarantined && !now.Before(held.QuarantineUntil)
	})
}
