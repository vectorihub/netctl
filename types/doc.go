// Package types holds netctl's shared vocabulary: the names, enums, clock and
// error model every other package builds on. It imports nothing else in this
// module, so it sits at the bottom of the dependency order.
//
// # Names
//
// SiteID, NodeID and LinkID are human-readable and stable: 1 to 63 characters
// of lowercase letters, digits and '-', not starting or ending with '-'. Check
// untrusted input with ValidID. LinkIDOf builds a link's ID from its two ends
// and gives the same answer whichever end is named first.
//
// # Roles and link kinds
//
// Role is hub or spoke, and LinkKind is hub-spoke or direct (D10). The zero
// value of each is "unknown", so a field that was never set cannot pass for a
// real role or kind. Use ParseRole and ParseLinkKind to read them from text.
//
// # Time
//
// Clock is how time enters netctl. No library package reads the real clock;
// callers pass one in. FixedClock stands still until Advance moves it, which
// keeps tests and repeat runs reproducible.
//
// # Errors
//
// Every error a caller acts on is an *Error with a stable Code, a Subject and
// a message. Match by code, never by text:
//
//	errors.Is(err, &types.Error{Code: types.ErrOverlap})
//	types.HasCode(err, types.ErrOverlap)
//
// Code matching works through any number of %w wraps. Functions that check many
// things collect problems in a List and return list.Err(), so every mistake is
// reported in one run.
package types
