package tests

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/cidr"
)

// Every random case is built from its own generator, seeded with (propertySeed,
// caseNumber). A failure prints both numbers, so one case can be replayed on its
// own without running the thousands before it.
const (
	propertySeed  = 20261119
	propertyCases = 5000
)

// caseRandom returns the random source for one numbered test case, so every case is reproducible
// from the fixed seed.
func caseRandom(caseNumber int) *rand.Rand {
	return rand.New(rand.NewPCG(propertySeed, uint64(caseNumber)))
}

// addrToNumber and numberToAddr turn an IPv4 address into its number and back.
// They are local copies: the tests reach cidr only through its exported names.
func addrToNumber(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
}

// numberToAddr turns a 32-bit number into the IPv4 address it stands for.
func numberToAddr(number uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(number >> 24), byte(number >> 16), byte(number >> 8), byte(number)})
}

// randomPrefixIn returns a random canonical prefix inside parent, between
// minBits and maxBits long.
func randomPrefixIn(random *rand.Rand, parent netip.Prefix, minBits, maxBits int) netip.Prefix {
	bits := minBits + random.IntN(maxBits-minBits+1)
	span := uint64(1) << (32 - parent.Bits())
	address := addrToNumber(parent.Masked().Addr()) + uint32(random.Uint64N(span))
	return netip.PrefixFrom(numberToAddr(address), bits).Masked()
}

// addressRange is a run of addresses, first to last inclusive.
type addressRange struct{ first, last uint64 }

// unionSize counts the addresses covered by at least one of prefixes, clipped
// to parent. It merges ranges rather than using Subtract, so it is independent
// of the code under test.
func unionSize(parent netip.Prefix, prefixes []netip.Prefix) uint64 {
	var ranges []addressRange
	for _, prefix := range prefixes {
		if !cidr.Overlaps(parent, prefix) {
			continue
		}
		first := uint64(addrToNumber(prefix.Masked().Addr()))
		ranges = append(ranges, addressRange{first, first + cidr.Size(prefix) - 1})
	}
	slices.SortFunc(ranges, func(a, b addressRange) int {
		if a.first != b.first {
			return int(int64(a.first) - int64(b.first))
		}
		return int(int64(a.last) - int64(b.last))
	})
	var total uint64
	var current addressRange
	started := false
	parentFirst := uint64(addrToNumber(parent.Masked().Addr()))
	parentLast := parentFirst + cidr.Size(parent) - 1
	for _, next := range ranges {
		next.first, next.last = max(next.first, parentFirst), min(next.last, parentLast)
		switch {
		case !started:
			current, started = next, true
		case next.first <= current.last+1:
			current.last = max(current.last, next.last)
		default:
			total += current.last - current.first + 1
			current = next
		}
	}
	if started {
		total += current.last - current.first + 1
	}
	return total
}

// checkSubtract is the rule set every Subtract answer must satisfy. Failing
// cases found by the random tests are kept as plain unit tests that call it.
func checkSubtract(t *testing.T, label string, parent netip.Prefix, used []netip.Prefix) {
	t.Helper()
	free := cidr.Subtract(parent, used)
	var freeSize uint64
	for index, block := range free {
		if block != block.Masked() || !cidr.Contains(parent, block) {
			t.Fatalf("%s: free block %s is not canonical or not inside %s", label, block, parent)
		}
		for _, taken := range used {
			if cidr.Overlaps(block, taken) {
				t.Fatalf("%s: free %s overlaps used %s", label, block, taken)
			}
		}
		if index > 0 {
			if cidr.Compare(free[index-1], block) >= 0 || cidr.Overlaps(free[index-1], block) {
				t.Fatalf("%s: free blocks %s then %s are not sorted and separate", label, free[index-1], block)
			}
			if canMerge(free[index-1], block) {
				t.Fatalf("%s: %s and %s should be one prefix (not minimal)", label, free[index-1], block)
			}
		}
		freeSize += cidr.Size(block)
	}
	if want := cidr.Size(parent) - unionSize(parent, used); freeSize != want {
		t.Fatalf("%s: free space is %d addresses, want %d (used %v, free %v)", label, freeSize, want, used, free)
	}
}

// P1-CIDR-005: used + free == parent and free never overlaps used, over
// thousands of seeded random cases on the real supernet.
func TestSubtractProperty(t *testing.T) {
	parent := prefixOf("10.42.0.0/16")
	for caseNumber := 0; caseNumber < propertyCases; caseNumber++ {
		random := caseRandom(caseNumber)
		var used []netip.Prefix
		var usedSize uint64
		for attempts := random.IntN(14); attempts > 0; attempts-- {
			candidate := randomPrefixIn(random, parent, 16, 32)
			clashes := false
			for _, taken := range used {
				clashes = clashes || cidr.Overlaps(taken, candidate)
			}
			if !clashes {
				used, usedSize = append(used, candidate), usedSize+cidr.Size(candidate)
			}
		}
		label := fmt.Sprintf("seed %d case %d", propertySeed, caseNumber)
		checkSubtract(t, label, parent, used)
		var freeSize uint64
		for _, block := range cidr.Subtract(parent, used) {
			freeSize += cidr.Size(block)
		}
		if usedSize+freeSize != cidr.Size(parent) {
			t.Fatalf("%s: used %d + free %d != %d", label, usedSize, freeSize, cidr.Size(parent))
		}
	}
}

// Same rules when the used prefixes overlap each other, repeat, or carry host
// bits, which is what hostile or sloppy input looks like.
func TestSubtractPropertyMessyInput(t *testing.T) {
	parent := prefixOf("10.42.0.0/16")
	for caseNumber := 0; caseNumber < propertyCases; caseNumber++ {
		random := caseRandom(caseNumber)
		var used []netip.Prefix
		for count := random.IntN(14); count > 0; count-- {
			prefix := randomPrefixIn(random, parent, 12, 32)
			if random.IntN(3) == 0 { // add host bits
				host := addrToNumber(prefix.Addr()) | uint32(random.Uint64N(cidr.Size(prefix)))
				prefix = netip.PrefixFrom(numberToAddr(host), prefix.Bits())
			}
			used = append(used, prefix)
			if random.IntN(4) == 0 {
				used = append(used, prefix) // exact repeat
			}
		}
		checkSubtract(t, fmt.Sprintf("seed %d case %d", propertySeed, caseNumber), parent, used)
	}
}

// sign returns -1, 0 or 1 according to the sign of number, so comparisons can be checked without
// caring about magnitude.
func sign(number int) int {
	switch {
	case number < 0:
		return -1
	case number > 0:
		return 1
	}
	return 0
}

// P1-CIDR-008: Compare is antisymmetric and transitive over random triples.
// Prefixes come from a small pool of addresses so ties on the address, and
// differences only in length, happen constantly.
func TestCompareIsATotalOrder(t *testing.T) {
	pool := []string{"10.42.0.0", "10.42.0.1", "10.42.16.0", "10.42.240.0", "10.43.0.0", "192.168.1.0", "0.0.0.0", "255.255.255.255"}
	randomPrefix := func(random *rand.Rand) netip.Prefix {
		return netip.PrefixFrom(netip.MustParseAddr(pool[random.IntN(len(pool))]), random.IntN(33))
	}
	for caseNumber := 0; caseNumber < 20000; caseNumber++ {
		random := caseRandom(caseNumber)
		a, b, c := randomPrefix(random), randomPrefix(random), randomPrefix(random)
		label := fmt.Sprintf("seed %d case %d: a=%s b=%s c=%s", propertySeed, caseNumber, a, b, c)
		if sign(cidr.Compare(a, b)) != -sign(cidr.Compare(b, a)) {
			t.Fatalf("%s: not antisymmetric", label)
		}
		if (cidr.Compare(a, b) == 0) != (a == b) {
			t.Fatalf("%s: Compare(a,b)==0 must mean a==b", label)
		}
		if cidr.Compare(a, a) != 0 {
			t.Fatalf("%s: not reflexive", label)
		}
		if cidr.Compare(a, b) <= 0 && cidr.Compare(b, c) <= 0 && cidr.Compare(a, c) > 0 {
			t.Fatalf("%s: not transitive", label)
		}
	}
}

// Sorting any shuffle of the same prefixes gives the same sequence, so output
// never depends on input order.
func TestCompareSortIsStable(t *testing.T) {
	parent := prefixOf("10.42.0.0/16")
	for caseNumber := 0; caseNumber < 500; caseNumber++ {
		random := caseRandom(caseNumber)
		var prefixes []netip.Prefix
		for count := 2 + random.IntN(20); count > 0; count-- {
			prefixes = append(prefixes, randomPrefixIn(random, parent, 16, 32))
		}
		sorted := slices.Clone(prefixes)
		slices.SortFunc(sorted, cidr.Compare)
		random.Shuffle(len(prefixes), func(i, j int) { prefixes[i], prefixes[j] = prefixes[j], prefixes[i] })
		slices.SortFunc(prefixes, cidr.Compare)
		if !slices.Equal(sorted, prefixes) {
			t.Fatalf("seed %d case %d: sort depends on input order", propertySeed, caseNumber)
		}
	}
}

// Regression slot: a case the random tests once failed on goes here as a
// plain unit test, with the seed and case number it was found at.
func TestSubtractRegressions(t *testing.T) {
	cases := []struct {
		name string
		used []string
	}{
		// Kept from hand-built boundary cases. Add failing random cases below.
		{"first and last address of the supernet", []string{"10.42.0.0/32", "10.42.255.255/32"}},
		{"same prefix three times", []string{"10.42.16.0/20", "10.42.16.0/20", "10.42.16.0/20"}},
		{"child inside an already-used parent", []string{"10.42.0.0/17", "10.42.1.0/24"}},
	}
	for _, tc := range cases {
		var used []netip.Prefix
		for _, text := range tc.used {
			used = append(used, prefixOf(text))
		}
		checkSubtract(t, tc.name, prefixOf("10.42.0.0/16"), used)
	}
}
