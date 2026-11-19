package ipam

import (
	"net/netip"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// AllocateSpecific claims exactly prefix for owner. Use it for hand-planned or
// imported ranges that Allocate would not have picked.
//
// The prefix may be any size inside a pool: the pool's ChildBits applies only
// to Allocate. A prefix with host bits set is masked and a warning is
// recorded (see Warnings). Errors:
//   - INVALID_PREFIX: not a valid IPv4 prefix, or not inside any pool.
//   - OUT_OF_SUPERNET: outside the supernet.
//   - RESERVED: the pool is reserved and no override reason was given, or the
//     prefix is reserved for another owner (the error names that owner).
//   - OVERLAP: the prefix collides with another allocation in any state, even
//     one that contains it or sits inside it (the error names that owner).
//
// Asking for a prefix the owner already holds returns it unchanged; if it was
// only a reservation it is promoted to Allocated in place.
//
// Example: AllocateSpecific(10.42.0.3/32, node:builder-1) while site:aws holds
// 10.42.0.0/20 fails with OVERLAP naming both owners.
func (registry *IPAM) AllocateSpecific(prefix netip.Prefix, owner Owner, opts ...Option) (Allocation, error) {
	return registry.claim(prefix, owner, Allocated, opts)
}

// Reserve holds prefix for owner without marking it in use. The same owner can
// later call AllocateSpecific (or Allocate, for a block in a sized pool) to
// promote it in place. Anyone else asking for it gets RESERVED naming owner.
//
// note is stored in Note. Reserving inside a reserved pool such as infra needs
// WithOverride, like any other allocation. All other rules are those of
// AllocateSpecific. Reserving a prefix the owner already holds changes nothing.
//
// Example: Reserve(10.42.248.0/24, infra:dns, "DNS anycast", WithOverride("approved by network team")).
func (registry *IPAM) Reserve(prefix netip.Prefix, owner Owner, note string, opts ...Option) (Allocation, error) {
	return registry.claim(prefix, owner, Reserved, append([]Option{WithNote(note)}, opts...))
}

// claim is the shared body of AllocateSpecific and Reserve.
func (registry *IPAM) claim(prefix netip.Prefix, owner Owner, want State, opts []Option) (Allocation, error) {
	var chosen options
	for _, apply := range opts {
		apply(&chosen)
	}
	if err := owner.validate(); err != nil {
		return Allocation{}, err
	}
	if !validIPv4(prefix) {
		return Allocation{}, types.Errf(types.ErrInvalidPrefix, prefix.String(), "not a valid IPv4 prefix")
	}
	registry.expire()
	masked, changed := cidr.Canonical(prefix)

	if !cidr.Contains(registry.supernet, masked) {
		return Allocation{}, types.Errf(types.ErrOutOfSupernet, masked.String(), "outside %s", registry.supernet)
	}
	home := registry.poolFor(masked)
	if home == nil {
		return Allocation{}, types.Errf(types.ErrInvalidPrefix, masked.String(), "not inside any pool")
	}
	if home.Reserved && chosen.override == "" {
		return Allocation{}, types.Errf(types.ErrReserved, masked.String(), "pool %s is reserved; an override reason is required", home.Name)
	}
	if other := registry.overlapping(masked); other != nil {
		switch {
		case other.Prefix == masked && other.Owner == owner && other.State != Quarantined:
			if want == Allocated {
				other.State = Allocated
			}
			registry.warnIfMasked(prefix, masked, changed)
			return *other, nil
		case other.State == Reserved:
			return Allocation{}, types.Errf(types.ErrReserved, masked.String(), "overlaps %s, which is reserved for %s", other.Prefix, other.Owner)
		default:
			return Allocation{}, types.Errf(types.ErrOverlap, masked.String(), "%s (%s) overlaps %s (%s)", owner, masked, other.Owner, other.Prefix)
		}
	}
	made := Allocation{
		Prefix:    masked,
		Pool:      home.Name,
		Owner:     owner,
		State:     want,
		CreatedAt: registry.clock.Now(),
		Note:      chosen.text(),
	}
	registry.insert(made)
	registry.warnIfMasked(prefix, masked, changed)
	return made, nil
}

// warnIfMasked records a warning for a prefix that arrived with host bits set.
func (registry *IPAM) warnIfMasked(given, masked netip.Prefix, changed bool) {
	if changed {
		registry.warnings = append(registry.warnings, given.String()+" has host bits set; stored as "+masked.String())
	}
}

// Warnings returns the warnings recorded so far, oldest first, as a copy.
// Today the only warning is a prefix given with host bits set. Warnings
// are not part of the Snapshot: they describe calls, not state.
func (registry *IPAM) Warnings() []string { return append([]string(nil), registry.warnings...) }
