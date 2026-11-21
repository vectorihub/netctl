package topology

import (
	"net/netip"

	"github.com/vectorihub/netctl/types"
)

// Schema is the format version written at the top of every topology file. A
// loader that sees another value refuses the file (rule T1).
const Schema = "beaver.topology/v1alpha1"

// Topology is the one description of what the network is made of: its sites,
// machines, links and the traffic that must be allowed or denied between them.
// After Normalize, sites, nodes and links are sorted by ID and intents are in the
// order they were written.
//
// It is plain data. Build one in Go code, or read one from YAML with Decode; the
// rest of netctl cannot tell the difference. This file imports no YAML
// package, which keeps that true.
type Topology struct {
	// Schema must be Schema.
	Schema string
	// Name labels the topology, for example "three-site". It may be empty.
	Name string
	// Addressing says where the address plan comes from.
	Addressing Addressing
	// Sites are the places machines live.
	Sites []Site
	// Nodes are the machines.
	Nodes []Node
	// Links join two nodes.
	Links []Link
	// Intents say who must or must not reach whom. Their order matters for
	// policy, so it is never changed.
	Intents []Intent
	// Validation holds what the file says about the validator: today, the
	// warnings it accepts. It describes how to judge the network, not the
	// network, so it is not part of Hash.
	//
	// Example:
	//
	//	Validation{Suppress: []Suppression{{Rule: "V-TOPO-004", Subject: "builder-1~dgx-spark", Reason: "relay through the hub is acceptable"}}}.
	Validation Validation
}

// Validation is what a topology file says about the validator.
//
// Example:
//
//	Validation{Suppress: []Suppression{{Rule: "V-TOPO-003", Subject: "dgx-spark", Reason: "single uplink until the second ISP is installed (BC-41)"}}}.
type Validation struct {
	// Suppress lists warnings the file accepts, each with a reason.
	//
	// Example:
	//
	//	one entry for each accepted warning.
	Suppress []Suppression
}

// Suppression accepts one validator warning, with the reason. An error can
// never be suppressed, and a suppression with no reason is refused by the
// validator.
//
// Example:
//
//	Suppression{Rule: "V-TOPO-003", Subject: "dgx-spark", Reason: "single uplink accepted (BC-41)"}.
type Suppression struct {
	// Rule is the rule's ID, for example "V-TOPO-003".
	//
	// Example:
	//
	//	"V-TOPO-003".
	Rule string
	// Subject is the node, link or prefix the warning is about. Empty means
	// every subject of the rule.
	//
	// Example:
	//
	//	"dgx-spark".
	Subject string
	// Reason says why the warning is accepted. It is mandatory.
	//
	// Example:
	//
	//	"single uplink accepted until the second ISP is installed (BC-41)".
	Reason string
}

// Addressing is the address plan: either a reference to a separate plan file
// or the plan itself. netctl never reads files, so a reference is only
// recorded here; the caller resolves it.
type Addressing struct {
	// Ref names a separate address-plan file, from "$ref". When set, Supernet
	// and Pools are empty.
	Ref string
	// Supernet is the overall range, for an inline plan.
	Supernet netip.Prefix
	// Pools carve the supernet up, for an inline plan.
	Pools []AddressPool
}

// AddressPool is one pool of an inline address plan. It mirrors ipam.Pool,
// which topology cannot import (topology sits beside ipam, not above it).
type AddressPool struct {
	// Name identifies the pool, for example "sites".
	Name string
	// Prefix is the block the pool covers.
	Prefix netip.Prefix
	// ChildBits is the prefix length of each child block. Zero means none.
	ChildBits int
	// Reserved means allocating from the pool needs an override.
	Reserved bool
	// SkipFirst is how many leading children are never handed out.
	SkipFirst int
}

// Site is a place where machines live.
type Site struct {
	// ID names the site, for example "aws".
	ID types.SiteID
	// Provider is who runs it ("aws", "owned", "gcp", ...). Informational.
	Provider string
	// Region is the provider's region. Informational.
	Region string
}

// NATMode says whether a node can be reached from outside.
//
// The zero value is NATUnknown, so a node that never said is not mistaken for
// one that is publicly reachable.
type NATMode int

const (
	// NATUnknown is the zero value: nothing was said, so assume nothing.
	NATUnknown NATMode = iota
	// NATNone means the node is publicly reachable on its endpoints.
	NATNone
	// NATBehind means the node can only start connections outward.
	NATBehind
)

// String returns "unknown", "none" or "behind".
func (mode NATMode) String() string {
	switch mode {
	case NATNone:
		return "none"
	case NATBehind:
		return "behind"
	}
	return "unknown"
}

// ParseNATMode converts "none", "behind" or "unknown" into a NATMode. Anything
// else, including a different capitalisation, returns NATUnknown and false.
//
// Example: ParseNATMode("behind") returns NATBehind, true.
func ParseNATMode(text string) (NATMode, bool) {
	switch text {
	case "none":
		return NATNone, true
	case "behind":
		return NATBehind, true
	case "unknown":
		return NATUnknown, true
	}
	return NATUnknown, false
}

// Advertisement is something a node announces to the overlay: its whole site
// block, or one extra prefix. Exactly one of the two is set.
type Advertisement struct {
	// Site means "announce my site's block".
	Site bool
	// Prefix is an extra IPv4 block to announce, in canonical form.
	Prefix netip.Prefix
}

// String returns "site" or the prefix.
func (advert Advertisement) String() string {
	if advert.Site {
		return "site"
	}
	return advert.Prefix.String()
}

// Node is a machine.
type Node struct {
	// ID names the machine, for example "builder-1".
	ID types.NodeID
	// Site is where it lives.
	Site types.SiteID
	// Role is hub or spoke. The zero value means none was set.
	Role types.Role
	// Endpoints are the public addresses other nodes can reach it on.
	Endpoints []netip.AddrPort
	// NAT says whether it can be reached from outside.
	NAT NATMode
	// Advertises lists what it announces to the overlay.
	Advertises []Advertisement
	// Labels are free-form tags that selectors can match.
	Labels map[string]string
	// Peers lists the nodes this one says it is linked to. It is an input form,
	// for sources that list adjacencies per node instead of per link. ImportPeers
	// turns it into Links and clears it; Decode calls ImportPeers for you. A
	// Topology that still has Peers set fails Check.
	Peers []types.NodeID
}

// HealthMode says how a link's health is known.
//
// The zero value is HealthUnset, so loading can tell "not said" from a choice
// and fill in the default for the link's kind.
type HealthMode int

const (
	// HealthUnset is the zero value: nothing was said.
	HealthUnset HealthMode = iota
	// HealthRequired means the link needs an independent health signal.
	HealthRequired
	// HealthAssumed means the link is taken to be up unless told otherwise.
	HealthAssumed
)

// String returns "unset", "required" or "assumed".
func (mode HealthMode) String() string {
	switch mode {
	case HealthRequired:
		return "required"
	case HealthAssumed:
		return "assumed"
	}
	return "unset"
}

// ParseHealthMode converts "required" or "assumed" into a HealthMode. Anything
// else returns HealthUnset and false.
//
// Example: ParseHealthMode("required") returns HealthRequired, true.
func ParseHealthMode(text string) (HealthMode, bool) {
	switch text {
	case "required":
		return HealthRequired, true
	case "assumed":
		return HealthAssumed, true
	}
	return HealthUnset, false
}

// Link joins two nodes. It is one object with two ends, so it cannot be
// declared on one side only.
type Link struct {
	// ID is derived from the two ends (see types.LinkIDOf) and does not depend
	// on which end is named first. Normalize sets it; it is never chosen.
	ID types.LinkID
	// Kind is hub-spoke or direct. The zero value means none was set.
	Kind types.LinkKind
	// A and B are the two ends. Normalize stores them with A before B.
	A, B types.NodeID
	// Cost makes a path cheaper or dearer. Zero means "use the default", which
	// Normalize fills in (DefaultHubSpokeCost or DefaultDirectCost).
	Cost int
	// Health says how the link's health is known.
	Health HealthMode
}

// Action is what an intent does.
type Action int

const (
	// ActionUnknown is the zero value: no action was set.
	ActionUnknown Action = iota
	// ActionAllow lets the traffic through.
	ActionAllow
	// ActionDeny blocks the traffic.
	ActionDeny
)

// String returns "allow", "deny" or "unknown".
func (action Action) String() string {
	switch action {
	case ActionAllow:
		return "allow"
	case ActionDeny:
		return "deny"
	}
	return "unknown"
}

// Intent says that traffic From one set of nodes To another is allowed or
// denied. netctl records intents; enforcing them is up to the caller.
type Intent struct {
	// Action is allow or deny.
	Action Action
	// From and To choose the nodes at each end.
	From, To Selector
	// Ports limits the intent to these destination ports. Empty means all.
	Ports []uint16
}
