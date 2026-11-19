package ipam

import (
	"net/netip"
	"sort"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// Option changes how one allocation is made.
type Option func(*options)

type options struct{ override, note string }

// WithOverride allows allocating from a reserved pool. The reason is required:
// an empty reason is the same as no override. It is stored in the allocation's
// Note as "override: <reason>".
//
// Example: Allocate("growth", Site("emea"), WithOverride("approved in CHG-12")).
func WithOverride(reason string) Option { return func(o *options) { o.override = reason } }

// WithNote attaches a free-text note to the allocation.
func WithNote(text string) Option { return func(o *options) { o.note = text } }

// text returns what is stored in Allocation.Note: the override reason, then any
// note, or the note alone.
func (o options) text() string {
	switch {
	case o.override != "" && o.note != "":
		return "override: " + o.override + "; " + o.note
	case o.override != "":
		return "override: " + o.override
	}
	return o.note
}

// holding returns the live (not quarantined) allocation owner has in pool, or nil.
func (registry *IPAM) holding(pool PoolName, owner Owner) *Allocation {
	for i := range registry.allocs {
		held := &registry.allocs[i]
		if held.Pool == pool && held.Owner == owner && held.State != Quarantined {
			return held
		}
	}
	return nil
}

// Allocate gives owner the lowest free child of pool, in address order, and
// returns it. The result is deterministic: the same snapshot and the same calls
// in the same order always give the same prefixes.
//
// It is idempotent: an owner that already holds a prefix in the pool gets that
// same prefix back, nothing new is recorded, and no one moves. If the held
// prefix was only a reservation it becomes Allocated.
//
// The first Pool.SkipFirst children are never handed out. A reserved pool
// refuses with RESERVED unless WithOverride gives a reason. A quarantined block
// is not free until its quarantine ends. Errors:
//   - NOT_FOUND: no pool has that name.
//   - RESERVED: the pool is reserved and no override reason was given.
//   - INVALID_PREFIX: the pool has no child size (ChildBits 0), so it cannot be
//     allocated child by child.
//   - POOL_EXHAUSTED: every child is taken or skipped.
//
// Example: on a fresh default plan, Allocate("sites", Site("aws")) returns
// 10.42.0.0/20, and calling it again returns the same allocation.
func (registry *IPAM) Allocate(pool PoolName, owner Owner, opts ...Option) (Allocation, error) {
	var chosen options
	for _, apply := range opts {
		apply(&chosen)
	}
	if err := owner.validate(); err != nil {
		return Allocation{}, err
	}
	registry.expire()

	if held := registry.holding(pool, owner); held != nil {
		held.State = Allocated
		return *held, nil
	}
	found := registry.pool(pool)
	if found == nil {
		return Allocation{}, types.Errf(types.ErrNotFound, string(pool), "no such pool")
	}
	if found.Reserved && chosen.override == "" {
		return Allocation{}, types.Errf(types.ErrReserved, string(pool), "pool is reserved; an override reason is required")
	}
	if found.ChildBits == 0 {
		return Allocation{}, types.Errf(types.ErrInvalidPrefix, string(pool), "pool has no child size; it cannot be allocated child by child")
	}
	child, ok := registry.lowestFree(found)
	if !ok {
		return Allocation{}, types.Errf(types.ErrPoolExhausted, string(pool), "no free /%d left for %s", found.ChildBits, owner)
	}
	made := Allocation{
		Prefix:    child,
		Pool:      pool,
		Owner:     owner,
		State:     Allocated,
		CreatedAt: registry.clock.Now(),
		Note:      chosen.text(),
	}
	registry.insert(made)
	return made, nil
}

// lowestFree returns the first child of pool, in address order and after the
// skipped ones, that overlaps no allocation in any state.
//
// Allocations are sorted and never overlap, so it makes one pass: find the first
// allocation that reaches the candidate child, and while that allocation
// collides with the candidate, move the candidate to the first child past it and
// step to the next allocation. The work depends on how many allocations are in
// the way, not on how many children come before them. (Testing every child from
// the first, each with its own search, made 2,000 overlay allocations take about
// a second.)
func (registry *IPAM) lowestFree(pool *Pool) (netip.Prefix, bool) {
	start := addrNumber(pool.Prefix.Masked().Addr())
	size := uint64(1) << (32 - pool.ChildBits)
	end := start + cidr.Size(pool.Prefix) // one past the pool's last address
	candidate := start + uint64(pool.SkipFirst)*size

	// The first allocation whose last address is at or after the candidate.
	next := sort.Search(len(registry.allocs), func(i int) bool {
		return lastOf(registry.allocs[i].Prefix) >= candidate
	})
	for candidate+size <= end {
		if next == len(registry.allocs) || addrNumber(registry.allocs[next].Prefix.Addr()) >= candidate+size {
			return netip.PrefixFrom(numberAddr(candidate), pool.ChildBits), true
		}
		// registry.allocs[next] overlaps the candidate: continue after it.
		candidate = start + ((lastOf(registry.allocs[next].Prefix)-start)/size+1)*size
		for next < len(registry.allocs) && lastOf(registry.allocs[next].Prefix) < candidate {
			next++
		}
	}
	return netip.Prefix{}, false
}

// lastOf returns the number of the last address of a stored prefix. Stored
// prefixes are always canonical, so unlike lastAddress it skips masking, which
// matters in the one loop that calls it for every allocation in the way.
func lastOf(prefix netip.Prefix) uint64 {
	return addrNumber(prefix.Addr()) + uint64(1)<<(32-prefix.Bits()) - 1
}

// addrNumber turns an IPv4 address into its number.
func addrNumber(addr netip.Addr) uint64 {
	bytes := addr.As4()
	return uint64(bytes[0])<<24 | uint64(bytes[1])<<16 | uint64(bytes[2])<<8 | uint64(bytes[3])
}

// numberAddr turns a number below 2^32 back into an IPv4 address.
func numberAddr(number uint64) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(number >> 24), byte(number >> 16), byte(number >> 8), byte(number)})
}
