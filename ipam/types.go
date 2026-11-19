package ipam

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl/cidr"
)

// Schema is the version string written into every Snapshot. A loader that sees
// any other value refuses the file (types.ErrUnsupportedSchema).
const Schema = "beaver.ipam/v1"

// PoolName names a pool.
//
// Example: "sites", "overlay", "transit".
type PoolName string

// Pool is a block of the supernet that hands out children of one size.
//
// A pool with ChildBits 0 has no fixed child size (the "infra" pool is like
// this) and is only ever reserved, never allocated child by child.
type Pool struct {
	// Name identifies the pool.
	Name PoolName `json:"name"`
	// Prefix is the block the pool covers. It must lie inside the supernet.
	Prefix netip.Prefix `json:"prefix"`
	// ChildBits is the prefix length of each child. Zero means no fixed size.
	ChildBits int `json:"child_bits,omitempty"`
	// Reserved means allocating from this pool needs WithOverride.
	Reserved bool `json:"reserved,omitempty"`
	// SkipFirst is how many leading children are never handed out.
	SkipFirst int `json:"skip_first,omitempty"`
}

// The kinds of owner. An Owner's Kind is one of these.
const (
	// KindSite is a site: "site:aws".
	KindSite = "site"
	// KindNode is a machine: "node:builder-1".
	KindNode = "node"
	// KindLink is a link between two nodes: "link:a~b".
	KindLink = "link"
	// KindInfra is infrastructure set aside for later phases: "infra:dns".
	KindInfra = "infra"
)

// Owner is who holds an allocation. In JSON and in text it is one string,
// "kind:id", such as "site:aws", "node:builder-1" or "link:a~b". That keeps
// ipam.json readable and makes owners easy to compare in tests.
//
// Owner is comparable, so it can be used as a map key and compared with ==.
// The zero Owner is not valid and refuses to marshal.
type Owner struct {
	// Kind is one of KindSite, KindNode, KindLink or KindInfra.
	Kind string
	// ID is the name within the kind. It is never empty.
	ID string
}

// Site returns the owner "site:id".
func Site(id string) Owner { return Owner{KindSite, id} }

// Node returns the owner "node:id".
func Node(id string) Owner { return Owner{KindNode, id} }

// Link returns the owner "link:id".
func Link(id string) Owner { return Owner{KindLink, id} }

// Infra returns the owner "infra:id".
func Infra(id string) Owner { return Owner{KindInfra, id} }

// String returns "kind:id".
//
// Example: Site("aws").String() is "site:aws".
func (owner Owner) String() string { return owner.Kind + ":" + owner.ID }

// validate reports why owner is not usable, or nil.
func (owner Owner) validate() error {
	switch owner.Kind {
	case KindSite, KindNode, KindLink, KindInfra:
	default:
		return fmt.Errorf("ipam: bad owner %q: kind must be site, node, link or infra", owner.String())
	}
	if owner.ID == "" {
		return fmt.Errorf("ipam: bad owner %q: id is empty", owner.String())
	}
	return nil
}

// MarshalText writes the owner as "kind:id". An owner with an unknown kind or
// an empty ID returns an error, so a half-filled Owner cannot reach ipam.json.
func (owner Owner) MarshalText() ([]byte, error) {
	if err := owner.validate(); err != nil {
		return nil, err
	}
	return []byte(owner.String()), nil
}

// UnmarshalText reads "kind:id". The text is split at the first ':', so an ID
// may itself contain ':'. An unknown kind, a missing ':' or an empty ID is an
// error and leaves the receiver unchanged.
func (owner *Owner) UnmarshalText(text []byte) error {
	kind, id, found := strings.Cut(string(text), ":")
	if !found {
		return fmt.Errorf("ipam: bad owner %q: want kind:id", text)
	}
	parsed := Owner{Kind: kind, ID: id}
	if err := parsed.validate(); err != nil {
		return err
	}
	*owner = parsed
	return nil
}

// State is what an allocation currently is.
type State string

// The states an Allocation can be in.
const (
	// Allocated means the prefix is in use by its owner.
	Allocated State = "allocated"
	// Reserved means the prefix is set aside and not yet in use.
	Reserved State = "reserved"
	// Quarantined means the prefix was released and may be reused only after
	// QuarantineUntil.
	Quarantined State = "quarantined"
)

// Valid reports whether state is one of the three known states.
func (state State) Valid() bool {
	switch state {
	case Allocated, Reserved, Quarantined:
		return true
	}
	return false
}

// Allocation is one prefix handed out of a pool.
type Allocation struct {
	// Prefix is the allocated block.
	Prefix netip.Prefix `json:"prefix"`
	// Pool is the pool the block came from.
	Pool PoolName `json:"pool"`
	// Owner holds the block.
	Owner Owner `json:"owner"`
	// State is allocated, reserved or quarantined.
	State State `json:"state"`
	// CreatedAt is when the block was first taken, by the injected clock.
	CreatedAt time.Time `json:"created_at"`
	// QuarantineUntil is when a quarantined block becomes reusable. It is
	// left out of the JSON for any other state.
	QuarantineUntil time.Time `json:"quarantine_until,omitzero"`
	// Note records why, for example the reason given with WithOverride.
	Note string `json:"note,omitempty"`
}

// Snapshot is the complete IPAM state and the only thing that is persisted.
// The caller stores it (D7); netctl never touches a file.
//
// Allocations are kept sorted by cidr.Compare so that the same state always
// serialises to the same bytes. Call SortAllocations after building one by hand.
type Snapshot struct {
	// Schema must be Schema.
	Schema string `json:"schema"`
	// Supernet is the overall range every pool and allocation lives inside.
	Supernet netip.Prefix `json:"supernet"`
	// Pools are the pools, in configuration order.
	Pools []Pool `json:"pools"`
	// Allocations are sorted by cidr.Compare.
	Allocations []Allocation `json:"allocations"`
}

// SortAllocations orders Allocations by cidr.Compare. The sort is stable, so
// equal prefixes (which New rejects as an overlap) keep their input order.
func (snapshot *Snapshot) SortAllocations() {
	slices.SortStableFunc(snapshot.Allocations, func(first, second Allocation) int {
		return cidr.Compare(first.Prefix, second.Prefix)
	})
}

// PoolUsage is how full one pool is, in addresses.
//
// Allocated + Reserved + Quarantined + Free == Total, unless the registry is
// corrupt (see Report.Problems).
type PoolUsage struct {
	// Name is the pool.
	Name PoolName `json:"name"`
	// Prefix is the block the pool covers.
	Prefix netip.Prefix `json:"prefix"`
	// Total is the number of addresses in the pool.
	Total uint64 `json:"total"`
	// Allocated is the number of addresses in allocated blocks.
	Allocated uint64 `json:"allocated"`
	// Reserved is the number of addresses in reserved blocks.
	Reserved uint64 `json:"reserved"`
	// Quarantined is the number of addresses in quarantined blocks.
	Quarantined uint64 `json:"quarantined"`
	// Free is the number of addresses in the pool that nothing holds.
	Free uint64 `json:"free"`
}

// Report is the result of Accounting: where every address in the supernet went.
//
// Total = Allocated + Reserved + Quarantined + Free. Sizes are counts of
// addresses, not prefixes.
type Report struct {
	// Supernet is the range accounted for.
	Supernet netip.Prefix `json:"supernet"`
	// Total is the number of addresses in Supernet.
	Total uint64 `json:"total"`
	// Allocated is the number of addresses in allocated blocks.
	Allocated uint64 `json:"allocated"`
	// Reserved is the number of addresses in reserved blocks.
	Reserved uint64 `json:"reserved"`
	// Quarantined is the number of addresses in quarantined blocks.
	Quarantined uint64 `json:"quarantined"`
	// Free is the number of addresses in no allocation at all. It includes
	// space that belongs to no pool.
	Free uint64 `json:"free"`
	// FreeBlocks are the free addresses as the fewest aligned prefixes, in
	// address order. Never nil.
	FreeBlocks []netip.Prefix `json:"free_blocks"`
	// Pools is the usage of each pool, in pool order. Never nil.
	Pools []PoolUsage `json:"pools"`
	// Problems lists anything wrong that accounting noticed, as text. It is
	// empty (not nil) on every valid registry.
	Problems []string `json:"problems"`
}
