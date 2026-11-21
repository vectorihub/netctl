package tests

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// TestSourceErrorsCarryTheInvalidValueCode checks every way a Source can be
// refused returns an error with the INVALID_VALUE code, found with errors.Is and
// not by reading the message: writing the zero source, reading an unknown name,
// reading a different capitalisation, and the same two through encoding/json,
// which wraps what the text methods return.
func TestSourceErrorsCarryTheInvalidValueCode(t *testing.T) {
	want := &types.Error{Code: types.ErrInvalidValue}
	if _, err := route.SourceUnknown.MarshalText(); !errors.Is(err, want) {
		t.Errorf("MarshalText of the zero source: %v, want INVALID_VALUE", err)
	}
	for _, text := range []string{"learned", "Computed", ""} {
		source := route.SourceBackup
		if err := source.UnmarshalText([]byte(text)); !errors.Is(err, want) || source != route.SourceBackup {
			t.Errorf("UnmarshalText(%q): %v (receiver %s), want INVALID_VALUE and no change", text, err, source)
		}
	}
	if _, err := json.Marshal(route.Route{}); !types.HasCode(err, types.ErrInvalidValue) {
		t.Errorf("json.Marshal of a route with no source: %v, want INVALID_VALUE through the wrapper", err)
	}
	var r route.Route
	if err := json.Unmarshal([]byte(`{"source":"learned"}`), &r); !types.HasCode(err, types.ErrInvalidValue) {
		t.Errorf("json.Unmarshal of an unknown source: %v, want INVALID_VALUE through the wrapper", err)
	}
}

// TestCompareTreatsSpellingsOfOneBlockAsOneBlock checks Compare orders routes by
// the canonical block, never by a prefix with host bits set: a route written
// 10.42.16.5/20 sorts with 10.42.16.0/20, not after 10.42.16.0/21 which the raw
// address 10.42.16.5 would put it behind; and the order is total and the same in
// every input order, with the spelling breaking only the last tie.
func TestCompareTreatsSpellingsOfOneBlockAsOneBlock(t *testing.T) {
	odd := computed("10.42.16.5/20", "n", "x~n", 5)
	plain := computed("10.42.16.0/20", "n", "x~n", 5)
	other := computed("10.42.16.2/21", "n", "x~n", 5)
	if got := route.Compare(odd, other); got != -1 {
		t.Errorf("Compare(10.42.16.5/20, 10.42.16.2/21) = %d, want -1: the /20 block 10.42.16.0 comes first", got)
	}
	if route.Compare(odd, plain) == 0 || route.Compare(odd, plain) != -route.Compare(plain, odd) {
		t.Error("two spellings of one block must differ, antisymmetrically, only in the last tie-break")
	}
	if route.Compare(plain, odd) != -1 {
		t.Error("the canonical spelling should sort first")
	}
	routes := []route.Route{odd, plain, other}
	want := []route.Route{plain, odd, other}
	inEveryOrder(t, routes, func(table *route.Table) {
		table.Sort()
		if !slices.EqualFunc(table.Routes, want, func(a, b route.Route) bool { return route.Compare(a, b) == 0 }) {
			t.Errorf("sorted to %v, want %v", table.Routes, want)
		}
	})
}
