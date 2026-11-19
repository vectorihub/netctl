package ipam

import (
	"net/netip"
	"strings"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// TransitPool is the name of the pool that numbers direct links.
const TransitPool PoolName = "transit"

// Lookup returns the live allocation that equals or contains prefix. Because
// allocations never overlap there is at most one. Quarantined blocks are not
// live, so they are not found. A prefix with host bits set is masked first.
//
// Example: with site:aws on 10.42.0.0/20, Lookup(10.42.4.0/24) and
// Lookup(10.42.0.0/20) both return that allocation; Lookup(10.42.16.0/20)
// finds nothing.
func (registry *IPAM) Lookup(prefix netip.Prefix) (Allocation, bool) {
	if !validIPv4(prefix) {
		return Allocation{}, false
	}
	registry.expire()
	masked := prefix.Masked()
	// The allocation containing masked sorts at or just before it.
	at := registry.firstNotBefore(masked)
	for _, i := range [2]int{at - 1, at} {
		if i >= 0 && i < len(registry.allocs) {
			found := registry.allocs[i]
			if found.State != Quarantined && cidr.Contains(found.Prefix, masked) {
				return found, true
			}
		}
	}
	return Allocation{}, false
}

// OwnerOf returns who holds addr, or false if no live allocation covers it.
//
// Example: on a plan where node:builder-1 holds 10.42.240.2/32,
// OwnerOf(10.42.240.2) is node:builder-1.
func (registry *IPAM) OwnerOf(addr netip.Addr) (Owner, bool) {
	if !addr.IsValid() || !addr.Is4() {
		return Owner{}, false
	}
	found, ok := registry.Lookup(netip.PrefixFrom(addr, 32))
	if !ok {
		return Owner{}, false
	}
	return found.Owner, true
}

// ByOwner returns every live (allocated or reserved) allocation owner holds, in
// address order. Quarantined blocks are not included. The result is a copy and
// is nil when owner holds nothing.
func (registry *IPAM) ByOwner(owner Owner) []Allocation {
	registry.expire()
	var out []Allocation
	for _, held := range registry.allocs {
		if held.Owner == owner && held.State != Quarantined {
			out = append(out, held)
		}
	}
	return out
}

// TransitEnd is one end of a numbered link.
type TransitEnd struct {
	// Node is the machine at this end.
	Node types.NodeID
	// Addr is the machine's address on the link.
	Addr netip.Addr
}

// Transit is a direct link's /30 and the two host addresses inside it.
type Transit struct {
	// Allocation is the /30 held by link:<id>.
	Allocation Allocation
	// Ends has the lexically smaller node ID first. It gets the lower address
	// (.1); the other node gets .2.
	Ends [2]TransitEnd
}

// AllocateTransit numbers the direct link between two nodes: it takes the lowest
// free /30 from the transit pool for owner link:<id> and returns the two usable
// host addresses, the lower to the lexically smaller node ID. It is idempotent,
// like Allocate, so asking again returns the same numbering.
//
// link must be "nodeA~nodeB" with two valid, different node IDs, in either
// order (see types.LinkIDOf). Errors: INVALID_TOPOLOGY for a malformed link,
// INVALID_PREFIX if the transit pool does not hand out /30s, and anything
// Allocate returns (NOT_FOUND without a transit pool, POOL_EXHAUSTED when full).
//
// Example: the first link, "dgx-spark~builder-1", gets 10.42.252.0/30; builder-1
// is 10.42.252.1 and dgx-spark is 10.42.252.2.
func (registry *IPAM) AllocateTransit(link types.LinkID, opts ...Option) (Transit, error) {
	first, second, ok := splitLink(link)
	if !ok {
		return Transit{}, types.Errf(types.ErrInvalidTopology, string(link), "a link is two different valid node IDs joined by '~'")
	}
	if pool := registry.pool(TransitPool); pool != nil && pool.ChildBits != 30 {
		return Transit{}, types.Errf(types.ErrInvalidPrefix, string(TransitPool), "transit pool hands out /%d, need /30", pool.ChildBits)
	}
	canonical := types.LinkIDOf(first, second)
	got, err := registry.Allocate(TransitPool, Link(string(canonical)), opts...)
	if err != nil {
		return Transit{}, err
	}
	low := got.Prefix.Addr().Next()
	return Transit{
		Allocation: got,
		Ends: [2]TransitEnd{
			{Node: min(first, second), Addr: low},
			{Node: max(first, second), Addr: low.Next()},
		},
	}, nil
}

// splitLink reads "a~b" into two valid, different node IDs.
func splitLink(link types.LinkID) (types.NodeID, types.NodeID, bool) {
	first, second, found := strings.Cut(string(link), "~")
	if !found || !types.ValidID(first) || !types.ValidID(second) || first == second {
		return "", "", false
	}
	return types.NodeID(first), types.NodeID(second), true
}
