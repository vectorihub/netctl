package cidr

import (
	"cmp"
	"encoding/binary"
	"iter"
	"net/netip"

	"github.com/vectorihub/netctl/types"
)

// maxSplit is the most children Split will build into a slice. Children has no
// such limit because it is lazy; Split exists for small, bounded cases.
const maxSplit = 1 << 20

// addrToNumber turns an IPv4 address into its number, so it can be added to.
func addrToNumber(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

// numberToAddr turns a number back into an IPv4 address.
func numberToAddr(number uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], number)
	return netip.AddrFrom4(bytes)
}

// isValidIPv4 reports whether prefix is a valid IPv4 prefix.
func isValidIPv4(prefix netip.Prefix) bool { return prefix.IsValid() && prefix.Addr().Is4() }

// Canonical masks prefix so its host bits are zero, and reports whether the input
// had host bits set. This is the one place a prefix is normalised: call it at
// the boundary, then pass the result on.
//
// An invalid prefix is returned unchanged with false; check validity with
// netip.Prefix.IsValid before calling when the input is untrusted.
//
// Example: Canonical(10.42.16.5/20) returns 10.42.16.0/20, true.
func Canonical(prefix netip.Prefix) (netip.Prefix, bool) {
	masked := prefix.Masked()
	if !prefix.IsValid() {
		return prefix, false
	}
	return masked, masked != prefix
}

// Contains reports whether child lies entirely inside parent. A prefix
// contains itself. Invalid prefixes and mixed address families never match.
//
// Example: Contains(10.42.0.0/16, 10.42.16.0/20) is true;
// Contains(10.42.16.0/20, 10.42.0.0/16) is false.
func Contains(parent, child netip.Prefix) bool {
	return parent.IsValid() && child.IsValid() &&
		parent.Bits() <= child.Bits() && parent.Contains(child.Addr())
}

// Overlaps reports whether first and second share any address. It is symmetric, and
// reports false when either is invalid or the families differ. Host bits are
// ignored, so it is safe on uncanonicalised input.
//
// Example: Overlaps(10.42.0.0/16, 10.42.16.0/20) is true;
// Overlaps(10.42.0.0/20, 10.42.16.0/20) is false.
func Overlaps(first, second netip.Prefix) bool { return first.Overlaps(second) }

// Size returns the number of addresses in an IPv4 prefix, or 0 for anything
// that is not a valid IPv4 prefix.
//
// Example: Size(10.42.16.0/20) is 4096; Size(10.0.0.0/0) is 4294967296.
func Size(prefix netip.Prefix) uint64 {
	if !isValidIPv4(prefix) {
		return 0
	}
	return 1 << (32 - prefix.Bits())
}

// Compare orders prefixes by address, then by prefix length with the shorter
// prefix first. It returns -1, 0 or +1 and is a total order, so sorting with it
// always gives the same sequence for the same set of prefixes. Pass canonical
// prefixes.
//
// Example: 10.42.0.0/16 sorts before 10.42.0.0/20, which sorts before 10.42.16.0/20.
func Compare(first, second netip.Prefix) int {
	if byAddress := first.Addr().Compare(second.Addr()); byAddress != 0 {
		return byAddress
	}
	return cmp.Compare(first.Bits(), second.Bits())
}

// Children yields the subnets of parent with prefix length childBits, in address order,
// together with their index starting at 0. It never builds the whole list, so
// a /21 of /32s costs nothing until it is iterated, and a caller that stops
// early pays only for what it read. The returned sequence can be iterated more
// than once.
//
// It returns an INVALID_PREFIX error when parent is not a valid IPv4 prefix,
// when childBits is shorter than parent's own length, or when childBits is
// greater than 32. Host bits in parent are ignored.
//
// Example: Children(10.42.0.0/16, 20) yields sixteen /20 blocks, the first
// 10.42.0.0/20 and the last 10.42.240.0/20.
func Children(parent netip.Prefix, childBits int) (iter.Seq2[int, netip.Prefix], error) {
	if !isValidIPv4(parent) {
		return nil, types.Errf(types.ErrInvalidPrefix, parent.String(), "not a valid IPv4 prefix")
	}
	if childBits < parent.Bits() || childBits > 32 {
		return nil, types.Errf(types.ErrInvalidPrefix, parent.String(), "cannot split into /%d (need /%d to /32)", childBits, parent.Bits())
	}
	firstAddress := addrToNumber(parent.Masked().Addr())
	addressesPerChild := uint64(1) << (32 - childBits)
	childCount := uint64(1) << (childBits - parent.Bits())
	return func(yield func(int, netip.Prefix) bool) {
		for index := uint64(0); index < childCount; index++ {
			child := netip.PrefixFrom(numberToAddr(firstAddress+uint32(index*addressesPerChild)), childBits)
			if !yield(int(index), child) {
				return
			}
		}
	}, nil
}

// Split returns every child of parent with prefix length childBits, in address
// order. It accepts the same inputs as Children and returns the same
// INVALID_PREFIX errors. Because it builds a slice, it also refuses a split into
// more than 1,048,576 pieces with INVALID_PREFIX; use Children for larger ones.
//
// Example: Split(10.42.0.0/16, 20) returns sixteen prefixes.
func Split(parent netip.Prefix, childBits int) ([]netip.Prefix, error) {
	children, err := Children(parent, childBits)
	if err != nil {
		return nil, err
	}
	if childBits-parent.Bits() > 20 {
		return nil, types.Errf(types.ErrInvalidPrefix, parent.String(), "splitting into /%d gives more than %d pieces; iterate with Children", childBits, maxSplit)
	}
	pieces := make([]netip.Prefix, 0, 1<<(childBits-parent.Bits()))
	for _, child := range children {
		pieces = append(pieces, child)
	}
	return pieces, nil
}

// halves splits prefix into its two children, one bit longer. The prefix must
// be canonical and shorter than /32.
func halves(prefix netip.Prefix) (lower, upper netip.Prefix) {
	childBits := prefix.Bits() + 1
	lower = netip.PrefixFrom(prefix.Addr(), childBits)
	upperStart := addrToNumber(prefix.Addr()) + uint32(1)<<(32-childBits)
	upper = netip.PrefixFrom(numberToAddr(upperStart), childBits)
	return lower, upper
}

// Subtract returns what is left of parent after removing used, as the fewest
// possible prefixes, sorted by address. It is how IPAM proves that allocated
// plus free space equals the whole supernet exactly: nothing lost, nothing
// counted twice.
//
// Prefixes in used that lie outside parent, or that are invalid, are ignored.
// Prefixes in used may overlap each other or carry host bits. A prefix in used
// that covers all of parent leaves nothing. The result is empty (not nil) when
// nothing is left, and nil when parent is not a valid IPv4 prefix.
//
// Example: Subtract(10.42.0.0/16, [10.42.16.0/20]) returns 10.42.0.0/20,
// 10.42.32.0/19, 10.42.64.0/18 and 10.42.128.0/17.
func Subtract(parent netip.Prefix, used []netip.Prefix) []netip.Prefix {
	if !isValidIPv4(parent) {
		return nil
	}
	free := []netip.Prefix{}
	var collectFree func(block netip.Prefix, touching []netip.Prefix)
	collectFree = func(block netip.Prefix, touching []netip.Prefix) {
		// Keep only the used prefixes that touch this block. If one covers the
		// whole block, the block is fully used. If none touch it, it is free.
		var stillTouching []netip.Prefix
		for _, candidate := range touching {
			if Contains(candidate, block) {
				return
			}
			if Overlaps(candidate, block) {
				stillTouching = append(stillTouching, candidate)
			}
		}
		if len(stillTouching) == 0 {
			free = append(free, block)
			return
		}
		lower, upper := halves(block)
		collectFree(lower, stillTouching)
		collectFree(upper, stillTouching)
	}
	collectFree(parent.Masked(), used)
	return free
}

// Usable returns the addresses of a small prefix that a machine can actually
// be given, in order. Prefixes from /24 to /30 skip the first address (the
// network itself) and the last (the broadcast address). A /31 is a
// point-to-point link and a /32 is a single host, so every address in them is
// usable. Host bits in prefix are ignored.
//
// It returns an INVALID_PREFIX error for a prefix that is not a valid IPv4
// prefix, or that is larger than a /24, which would be pointless to list.
//
// Example: Usable(10.42.252.0/30) returns 10.42.252.1 and 10.42.252.2.
func Usable(prefix netip.Prefix) ([]netip.Addr, error) {
	if !isValidIPv4(prefix) {
		return nil, types.Errf(types.ErrInvalidPrefix, prefix.String(), "not a valid IPv4 prefix")
	}
	if prefix.Bits() < 24 {
		return nil, types.Errf(types.ErrInvalidPrefix, prefix.String(), "too large to list; use /24 or smaller")
	}
	first := uint64(addrToNumber(prefix.Masked().Addr()))
	total := Size(prefix)
	skipped := uint64(0)
	if prefix.Bits() <= 30 {
		skipped = 1 // the network address; the broadcast address is dropped below
		total -= 2
	}
	// Count addresses instead of looping up to a last value, so a prefix at the
	// very top of the address space cannot overflow and loop forever.
	addrs := make([]netip.Addr, 0, total)
	for offset := uint64(0); offset < total; offset++ {
		addrs = append(addrs, numberToAddr(uint32(first+skipped+offset)))
	}
	return addrs, nil
}
