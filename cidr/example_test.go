package cidr_test

// This file holds the runnable examples shown on the package documentation.
// They live beside the code, not in tests/, because pkg.go.dev only shows
// examples from the package's own directory. They use only the exported API.

import (
	"fmt"
	"net/netip"

	"github.com/vectorihub/netctl/cidr"
)

// ExampleSubtract shows Subtract in use.
func ExampleSubtract() {
	free := cidr.Subtract(netip.MustParsePrefix("10.42.0.0/16"), []netip.Prefix{netip.MustParsePrefix("10.42.16.0/20")})
	fmt.Println(free)
	// Output: [10.42.0.0/20 10.42.32.0/19 10.42.64.0/18 10.42.128.0/17]
}

// ExampleUsable shows Usable in use.
func ExampleUsable() {
	addrs, _ := cidr.Usable(netip.MustParsePrefix("10.42.252.0/30"))
	fmt.Println(addrs)
	// Output: [10.42.252.1 10.42.252.2]
}

// ExampleCanonical shows Canonical in use.
func ExampleCanonical() {
	c, hadHostBits := cidr.Canonical(netip.MustParsePrefix("10.42.16.5/20"))
	fmt.Println(c, hadHostBits)
	// Output: 10.42.16.0/20 true
}

// ExampleContains shows Contains in use.
func ExampleContains() {
	fmt.Println(cidr.Contains(netip.MustParsePrefix("10.42.0.0/16"), netip.MustParsePrefix("10.42.16.0/20")))
	fmt.Println(cidr.Contains(netip.MustParsePrefix("10.42.16.0/20"), netip.MustParsePrefix("10.42.0.0/16")))
	// Output:
	// true
	// false
}

// ExampleOverlaps shows Overlaps in use.
func ExampleOverlaps() {
	fmt.Println(cidr.Overlaps(netip.MustParsePrefix("10.42.0.0/16"), netip.MustParsePrefix("10.42.16.0/20")))
	fmt.Println(cidr.Overlaps(netip.MustParsePrefix("10.42.0.0/20"), netip.MustParsePrefix("10.42.16.0/20")))
	// Output:
	// true
	// false
}

// ExampleSize shows Size in use.
func ExampleSize() {
	fmt.Println(cidr.Size(netip.MustParsePrefix("10.42.16.0/20")))
	// Output: 4096
}

// ExampleCompare shows Compare in use.
func ExampleCompare() {
	fmt.Println(cidr.Compare(netip.MustParsePrefix("10.42.0.0/16"), netip.MustParsePrefix("10.42.0.0/20")))
	fmt.Println(cidr.Compare(netip.MustParsePrefix("10.42.16.0/20"), netip.MustParsePrefix("10.42.0.0/20")))
	// Output:
	// -1
	// 1
}

// ExampleSplit shows Split in use.
func ExampleSplit() {
	s, _ := cidr.Split(netip.MustParsePrefix("10.42.0.0/16"), 18)
	fmt.Println(s)
	// Output: [10.42.0.0/18 10.42.64.0/18 10.42.128.0/18 10.42.192.0/18]
}

// ExampleChildren shows Children in use.
func ExampleChildren() {
	seq, _ := cidr.Children(netip.MustParsePrefix("10.42.252.0/22"), 30)
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
