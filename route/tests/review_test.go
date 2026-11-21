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

// TestSortMasksPrefixesAtTheBoundary checks Table.Sort is where a caller-built
// route enters the package: a route written 10.42.16.5/20 is stored as its
// block 10.42.16.0/20, so it sorts with that block and before 10.42.16.0/21
// (written 10.42.16.2/21), never after it as the raw address would put it, and
// no route left in the table has host bits set, in every input order.
func TestSortMasksPrefixesAtTheBoundary(t *testing.T) {
	odd := computed("10.42.16.5/20", "n", "x~n", 5)
	plain := computed("10.42.16.0/20", "m", "x~m", 5)
	other := computed("10.42.16.2/21", "n", "x~n", 5)
	want := []route.Route{
		computed("10.42.16.0/20", "m", "x~m", 5),
		computed("10.42.16.0/20", "n", "x~n", 5),
		computed("10.42.16.0/21", "n", "x~n", 5),
	}
	inEveryOrder(t, []route.Route{odd, plain, other}, func(table *route.Table) {
		table.Sort()
		if !slices.EqualFunc(table.Routes, want, identical) {
			t.Errorf("sorted to %v, want %v", table.Routes, want)
		}
		for _, r := range table.Routes {
			if r.Prefix != r.Prefix.Masked() {
				t.Errorf("Sort left %s with host bits set", r.Prefix)
			}
		}
	})
}

// TestDiffAndExplainMaskPrefixes checks Diff and Explain mask a caller's routes
// before comparing them: a table written with 10.42.16.5/20 and the same table
// written with 10.42.16.0/20 give no diff, and Explain pairs a backup written
// with host bits with the active route for its block.
func TestDiffAndExplainMaskPrefixes(t *testing.T) {
	odd := &route.Table{Node: "x", Routes: []route.Route{computed("10.42.16.5/20", "n", "x~n", 5)}}
	plain := &route.Table{Node: "x", Routes: []route.Route{computed("10.42.16.0/20", "n", "x~n", 5)}}
	if diffs := route.Diff(route.Tables{"x": odd}, route.Tables{"x": plain}); len(diffs) != 0 {
		t.Errorf("two spellings of one block gave a diff: %+v", diffs)
	}
	backup := computed("10.42.16.9/20", "m", "x~m", 9)
	backup.Source, backup.Active = route.SourceBackup, false
	table := &route.Table{Node: "x", Routes: []route.Route{computed("10.42.16.5/20", "n", "x~n", 5), backup}}
	explanation := route.Explain(table, addrOf("10.42.20.1"), route.Notes{})
	if explanation.Match == nil || explanation.Match.Prefix != prefixOf("10.42.16.0/20") {
		t.Fatalf("match = %v, want the masked block 10.42.16.0/20", explanation.Match)
	}
	if explanation.Backup == nil || explanation.Backup.NextHop != "m" || explanation.Backup.Prefix != prefixOf("10.42.16.0/20") {
		t.Errorf("backup = %v, want the masked backup via m", explanation.Backup)
	}
}
