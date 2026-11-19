package types_test

// This file holds the runnable examples shown on the package documentation.
// They live beside the code, not in tests/, because pkg.go.dev only shows
// examples from the package's own directory. They use only the exported API.

import (
	"fmt"
	"time"

	"github.com/vectorihub/netctl/types"
)

// ExampleErrf shows Errf in use.
func ExampleErrf() {
	err := fmt.Errorf("allocating: %w", types.Errf(types.ErrOverlap, "site aws", "overlaps site builder"))
	fmt.Println(types.HasCode(err, types.ErrOverlap), types.HasCode(err, types.ErrNotFound))
	fmt.Println(err)
	// Output:
	// true false
	// allocating: OVERLAP: site aws: overlaps site builder
}

// ExampleList shows List in use.
func ExampleList() {
	var l types.List
	l.Add(types.Errf(types.ErrNotFound, "node x", "missing"))
	l.Add(types.Errf(types.ErrNoRoute, "a to b", "unreachable"))
	fmt.Println(l.Err())
	fmt.Println(types.HasCode(l.Err(), types.ErrNoRoute))
	// Output:
	// NOT_FOUND: node x: missing
	// NO_ROUTE: a to b: unreachable
	// true
}

// ExampleValidID shows ValidID in use.
func ExampleValidID() {
	fmt.Println(types.ValidID("dgx-spark"), types.ValidID("Builder 1"), types.ValidID("-x"))
	// Output: true false false
}

// ExampleLinkIDOf shows LinkIDOf in use.
func ExampleLinkIDOf() {
	fmt.Println(types.LinkIDOf("dgx-spark", "builder-1"))
	fmt.Println(types.LinkIDOf("builder-1", "dgx-spark"))
	// Output:
	// builder-1~dgx-spark
	// builder-1~dgx-spark
}

// ExampleParseRole shows ParseRole in use.
func ExampleParseRole() {
	r, ok := types.ParseRole("hub")
	fmt.Println(r, ok)
	r, ok = types.ParseRole("relay")
	fmt.Println(r, ok)
	// Output:
	// hub true
	// unknown false
}

// ExampleParseLinkKind shows ParseLinkKind in use.
func ExampleParseLinkKind() {
	k, ok := types.ParseLinkKind("direct")
	fmt.Println(k, ok)
	// Output: direct true
}

// ExampleFixedClock shows FixedClock in use.
func ExampleFixedClock() {
	c := &types.FixedClock{T: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	c.Advance(time.Hour)
	fmt.Println(c.Now().Format(time.RFC3339))
	// Output: 2026-11-19T13:00:00Z
}
