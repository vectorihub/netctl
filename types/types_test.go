package types

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// P1-TYPE-001
func TestValidID(t *testing.T) {
	good := []string{"builder-1", "a", "dgx-spark", "0", "a-b-c", "x1", strings.Repeat("a", 63)}
	for _, s := range good {
		if !ValidID(s) {
			t.Errorf("ValidID(%q) = false, want true", s)
		}
	}
	bad := []string{
		"Builder 1", "", "-x", "x-", "-", strings.Repeat("a", 64),
		"Builder", "a_b", "a b", "a.b", "a~b", "é", "a\n", "\na", " a", "a ",
	}
	for _, s := range bad {
		if ValidID(s) {
			t.Errorf("ValidID(%q) = true, want false", s)
		}
	}
}

// P1-TYPE-002: random pairs of valid IDs give the same link ID both ways, and
// the ID is built from the sorted pair.
func TestLinkIDOfSymmetric(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-"
	randID := func() NodeID {
		for {
			n := 1 + rng.Intn(63)
			b := make([]byte, n)
			for i := range b {
				b[i] = alphabet[rng.Intn(len(alphabet))]
			}
			if ValidID(string(b)) {
				return NodeID(b)
			}
		}
	}
	for i := 0; i < 2000; i++ {
		a, b := randID(), randID()
		ab, ba := LinkIDOf(a, b), LinkIDOf(b, a)
		if ab != ba {
			t.Fatalf("seed 1 case %d: LinkIDOf(%q,%q)=%q but reversed=%q", i, a, b, ab, ba)
		}
		lo, hi := a, b
		if hi < lo {
			lo, hi = hi, lo
		}
		if want := LinkID(string(lo) + "~" + string(hi)); ab != want {
			t.Fatalf("seed 1 case %d: got %q, want %q", i, ab, want)
		}
	}
}

func TestLinkIDOfKnown(t *testing.T) {
	want := LinkID("builder-1~dgx-spark")
	if got := LinkIDOf("dgx-spark", "builder-1"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := LinkIDOf("builder-1", "dgx-spark"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := LinkIDOf("a", "a"); got != "a~a" {
		t.Errorf("self link: got %q", got)
	}
}

func TestRole(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Role
		ok   bool
	}{
		{"hub", RoleHub, true},
		{"spoke", RoleSpoke, true},
		{"relay", RoleUnknown, false}, // reserved for Phase 5 (D10)
		{"Hub", RoleUnknown, false},
		{"", RoleUnknown, false},
	} {
		got, ok := ParseRole(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseRole(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
	for r, s := range map[Role]string{RoleHub: "hub", RoleSpoke: "spoke", RoleUnknown: "unknown", Role(99): "unknown"} {
		if r.String() != s {
			t.Errorf("Role(%d).String() = %q want %q", int(r), r.String(), s)
		}
	}
	var zero Role
	if zero != RoleUnknown {
		t.Error("zero Role must be RoleUnknown")
	}
}

func TestLinkKind(t *testing.T) {
	for _, c := range []struct {
		in   string
		want LinkKind
		ok   bool
	}{
		{"hub-spoke", LinkHubSpoke, true},
		{"direct", LinkDirect, true},
		{"transit", LinkUnknown, false}, // reserved for Phase 5 (D10)
		{"", LinkUnknown, false},
	} {
		got, ok := ParseLinkKind(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseLinkKind(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
	for k, s := range map[LinkKind]string{LinkHubSpoke: "hub-spoke", LinkDirect: "direct", LinkUnknown: "unknown", LinkKind(99): "unknown"} {
		if k.String() != s {
			t.Errorf("LinkKind(%d).String() = %q want %q", int(k), k.String(), s)
		}
	}
	var zero LinkKind
	if zero != LinkUnknown {
		t.Error("zero LinkKind must be LinkUnknown")
	}
}

func TestFixedClock(t *testing.T) {
	t0 := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	var c Clock = &FixedClock{T: t0}
	if !c.Now().Equal(t0) {
		t.Fatalf("Now = %v want %v", c.Now(), t0)
	}
	if !c.Now().Equal(t0) { // does not drift between calls
		t.Fatal("clock moved on its own")
	}
	fc := c.(*FixedClock)
	fc.Advance(90 * time.Minute)
	if want := t0.Add(90 * time.Minute); !c.Now().Equal(want) {
		t.Fatalf("after Advance: %v want %v", c.Now(), want)
	}
	fc.Advance(-90 * time.Minute)
	if !c.Now().Equal(t0) {
		t.Fatalf("after rewind: %v want %v", c.Now(), t0)
	}
	if !(&FixedClock{}).Now().IsZero() {
		t.Error("zero FixedClock must report the zero time")
	}
}

func ExampleValidID() {
	fmt.Println(ValidID("dgx-spark"), ValidID("Builder 1"), ValidID("-x"))
	// Output: true false false
}

func ExampleLinkIDOf() {
	fmt.Println(LinkIDOf("dgx-spark", "builder-1"))
	fmt.Println(LinkIDOf("builder-1", "dgx-spark"))
	// Output:
	// builder-1~dgx-spark
	// builder-1~dgx-spark
}

func ExampleParseRole() {
	r, ok := ParseRole("hub")
	fmt.Println(r, ok)
	r, ok = ParseRole("relay")
	fmt.Println(r, ok)
	// Output:
	// hub true
	// unknown false
}

func ExampleParseLinkKind() {
	k, ok := ParseLinkKind("direct")
	fmt.Println(k, ok)
	// Output: direct true
}

func ExampleFixedClock() {
	c := &FixedClock{T: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	c.Advance(time.Hour)
	fmt.Println(c.Now().Format(time.RFC3339))
	// Output: 2026-11-19T13:00:00Z
}
