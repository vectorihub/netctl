package tests

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/types"
)

// P1-TYPE-001
//
// TestValidID checks which strings are valid IDs: lower-case letters, digits and hyphens, up to 63
// characters.
func TestValidID(t *testing.T) {
	good := []string{"builder-1", "a", "dgx-spark", "0", "a-b-c", "x1", strings.Repeat("a", 63)}
	for _, s := range good {
		if !types.ValidID(s) {
			t.Errorf("ValidID(%q) = false, want true", s)
		}
	}
	bad := []string{
		"Builder 1", "", "-x", "x-", "-", strings.Repeat("a", 64),
		"Builder", "a_b", "a b", "a.b", "a~b", "é", "a\n", "\na", " a", "a ",
	}
	for _, s := range bad {
		if types.ValidID(s) {
			t.Errorf("ValidID(%q) = true, want false", s)
		}
	}
}

// P1-TYPE-002: random pairs of valid IDs give the same link ID both ways, and
// the ID is built from the sorted pair.
func TestLinkIDOfSymmetric(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-"
	randID := func() types.NodeID {
		for {
			n := 1 + rng.Intn(63)
			b := make([]byte, n)
			for i := range b {
				b[i] = alphabet[rng.Intn(len(alphabet))]
			}
			if types.ValidID(string(b)) {
				return types.NodeID(b)
			}
		}
	}
	for i := 0; i < 2000; i++ {
		a, b := randID(), randID()
		ab, ba := types.LinkIDOf(a, b), types.LinkIDOf(b, a)
		if ab != ba {
			t.Fatalf("seed 1 case %d: LinkIDOf(%q,%q)=%q but reversed=%q", i, a, b, ab, ba)
		}
		lo, hi := a, b
		if hi < lo {
			lo, hi = hi, lo
		}
		if want := types.LinkID(string(lo) + "~" + string(hi)); ab != want {
			t.Fatalf("seed 1 case %d: got %q, want %q", i, ab, want)
		}
	}
}

// TestLinkIDOfKnown checks the reference link ID, builder-1~dgx-spark, from either argument order.
func TestLinkIDOfKnown(t *testing.T) {
	want := types.LinkID("builder-1~dgx-spark")
	if got := types.LinkIDOf("dgx-spark", "builder-1"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := types.LinkIDOf("builder-1", "dgx-spark"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := types.LinkIDOf("a", "a"); got != "a~a" {
		t.Errorf("self link: got %q", got)
	}
}

// TestRole checks parsing and printing of roles and that unknown text is refused.
func TestRole(t *testing.T) {
	for _, c := range []struct {
		in   string
		want types.Role
		ok   bool
	}{
		{"hub", types.RoleHub, true},
		{"spoke", types.RoleSpoke, true},
		{"relay", types.RoleUnknown, false}, // reserved for Phase 5 (D10)
		{"Hub", types.RoleUnknown, false},
		{"", types.RoleUnknown, false},
	} {
		got, ok := types.ParseRole(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseRole(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
	for r, s := range map[types.Role]string{types.RoleHub: "hub", types.RoleSpoke: "spoke", types.RoleUnknown: "unknown", types.Role(99): "unknown"} {
		if r.String() != s {
			t.Errorf("Role(%d).String() = %q want %q", int(r), r.String(), s)
		}
	}
	var zero types.Role
	if zero != types.RoleUnknown {
		t.Error("zero Role must be RoleUnknown")
	}
}

// TestLinkKind checks parsing and printing of link kinds and that unknown text is refused.
func TestLinkKind(t *testing.T) {
	for _, c := range []struct {
		in   string
		want types.LinkKind
		ok   bool
	}{
		{"hub-spoke", types.LinkHubSpoke, true},
		{"direct", types.LinkDirect, true},
		{"transit", types.LinkUnknown, false}, // reserved for Phase 5 (D10)
		{"", types.LinkUnknown, false},
	} {
		got, ok := types.ParseLinkKind(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseLinkKind(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
	for k, s := range map[types.LinkKind]string{types.LinkHubSpoke: "hub-spoke", types.LinkDirect: "direct", types.LinkUnknown: "unknown", types.LinkKind(99): "unknown"} {
		if k.String() != s {
			t.Errorf("LinkKind(%d).String() = %q want %q", int(k), k.String(), s)
		}
	}
	var zero types.LinkKind
	if zero != types.LinkUnknown {
		t.Error("zero LinkKind must be LinkUnknown")
	}
}

// TestFixedClock checks that a FixedClock reports its time and moves only when advanced.
func TestFixedClock(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	var c types.Clock = &types.FixedClock{T: t0}
	if !c.Now().Equal(t0) {
		t.Fatalf("Now = %v want %v", c.Now(), t0)
	}
	if !c.Now().Equal(t0) { // does not drift between calls
		t.Fatal("clock moved on its own")
	}
	fc := c.(*types.FixedClock)
	fc.Advance(90 * time.Minute)
	if want := t0.Add(90 * time.Minute); !c.Now().Equal(want) {
		t.Fatalf("after Advance: %v want %v", c.Now(), want)
	}
	fc.Advance(-90 * time.Minute)
	if !c.Now().Equal(t0) {
		t.Fatalf("after rewind: %v want %v", c.Now(), t0)
	}
	if !(&types.FixedClock{}).Now().IsZero() {
		t.Error("zero FixedClock must report the zero time")
	}
}

// ExampleValidID shows ValidID in use; `go test` runs it and checks the "Output:" comment, so the
// example in the documentation cannot go stale.
func ExampleValidID() {
	fmt.Println(types.ValidID("dgx-spark"), types.ValidID("Builder 1"), types.ValidID("-x"))
	// Output: true false false
}

// ExampleLinkIDOf shows LinkIDOf in use; `go test` runs it and checks the "Output:" comment, so the
// example in the documentation cannot go stale.
func ExampleLinkIDOf() {
	fmt.Println(types.LinkIDOf("dgx-spark", "builder-1"))
	fmt.Println(types.LinkIDOf("builder-1", "dgx-spark"))
	// Output:
	// builder-1~dgx-spark
	// builder-1~dgx-spark
}

// ExampleParseRole shows ParseRole in use; `go test` runs it and checks the "Output:" comment, so the
// example in the documentation cannot go stale.
func ExampleParseRole() {
	r, ok := types.ParseRole("hub")
	fmt.Println(r, ok)
	r, ok = types.ParseRole("relay")
	fmt.Println(r, ok)
	// Output:
	// hub true
	// unknown false
}

// ExampleParseLinkKind shows ParseLinkKind in use; `go test` runs it and checks the "Output:" comment, so the
// example in the documentation cannot go stale.
func ExampleParseLinkKind() {
	k, ok := types.ParseLinkKind("direct")
	fmt.Println(k, ok)
	// Output: direct true
}

// ExampleFixedClock shows FixedClock in use; `go test` runs it and checks the "Output:" comment, so the
// example in the documentation cannot go stale.
func ExampleFixedClock() {
	c := &types.FixedClock{T: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	c.Advance(time.Hour)
	fmt.Println(c.Now().Format(time.RFC3339))
	// Output: 2026-11-19T13:00:00Z
}
