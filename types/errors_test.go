package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

var allCodes = map[Code]string{
	ErrInvalidPrefix:     "INVALID_PREFIX",
	ErrOverlap:           "OVERLAP",
	ErrPoolExhausted:     "POOL_EXHAUSTED",
	ErrNotFound:          "NOT_FOUND",
	ErrReserved:          "RESERVED",
	ErrOutOfSupernet:     "OUT_OF_SUPERNET",
	ErrInvalidTopology:   "INVALID_TOPOLOGY",
	ErrNoRoute:           "NO_ROUTE",
	ErrUnsupportedSchema: "UNSUPPORTED_SCHEMA",
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
	e := Errf(ErrOverlap, "site aws", "overlaps %s", "site builder")
	target := &Error{Code: e.Code}

	wrapped := fmt.Errorf("wrap: %w", fmt.Errorf("again: %w", e))
	if !errors.Is(wrapped, target) {
		t.Error("code not found through two %w levels")
	}
	if errors.Is(wrapped, &Error{Code: ErrNotFound}) {
		t.Error("a different code must not match")
	}

	var l List
	l.Add(Errf(ErrNotFound, "node x", "missing"))
	l.Add(e)
	if !errors.Is(l.Err(), target) {
		t.Error("code not found inside a List")
	}
	if !errors.Is(fmt.Errorf("ctx: %w", l.Err()), target) {
		t.Error("code not found in a wrapped List")
	}
	if !HasCode(fmt.Errorf("ctx: %w", l.Err()), ErrNotFound) || HasCode(l.Err(), ErrNoRoute) {
		t.Error("HasCode wrong")
	}
}

func TestIsIgnoresSubjectAndMessage(t *testing.T) {
	a := Errf(ErrReserved, "one", "first")
	b := Errf(ErrReserved, "two", "second")
	if !errors.Is(a, b) {
		t.Error("same code, different text must match")
	}
}

func TestIsEdgeCases(t *testing.T) {
	var nilErr *Error
	if nilErr.Is(&Error{Code: ErrOverlap}) {
		t.Error("nil receiver must not match")
	}
	e := Errf(ErrOverlap, "s", "m")
	if e.Is(errors.New("plain")) {
		t.Error("non-*Error target must not match")
	}
	if e.Is((*Error)(nil)) {
		t.Error("nil *Error target must not match")
	}
	if HasCode(nil, ErrOverlap) {
		t.Error("nil error has no code")
	}
}

func TestErrorString(t *testing.T) {
	e := Errf(ErrPoolExhausted, "pool sites", "no free /%d", 20)
	if got, want := e.Error(), "POOL_EXHAUSTED: pool sites: no free /20"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestListEmptyIsNilError(t *testing.T) {
	var l List
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

func TestListReportsEveryProblem(t *testing.T) {
	var l List
	l.Add(Errf(ErrInvalidTopology, "a", "one"))
	l.Add(Errf(ErrInvalidTopology, "b", "two"))
	l.Add(Errf(ErrNoRoute, "c", "three"))
	err := l.Err()
	if err == nil {
		t.Fatal("want an error")
	}
	want := "INVALID_TOPOLOGY: a: one\nINVALID_TOPOLOGY: b: two\nNO_ROUTE: c: three"
	if err.Error() != want {
		t.Errorf("got %q want %q", err.Error(), want)
	}
	var le List
	if !errors.As(err, &le) || len(le) != 3 {
		t.Errorf("errors.As to List failed: %v", le)
	}
	if n := len(l.Unwrap()); n != 3 {
		t.Errorf("Unwrap len = %d want 3", n)
	}
}

func TestErrorJSON(t *testing.T) {
	b, err := json.Marshal(Errf(ErrNotFound, "node x", "missing"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"NOT_FOUND","subject":"node x","message":"missing"}`
	if string(b) != want {
		t.Errorf("got %s want %s", b, want)
	}
}

func ExampleErrf() {
	err := fmt.Errorf("allocating: %w", Errf(ErrOverlap, "site aws", "overlaps site builder"))
	fmt.Println(HasCode(err, ErrOverlap), HasCode(err, ErrNotFound))
	fmt.Println(err)
	// Output:
	// true false
	// allocating: OVERLAP: site aws: overlaps site builder
}

func ExampleList() {
	var l List
	l.Add(Errf(ErrNotFound, "node x", "missing"))
	l.Add(Errf(ErrNoRoute, "a to b", "unreachable"))
	fmt.Println(l.Err())
	fmt.Println(HasCode(l.Err(), ErrNoRoute))
	// Output:
	// NOT_FOUND: node x: missing
	// NO_ROUTE: a to b: unreachable
	// true
}
