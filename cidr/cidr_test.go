package cidr

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/types"
)

var prefixOf = netip.MustParsePrefix

// P1-CIDR-001
func TestSplit(t *testing.T) {
	s, err := Split(prefixOf("10.42.0.0/16"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 16 || s[0] != prefixOf("10.42.0.0/20") || s[15] != prefixOf("10.42.240.0/20") {
		t.Fatalf("got %v", s)
	}
	for i, c := range s {
		if want := prefixOf(fmt.Sprintf("10.42.%d.0/20", i*16)); c != want {
			t.Errorf("child %d = %s want %s", i, c, want)
		}
	}
}

func TestSplitSameLengthAndHostBits(t *testing.T) {
	s, err := Split(prefixOf("10.42.16.0/20"), 20)
	if err != nil || len(s) != 1 || s[0] != prefixOf("10.42.16.0/20") {
		t.Fatalf("%v %v", s, err)
	}
	s, err = Split(prefixOf("10.42.16.5/20"), 21) // host bits ignored
	if err != nil || len(s) != 2 || s[0] != prefixOf("10.42.16.0/21") || s[1] != prefixOf("10.42.24.0/21") {
		t.Fatalf("%v %v", s, err)
	}
	s, err = Split(prefixOf("10.42.0.7/32"), 32)
	if err != nil || len(s) != 1 || s[0] != prefixOf("10.42.0.7/32") {
		t.Fatalf("%v %v", s, err)
	}
}

// P1-CIDR-002
func TestSplitBadSizes(t *testing.T) {
	cases := []struct {
		name string
		p    netip.Prefix
		bits int
	}{
		{"shorter than parent", prefixOf("10.42.0.0/16"), 8},
		{"over 32", prefixOf("10.42.0.0/16"), 33},
		{"negative", prefixOf("10.42.0.0/16"), -1},
		{"ipv6", prefixOf("fd00::/64"), 72},
		{"zero prefix", netip.Prefix{}, 24},
		{"too many pieces", prefixOf("0.0.0.0/0"), 32},
		{"just over the cap", prefixOf("10.0.0.0/8"), 29},
	}
	for _, c := range cases {
		out, err := Split(c.p, c.bits)
		if !types.HasCode(err, types.ErrInvalidPrefix) || out != nil {
			t.Errorf("%s: want INVALID_PREFIX, got %v, %v", c.name, out, err)
		}
		if seq, err := Children(c.p, c.bits); c.name != "too many pieces" && c.name != "just over the cap" {
			if !types.HasCode(err, types.ErrInvalidPrefix) || seq != nil {
				t.Errorf("%s: Children want INVALID_PREFIX, got %v", c.name, err)
			}
		}
	}
	// exactly at the cap is allowed
	if s, err := Split(prefixOf("10.0.0.0/8"), 28); err != nil || len(s) != 1<<20 {
		t.Errorf("cap boundary: %d %v", len(s), err)
	}
}

// P1-CIDR-007
func TestCanonical(t *testing.T) {
	c, had := Canonical(prefixOf("10.42.16.5/20"))
	if c != prefixOf("10.42.16.0/20") || !had {
		t.Fatal(c, had)
	}
	c, had = Canonical(prefixOf("10.42.16.0/20"))
	if c != prefixOf("10.42.16.0/20") || had {
		t.Fatal(c, had)
	}
	if c, had := Canonical(netip.Prefix{}); c.IsValid() || had {
		t.Fatal("invalid prefix must pass through unchanged", c, had)
	}
	if c, had := Canonical(prefixOf("fd00::1/64")); c != prefixOf("fd00::/64") || !had {
		t.Fatal(c, had)
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		parent, child string
		want          bool
	}{
		{"10.42.0.0/16", "10.42.16.0/20", true},
		{"10.42.0.0/16", "10.42.0.0/16", true},
		{"10.42.16.0/20", "10.42.0.0/16", false},
		{"10.42.0.0/20", "10.42.16.0/20", false},
		{"10.42.0.0/16", "10.43.0.0/24", false},
		{"0.0.0.0/0", "10.42.1.2/32", true},
		{"10.42.16.0/20", "10.42.31.255/32", true},
		{"10.42.16.0/20", "10.42.32.0/32", false},
		{"10.42.0.0/16", "fd00::/64", false},
	}
	for _, c := range cases {
		if got := Contains(prefixOf(c.parent), prefixOf(c.child)); got != c.want {
			t.Errorf("Contains(%s,%s) = %v", c.parent, c.child, got)
		}
	}
	if Contains(netip.Prefix{}, prefixOf("10.0.0.0/8")) || Contains(prefixOf("10.0.0.0/8"), netip.Prefix{}) {
		t.Error("invalid prefixes must never match")
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.42.0.0/16", "10.42.16.0/20", true},
		{"10.42.16.0/20", "10.42.0.0/16", true},
		{"10.42.0.0/20", "10.42.16.0/20", false},
		{"10.42.16.5/20", "10.42.16.0/20", true}, // host bits ignored
		{"10.42.0.0/16", "10.43.0.0/16", false},
		{"10.42.0.1/32", "10.42.0.1/32", true},
		{"10.42.0.0/16", "fd00::/16", false},
	}
	for _, c := range cases {
		if got := Overlaps(prefixOf(c.a), prefixOf(c.b)); got != c.want {
			t.Errorf("Overlaps(%s,%s) = %v", c.a, c.b, got)
		}
	}
	if Overlaps(netip.Prefix{}, prefixOf("10.0.0.0/8")) {
		t.Error("invalid prefix must not overlap")
	}
}

func TestSize(t *testing.T) {
	for s, want := range map[string]uint64{
		"10.42.16.0/20": 4096, "10.42.0.1/32": 1, "10.42.0.0/31": 2, "10.42.0.0/30": 4,
		"10.42.0.0/16": 65536, "0.0.0.0/0": 1 << 32, "fd00::/64": 0,
	} {
		if got := Size(prefixOf(s)); got != want {
			t.Errorf("Size(%s) = %d want %d", s, got, want)
		}
	}
	if Size(netip.Prefix{}) != 0 {
		t.Error("Size of an invalid prefix must be 0")
	}
}

func TestCompare(t *testing.T) {
	ordered := []string{"10.42.0.0/16", "10.42.0.0/20", "10.42.0.0/32", "10.42.16.0/20", "10.43.0.0/16", "192.168.0.0/24"}
	for i, a := range ordered {
		for j, b := range ordered {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(prefixOf(a), prefixOf(b)); got != want {
				t.Errorf("Compare(%s,%s) = %d want %d", a, b, got, want)
			}
		}
	}
	shuffled := []netip.Prefix{prefixOf("192.168.0.0/24"), prefixOf("10.42.16.0/20"), prefixOf("10.42.0.0/20"), prefixOf("10.42.0.0/16"), prefixOf("10.43.0.0/16"), prefixOf("10.42.0.0/32")}
	slices.SortFunc(shuffled, Compare)
	for i, s := range shuffled {
		if s != prefixOf(ordered[i]) {
			t.Fatalf("sorted[%d] = %s want %s", i, s, ordered[i])
		}
	}
}

// A /21 of /32s costs nothing until iterated, and stopping early is free.
func TestChildrenIsLazy(t *testing.T) {
	seq, err := Children(prefixOf("10.0.0.0/8"), 32) // 16,777,216 children
	if err != nil {
		t.Fatal(err)
	}
	var got []netip.Prefix
	for i, c := range seq {
		if i == 3 {
			break
		}
		got = append(got, c)
	}
	want := []netip.Prefix{prefixOf("10.0.0.0/32"), prefixOf("10.0.0.1/32"), prefixOf("10.0.0.2/32")}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	// Starting a split costs the same however many children it has.
	cost := func(parent netip.Prefix, bits int) float64 {
		return testing.AllocsPerRun(100, func() {
			s, _ := Children(parent, bits)
			for range s {
				break
			}
		})
	}
	small, huge := cost(prefixOf("10.42.0.0/24"), 25), cost(prefixOf("10.0.0.0/8"), 32)
	if huge != small {
		t.Errorf("allocations grew with split size: %v for 2 children, %v for 16M", small, huge)
	}
}

func TestChildrenOverlayPoolAndReiterate(t *testing.T) {
	seq, err := Children(prefixOf("10.42.240.0/21"), 32)
	if err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ { // can be iterated more than once
		n := 0
		var last netip.Prefix
		for i, c := range seq {
			if i != n {
				t.Fatalf("index %d at position %d", i, n)
			}
			n, last = n+1, c
		}
		if n != 2048 || last != prefixOf("10.42.247.255/32") {
			t.Fatalf("pass %d: %d children, last %s", pass, n, last)
		}
	}
	seq, _ = Children(prefixOf("0.0.0.0/0"), 32) // top of the range must not wrap
	n := 0
	for range seq {
		n++
		if n == 5 {
			break
		}
	}
	seq, _ = Children(prefixOf("255.255.255.252/30"), 32)
	var last netip.Prefix
	for _, c := range seq {
		last = c
	}
	if last != prefixOf("255.255.255.255/32") {
		t.Fatalf("end of address space: %s", last)
	}
}

// Children partition the parent: contiguous, in order, none outside.
func TestChildrenPartitionProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 500; i++ {
		bits := 8 + r.IntN(17)
		p := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(r.IntN(256)), byte(r.IntN(256)), byte(r.IntN(256)), byte(r.IntN(256))}), bits).Masked()
		child := bits + r.IntN(33-bits)
		if child-bits > 12 {
			child = bits + 12
		}
		seq, err := Children(p, child)
		if err != nil {
			t.Fatalf("seed 1,2 case %d: %v", i, err)
		}
		var total uint64
		var prev netip.Prefix
		n := 0
		for idx, c := range seq {
			if idx != n || !Contains(p, c) || c.Bits() != child || c != c.Masked() {
				t.Fatalf("seed 1,2 case %d: bad child %d %s of %s", i, idx, c, p)
			}
			if n > 0 && (Compare(prev, c) >= 0 || Overlaps(prev, c)) {
				t.Fatalf("seed 1,2 case %d: %s then %s", i, prev, c)
			}
			total, prev, n = total+Size(c), c, n+1
		}
		if total != Size(p) {
			t.Fatalf("seed 1,2 case %d: children cover %d of %d", i, total, Size(p))
		}
	}
}

// P1-CIDR-003
func TestSubtractNothingUsed(t *testing.T) {
	parent := prefixOf("10.42.0.0/16")
	for _, used := range [][]netip.Prefix{nil, {}} {
		if got := Subtract(parent, used); !slices.Equal(got, []netip.Prefix{parent}) {
			t.Fatalf("got %v want [%s]", got, parent)
		}
	}
}

// P1-CIDR-004
func TestSubtract(t *testing.T) {
	got := fmt.Sprint(Subtract(prefixOf("10.42.0.0/16"), []netip.Prefix{prefixOf("10.42.16.0/20")}))
	want := "[10.42.0.0/20 10.42.32.0/19 10.42.64.0/18 10.42.128.0/17]"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSubtractEdgeCases(t *testing.T) {
	parent := prefixOf("10.42.0.0/20")
	cases := []struct {
		name string
		used []string
		want string
	}{
		{"whole parent used", []string{"10.42.0.0/20"}, "[]"},
		{"bigger prefix covers parent", []string{"10.42.0.0/16"}, "[]"},
		{"outside parent is ignored", []string{"10.43.0.0/24", "192.168.0.0/16"}, "[10.42.0.0/20]"},
		{"host bits are ignored", []string{"10.42.0.5/21"}, "[10.42.8.0/21]"},
		{"overlapping used prefixes", []string{"10.42.0.0/22", "10.42.0.0/21", "10.42.2.0/23"}, "[10.42.8.0/21]"},
		{"first address used", []string{"10.42.0.0/32"}, "[10.42.0.1/32 10.42.0.2/31 10.42.0.4/30 10.42.0.8/29 10.42.0.16/28 10.42.0.32/27 10.42.0.64/26 10.42.0.128/25 10.42.1.0/24 10.42.2.0/23 10.42.4.0/22 10.42.8.0/21]"},
		{"two used halves", []string{"10.42.0.0/21", "10.42.8.0/21"}, "[]"},
	}
	for _, tc := range cases {
		var used []netip.Prefix
		for _, text := range tc.used {
			used = append(used, prefixOf(text))
		}
		if got := fmt.Sprint(Subtract(parent, used)); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
	if got := Subtract(parent, []netip.Prefix{{}}); !slices.Equal(got, []netip.Prefix{parent}) {
		t.Errorf("invalid used prefix must be ignored, got %v", got)
	}
	if got := Subtract(prefixOf("10.42.0.7/20"), nil); !slices.Equal(got, []netip.Prefix{parent}) {
		t.Errorf("parent host bits must be masked, got %v", got)
	}
	if Subtract(netip.Prefix{}, nil) != nil || Subtract(prefixOf("fd00::/64"), nil) != nil {
		t.Error("invalid parent must give nil")
	}
	if got := Subtract(parent, []netip.Prefix{prefixOf("10.42.0.0/20")}); got == nil {
		t.Error("an empty result must be non-nil")
	}
	// the input slice must not be modified
	used := []netip.Prefix{prefixOf("10.42.8.0/21"), prefixOf("10.42.0.0/22")}
	before := slices.Clone(used)
	Subtract(parent, used)
	if !slices.Equal(used, before) {
		t.Error("Subtract modified its input")
	}
}

// Checks Subtract against a brute-force model on a /24, address by address:
// the free prefixes must be exactly the unused addresses, sorted, with no
// overlap, and minimal (no two neighbours could have been one prefix).
// The 2,000-case /16 property test is P1-CIDR-005 in V12.
func TestSubtractMatchesBruteForce(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 11))
	parent := prefixOf("10.0.0.0/24")
	for caseNumber := 0; caseNumber < 500; caseNumber++ {
		var used []netip.Prefix
		var isUsed [256]bool
		for i := random.IntN(6); i > 0; i-- {
			bits := 24 + random.IntN(9)
			start := byte(random.IntN(256))
			prefix := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 0, 0, start}), bits)
			used = append(used, prefix)
			for offset := 0; offset < 256; offset++ {
				if prefix.Contains(netip.AddrFrom4([4]byte{10, 0, 0, byte(offset)})) {
					isUsed[offset] = true
				}
			}
		}
		var isFree [256]bool
		free := Subtract(parent, used)
		for index, block := range free {
			if index > 0 && Compare(free[index-1], block) >= 0 {
				t.Fatalf("seed 7,11 case %d: not sorted: %v", caseNumber, free)
			}
			if index > 0 && canMerge(free[index-1], block) {
				t.Fatalf("seed 7,11 case %d: %s and %s should be one prefix", caseNumber, free[index-1], block)
			}
			for offset := 0; offset < 256; offset++ {
				if block.Contains(netip.AddrFrom4([4]byte{10, 0, 0, byte(offset)})) {
					if isFree[offset] {
						t.Fatalf("seed 7,11 case %d: address %d listed twice", caseNumber, offset)
					}
					isFree[offset] = true
				}
			}
		}
		for offset := 0; offset < 256; offset++ {
			if isFree[offset] == isUsed[offset] {
				t.Fatalf("seed 7,11 case %d: address %d used=%v free=%v (used %v, free %v)", caseNumber, offset, isUsed[offset], isFree[offset], used, free)
			}
		}
	}
}

// canMerge reports whether first and second are the two halves of one prefix.
func canMerge(first, second netip.Prefix) bool {
	if first.Bits() != second.Bits() || first.Bits() == 0 {
		return false
	}
	parent := netip.PrefixFrom(first.Addr(), first.Bits()-1).Masked()
	return parent.Contains(first.Addr()) && parent.Contains(second.Addr())
}

// P1-CIDR-006
func TestUsable(t *testing.T) {
	cases := []struct {
		prefix string
		want   string
	}{
		{"10.42.252.0/30", "[10.42.252.1 10.42.252.2]"},
		{"10.42.252.4/31", "[10.42.252.4 10.42.252.5]"},
		{"10.42.240.7/32", "[10.42.240.7]"},
		{"10.42.252.5/30", "[10.42.252.5 10.42.252.6]"}, // host bits ignored: block is .4/30
		{"10.42.0.0/29", "[10.42.0.1 10.42.0.2 10.42.0.3 10.42.0.4 10.42.0.5 10.42.0.6]"},
	}
	for _, tc := range cases {
		got, err := Usable(prefixOf(tc.prefix))
		if err != nil || fmt.Sprint(got) != tc.want {
			t.Errorf("Usable(%s) = %v, %v want %s", tc.prefix, got, err, tc.want)
		}
	}
	whole, err := Usable(prefixOf("10.42.1.0/24"))
	if err != nil || len(whole) != 254 || whole[0] != netip.MustParseAddr("10.42.1.1") || whole[253] != netip.MustParseAddr("10.42.1.254") {
		t.Errorf("/24: %d addresses, %v", len(whole), err)
	}
}

func TestUsableRefusals(t *testing.T) {
	for _, text := range []string{"10.42.0.0/23", "10.42.0.0/16", "0.0.0.0/0", "fd00::/120"} {
		if got, err := Usable(prefixOf(text)); !types.HasCode(err, types.ErrInvalidPrefix) || got != nil {
			t.Errorf("Usable(%s) = %v, %v want INVALID_PREFIX", text, got, err)
		}
	}
	if got, err := Usable(netip.Prefix{}); !types.HasCode(err, types.ErrInvalidPrefix) || got != nil {
		t.Errorf("zero prefix: %v, %v", got, err)
	}
}

// The top of the address space must not wrap around or loop forever.
func TestUsableAtTopOfAddressSpace(t *testing.T) {
	for text, want := range map[string]string{
		"255.255.255.255/32": "[255.255.255.255]",
		"255.255.255.254/31": "[255.255.255.254 255.255.255.255]",
		"255.255.255.252/30": "[255.255.255.253 255.255.255.254]",
	} {
		got, err := Usable(prefixOf(text))
		if err != nil || fmt.Sprint(got) != want {
			t.Errorf("Usable(%s) = %v, %v want %s", text, got, err, want)
		}
	}
	if got, err := Usable(prefixOf("255.255.255.0/24")); err != nil || len(got) != 254 || got[253] != netip.MustParseAddr("255.255.255.254") {
		t.Errorf("top /24: %d addresses, %v", len(got), err)
	}
}

func ExampleSubtract() {
	free := Subtract(prefixOf("10.42.0.0/16"), []netip.Prefix{prefixOf("10.42.16.0/20")})
	fmt.Println(free)
	// Output: [10.42.0.0/20 10.42.32.0/19 10.42.64.0/18 10.42.128.0/17]
}

func ExampleUsable() {
	addrs, _ := Usable(prefixOf("10.42.252.0/30"))
	fmt.Println(addrs)
	// Output: [10.42.252.1 10.42.252.2]
}

func ExampleCanonical() {
	c, hadHostBits := Canonical(netip.MustParsePrefix("10.42.16.5/20"))
	fmt.Println(c, hadHostBits)
	// Output: 10.42.16.0/20 true
}

func ExampleContains() {
	fmt.Println(Contains(prefixOf("10.42.0.0/16"), prefixOf("10.42.16.0/20")))
	fmt.Println(Contains(prefixOf("10.42.16.0/20"), prefixOf("10.42.0.0/16")))
	// Output:
	// true
	// false
}

func ExampleOverlaps() {
	fmt.Println(Overlaps(prefixOf("10.42.0.0/16"), prefixOf("10.42.16.0/20")))
	fmt.Println(Overlaps(prefixOf("10.42.0.0/20"), prefixOf("10.42.16.0/20")))
	// Output:
	// true
	// false
}

func ExampleSize() {
	fmt.Println(Size(prefixOf("10.42.16.0/20")))
	// Output: 4096
}

func ExampleCompare() {
	fmt.Println(Compare(prefixOf("10.42.0.0/16"), prefixOf("10.42.0.0/20")))
	fmt.Println(Compare(prefixOf("10.42.16.0/20"), prefixOf("10.42.0.0/20")))
	// Output:
	// -1
	// 1
}

func ExampleSplit() {
	s, _ := Split(prefixOf("10.42.0.0/16"), 18)
	fmt.Println(s)
	// Output: [10.42.0.0/18 10.42.64.0/18 10.42.128.0/18 10.42.192.0/18]
}

func ExampleChildren() {
	seq, _ := Children(prefixOf("10.42.252.0/22"), 30)
	for i, c := range seq {
		if i == 2 {
			break
		}
		fmt.Println(i, c)
	}
	// Output:
	// 0 10.42.252.0/30
	// 1 10.42.252.4/30
}
