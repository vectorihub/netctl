package tests

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/vectorihub/netctl/types"
)

var allCodes = map[types.Code]string{
	types.ErrInvalidPrefix:     "INVALID_PREFIX",
	types.ErrOverlap:           "OVERLAP",
	types.ErrPoolExhausted:     "POOL_EXHAUSTED",
	types.ErrNotFound:          "NOT_FOUND",
	types.ErrReserved:          "RESERVED",
	types.ErrOutOfSupernet:     "OUT_OF_SUPERNET",
	types.ErrInvalidTopology:   "INVALID_TOPOLOGY",
	types.ErrNoRoute:           "NO_ROUTE",
	types.ErrUnsupportedSchema: "UNSUPPORTED_SCHEMA",
}

// The code strings are a public contract; this pins them.
func TestCodesAreStable(t *testing.T) {
	if len(allCodes) != 9 {
		t.Fatalf("expected 9 distinct codes, got %d", len(allCodes))
	}
	for c, s := range allCodes {
		if string(c) != s {
			t.Errorf("code %q changed from %q", c, s)
		}
	}
}

// P1-TYPE-003: errors.Is works through two %w levels and inside a List.
func TestErrorsIsThroughWrapsAndList(t *testing.T) {
	e := types.Errf(types.ErrOverlap, "site aws", "overlaps %s", "site builder")
	target := &types.Error{Code: e.Code}

	wrapped := fmt.Errorf("wrap: %w", fmt.Errorf("again: %w", e))
	if !errors.Is(wrapped, target) {
		t.Error("code not found through two %w levels")
	}
	if errors.Is(wrapped, &types.Error{Code: types.ErrNotFound}) {
		t.Error("a different code must not match")
	}

	var l types.List
	l.Add(types.Errf(types.ErrNotFound, "node x", "missing"))
	l.Add(e)
	if !errors.Is(l.Err(), target) {
		t.Error("code not found inside a List")
	}
	if !errors.Is(fmt.Errorf("ctx: %w", l.Err()), target) {
		t.Error("code not found in a wrapped List")
	}
	if !types.HasCode(fmt.Errorf("ctx: %w", l.Err()), types.ErrNotFound) || types.HasCode(l.Err(), types.ErrNoRoute) {
		t.Error("HasCode wrong")
	}
}

// TestIsIgnoresSubjectAndMessage checks that errors.Is matches on the code alone.
func TestIsIgnoresSubjectAndMessage(t *testing.T) {
	a := types.Errf(types.ErrReserved, "one", "first")
	b := types.Errf(types.ErrReserved, "two", "second")
	if !errors.Is(a, b) {
		t.Error("same code, different text must match")
	}
}

// TestIsEdgeCases checks errors.Is with nil errors, nil targets and targets that are not *Error.
func TestIsEdgeCases(t *testing.T) {
	var nilErr *types.Error
	if nilErr.Is(&types.Error{Code: types.ErrOverlap}) {
		t.Error("nil receiver must not match")
	}
	e := types.Errf(types.ErrOverlap, "s", "m")
	if e.Is(errors.New("plain")) {
		t.Error("non-*Error target must not match")
	}
	if e.Is((*types.Error)(nil)) {
		t.Error("nil *Error target must not match")
	}
	if types.HasCode(nil, types.ErrOverlap) {
		t.Error("nil error has no code")
	}
}

// TestErrorString checks the "CODE: subject: message" text of an error.
func TestErrorString(t *testing.T) {
	e := types.Errf(types.ErrPoolExhausted, "pool sites", "no free /%d", 20)
	if got, want := e.Error(), "POOL_EXHAUSTED: pool sites: no free /20"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestListEmptyIsNilError checks that an empty List gives a nil error, so callers can return it
// directly.
func TestListEmptyIsNilError(t *testing.T) {
	var l types.List
	if err := l.Err(); err != nil {
		t.Fatalf("empty list Err() = %v, want nil", err)
	}
	l.Add(nil) // ignored
	if len(l) != 0 || l.Err() != nil {
		t.Fatal("adding nil must not grow the list")
	}
	if got := l.Unwrap(); len(got) != 0 {
		t.Fatalf("Unwrap of empty list = %v", got)
	}
}

// TestListReportsEveryProblem checks that a List keeps every problem, prints one per line and lets
// errors.Is find any code in it.
func TestListReportsEveryProblem(t *testing.T) {
	var l types.List
	l.Add(types.Errf(types.ErrInvalidTopology, "a", "one"))
	l.Add(types.Errf(types.ErrInvalidTopology, "b", "two"))
	l.Add(types.Errf(types.ErrNoRoute, "c", "three"))
	err := l.Err()
	if err == nil {
		t.Fatal("want an error")
	}
	want := "INVALID_TOPOLOGY: a: one\nINVALID_TOPOLOGY: b: two\nNO_ROUTE: c: three"
	if err.Error() != want {
		t.Errorf("got %q want %q", err.Error(), want)
	}
	var le types.List
	if !errors.As(err, &le) || len(le) != 3 {
		t.Errorf("errors.As to List failed: %v", le)
	}
	if n := len(l.Unwrap()); n != 3 {
		t.Errorf("Unwrap len = %d want 3", n)
	}
}

// TestErrorJSON checks the JSON field names of an error, which callers depend on.
func TestErrorJSON(t *testing.T) {
	b, err := json.Marshal(types.Errf(types.ErrNotFound, "node x", "missing"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"NOT_FOUND","subject":"node x","message":"missing"}`
	if string(b) != want {
		t.Errorf("got %s want %s", b, want)
	}
}
