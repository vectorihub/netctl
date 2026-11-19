package types

import (
	"regexp"
	"time"
)

// IDs are human-readable, stable and validated: lowercase letters, digits and
// '-', 1 to 63 characters, starting and ending with a letter or digit.
type (
	// SiteID names a site.
	//
	// Example: "aws", "builder", "dgx".
	SiteID string

	// NodeID names a machine.
	//
	// Example: "aws-hub", "builder-1", "dgx-spark".
	NodeID string

	// LinkID names the link between two nodes. It is derived from both ends
	// by LinkIDOf and is the same whichever end is named first.
	//
	// Example: "builder-1~dgx-spark".
	LinkID string
)

var idRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidID reports whether id is a valid site or node ID: 1 to 63 characters of
// lowercase letters, digits and '-', not starting or ending with '-'.
//
// Example: ValidID("dgx-spark") is true; ValidID("Builder 1"), ValidID(""),
// ValidID("-x") and ValidID("x-") are false.
func ValidID(id string) bool { return idRe.MatchString(id) }

// LinkIDOf returns the order-independent ID of the link between nodeA and nodeB: the
// two IDs sorted, joined by '~'. Because '~' is not a valid ID character, two
// different pairs never produce the same link ID.
//
// It does not validate its arguments. Use ValidID first when the IDs come from
// outside. A link from a node to itself is not rejected here; the topology
// validator rejects it.
//
// Example: LinkIDOf("dgx-spark", "builder-1") and LinkIDOf("builder-1",
// "dgx-spark") both return "builder-1~dgx-spark".
func LinkIDOf(nodeA, nodeB NodeID) LinkID {
	if nodeB < nodeA {
		nodeA, nodeB = nodeB, nodeA
	}
	return LinkID(string(nodeA) + "~" + string(nodeB))
}

// Role is what a node does in the topology.
//
// v1alpha1 has two roles. A relay role is reserved for Phase 5 and is
// deliberately absent (D10).
type Role int

const (
	// RoleUnknown is the zero value: no role was set or parsed.
	RoleUnknown Role = iota
	// RoleHub forwards traffic between other nodes.
	RoleHub
	// RoleSpoke sends and receives only its own traffic.
	RoleSpoke
)

// String returns "hub", "spoke" or "unknown".
func (role Role) String() string {
	switch role {
	case RoleHub:
		return "hub"
	case RoleSpoke:
		return "spoke"
	}
	return "unknown"
}

// ParseRole converts "hub" or "spoke" into a Role. Anything else, including
// different capitalisation, returns RoleUnknown and false.
//
// Example: ParseRole("hub") returns RoleHub, true; ParseRole("relay") returns
// RoleUnknown, false.
func ParseRole(text string) (Role, bool) {
	switch text {
	case "hub":
		return RoleHub, true
	case "spoke":
		return RoleSpoke, true
	}
	return RoleUnknown, false
}

// LinkKind is the kind of a link between two nodes.
//
// v1alpha1 has two kinds. A transit kind is reserved for Phase 5 and is
// deliberately absent (D10).
type LinkKind int

const (
	// LinkUnknown is the zero value: no kind was set or parsed.
	LinkUnknown LinkKind = iota
	// LinkHubSpoke is a spoke-to-hub tunnel.
	LinkHubSpoke
	// LinkDirect is a spoke-to-spoke direct peering.
	LinkDirect
)

// String returns "hub-spoke", "direct" or "unknown".
func (kind LinkKind) String() string {
	switch kind {
	case LinkHubSpoke:
		return "hub-spoke"
	case LinkDirect:
		return "direct"
	}
	return "unknown"
}

// ParseLinkKind converts "hub-spoke" or "direct" into a LinkKind. Anything
// else returns LinkUnknown and false.
//
// Example: ParseLinkKind("direct") returns LinkDirect, true.
func ParseLinkKind(text string) (LinkKind, bool) {
	switch text {
	case "hub-spoke":
		return LinkHubSpoke, true
	case "direct":
		return LinkDirect, true
	}
	return LinkUnknown, false
}

// Clock is injected so that netctl never reads wall time itself. Callers
// supply the real time or a fixed one.
type Clock interface{ Now() time.Time }

// FixedClock is a Clock that stands still until told to move. It is used by
// tests and by callers that need a reproducible run (for example a --now flag).
//
// Use a pointer: Now and Advance have pointer receivers. The zero value reports
// the zero time.
//
// Example: clock := &FixedClock{T: t0}; clock.Advance(time.Hour); clock.Now()
// is t0 plus one hour.
type FixedClock struct{ T time.Time }

// Now returns the clock's current fixed time.
func (clock *FixedClock) Now() time.Time { return clock.T }

// Advance moves the clock forward by amount. A negative amount moves it back.
func (clock *FixedClock) Advance(amount time.Duration) { clock.T = clock.T.Add(amount) }
