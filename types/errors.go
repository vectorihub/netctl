package types

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a stable error code. Callers and tests compare codes, never message
// text, so a message can be reworded without breaking anyone. A code is never
// renamed or reused once released.
type Code string

// The error codes netctl returns.
const (
	// ErrInvalidPrefix means an address block is malformed or has the wrong size.
	ErrInvalidPrefix Code = "INVALID_PREFIX"
	// ErrOverlap means two address blocks collide.
	ErrOverlap Code = "OVERLAP"
	// ErrPoolExhausted means no free address block is left to hand out.
	ErrPoolExhausted Code = "POOL_EXHAUSTED"
	// ErrNotFound means the thing asked for does not exist.
	ErrNotFound Code = "NOT_FOUND"
	// ErrReserved means the thing asked for is set aside and cannot be used.
	ErrReserved Code = "RESERVED"
	// ErrOutOfSupernet means an address lies outside the allowed overall range.
	ErrOutOfSupernet Code = "OUT_OF_SUPERNET"
	// ErrInvalidTopology means the network description is broken.
	ErrInvalidTopology Code = "INVALID_TOPOLOGY"
	// ErrNoRoute means there is no path between two nodes.
	ErrNoRoute Code = "NO_ROUTE"
	// ErrUnsupportedSchema means an input uses a format version netctl does not know.
	ErrUnsupportedSchema Code = "UNSUPPORTED_SCHEMA"
)

// Error is a coded error about one subject (a prefix, node, link or file).
//
// Match it by code with errors.Is:
//
//	errors.Is(err, &types.Error{Code: types.ErrOverlap})
type Error struct {
	// Code is the stable code callers act on.
	Code Code `json:"code"`
	// Subject is what the error is about, for example "site aws".
	Subject string `json:"subject"`
	// Msg is the human-readable explanation. It may change between releases.
	Msg string `json:"message"`
}

// Error returns "CODE: subject: message".
func (err *Error) Error() string { return string(err.Code) + ": " + err.Subject + ": " + err.Msg }

// Is makes errors.Is match on the code alone: the subject and message of the
// target are ignored. A nil receiver or a target that is not an *Error never
// matches.
//
// Example: errors.Is(err, &Error{Code: ErrOverlap}) is true for any error
// wrapping an overlap, however many %w layers deep.
func (err *Error) Is(target error) bool {
	wanted, ok := target.(*Error)
	return ok && err != nil && wanted != nil && wanted.Code == err.Code
}

// Errf builds an *Error with a formatted message.
//
// Example: Errf(ErrNotFound, "site aws", "no such site %q", "aws").
func Errf(code Code, subject, format string, args ...any) *Error {
	return &Error{Code: code, Subject: subject, Msg: fmt.Sprintf(format, args...)}
}

// HasCode reports whether err, or any error it wraps or lists, carries code.
// It is a shorthand for errors.Is with a code-only target.
//
// Example: HasCode(list.Err(), ErrOverlap).
func HasCode(err error, code Code) bool {
	return errors.Is(err, &Error{Code: code})
}

// List collects several errors so that every problem is reported at once
// instead of one per run. Functions that check many things add to a List and
// return list.Err().
type List []*Error

// Add appends problem to the list. A nil problem is ignored.
func (list *List) Add(problem *Error) {
	if problem != nil {
		*list = append(*list, problem)
	}
}

// Err returns nil for an empty list, so callers can write `return list.Err()`
// without handing back a non-nil error that holds nothing.
func (list List) Err() error {
	if len(list) == 0 {
		return nil
	}
	return list
}

// Error joins the messages of every error in the list, one per line.
func (list List) Error() string {
	parts := make([]string, len(list))
	for i, problem := range list {
		parts[i] = problem.Error()
	}
	return strings.Join(parts, "\n")
}

// Unwrap lets errors.Is find any code inside the list.
func (list List) Unwrap() []error {
	problems := make([]error, len(list))
	for i, problem := range list {
		problems[i] = problem
	}
	return problems
}
