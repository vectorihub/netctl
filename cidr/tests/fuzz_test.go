package tests

import (
	"net/netip"
	"testing"

	"github.com/vectorihub/netctl/cidr"
)

// P1-CIDR-009: Canonical is idempotent and never panics, whatever text
// ParsePrefix accepts. Go saves any failing input under testdata/fuzz, where it
// then runs as a regression test on every `go test`.
func FuzzCanonical(f *testing.F) {
	for _, seed := range []string{
		"10.42.16.5/20", "10.42.16.0/20", "0.0.0.0/0", "255.255.255.255/32", "10.42.0.1/31",
		"fd00::1/64", "::/0", "::ffff:10.42.16.5/112", "1.2.3.4/0",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return
		}
		canonical, _ := cidr.Canonical(prefix)
		again, changed := cidr.Canonical(canonical)
		if changed || again != canonical {
			t.Fatalf("Canonical is not idempotent for %q: %s then %s", text, canonical, again)
		}
		if canonical.Bits() != prefix.Bits() || canonical != canonical.Masked() {
			t.Fatalf("Canonical changed the length or left host bits for %q: %s", text, canonical)
		}
		if !cidr.Contains(canonical, canonical) && prefix.Addr().Is4() {
			t.Fatalf("a prefix must contain itself: %s", canonical)
		}
		// None of these may panic on any parsed prefix.
		_ = cidr.Size(prefix)
		_ = cidr.Overlaps(prefix, canonical)
		_ = cidr.Compare(prefix, canonical)
	})
}

// FuzzChildren feeds Children, Split, Subtract and Usable arbitrary text and
// lengths. They may return errors but must never panic, and a split that
// succeeds must stay inside its parent.
func FuzzChildren(f *testing.F) {
	f.Add("10.42.0.0/16", 20)
	f.Add("10.42.252.0/22", 30)
	f.Add("0.0.0.0/0", 32)
	f.Add("255.255.255.252/30", 32)
	f.Add("fd00::/64", 72)
	f.Fuzz(func(t *testing.T, text string, childBits int) {
		parent, err := netip.ParsePrefix(text)
		if err != nil {
			return
		}
		if children, err := cidr.Children(parent, childBits); err == nil {
			seen := 0
			for index, child := range children {
				if index != seen || !cidr.Contains(parent, child) || child.Bits() != childBits {
					t.Fatalf("bad child %d %s of %s into /%d", index, child, parent, childBits)
				}
				if seen++; seen == 64 {
					break
				}
			}
		}
		_, _ = cidr.Split(parent, childBits)
		_ = cidr.Subtract(parent, []netip.Prefix{parent})
		_, _ = cidr.Usable(parent)
	})
}
