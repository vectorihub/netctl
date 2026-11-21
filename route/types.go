package route

import (
	"cmp"
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// Source says where a route came from.
//
// The zero value is SourceUnknown, so a route whose source was never set
// cannot pass for a real one.
type Source int

const (
	// SourceUnknown is the zero value: no source was set or parsed.
	SourceUnknown Source = iota
	// SourceConnected is a prefix that belongs to this node itself.
	SourceConnected
	// SourceComputed is a route found by the shortest-path calculation.
	SourceComputed
	// SourceBackup is a pre-computed alternative. It is inactive while its
	// primary route is healthy.
	SourceBackup
	// SourceStatic is an operator override. It is always flagged so that it is
	// never preferred silently.
	SourceStatic
)

// String returns "connected", "computed", "backup", "static" or "unknown".
//
// Example: SourceBackup.String() returns "backup".
func (source Source) String() string {
	switch source {
	case SourceConnected:
		return "connected"
	case SourceComputed:
		return "computed"
	case SourceBackup:
		return "backup"
	case SourceStatic:
		return "static"
	}
	return "unknown"
}

// ParseSource converts "connected", "computed", "backup" or "static" into a
// Source. Anything else, including different capitalisation, returns
// SourceUnknown and false.
//
// Example: ParseSource("backup") returns SourceBackup, true;
// ParseSource("learned") returns SourceUnknown, false.
func ParseSource(text string) (Source, bool) {
	switch text {
	case "connected":
		return SourceConnected, true
	case "computed":
		return SourceComputed, true
	case "backup":
		return SourceBackup, true
	case "static":
		return SourceStatic, true
	}
	return SourceUnknown, false
}

// MarshalText writes the source as its name, so a Route written as JSON shows
// "computed" and not a number. An unknown source returns an INVALID_VALUE
// error, so a half-filled Route cannot be written out; through encoding/json the
// error is wrapped, and errors.Is still finds the code.
//
// Example: SourceComputed.MarshalText() returns "computed"; the zero Source
// returns an INVALID_VALUE error.
func (source Source) MarshalText() ([]byte, error) {
	if source.String() == "unknown" {
		return nil, types.Errf(types.ErrInvalidValue, "route source", "cannot write unknown source %d", int(source))
	}
	return []byte(source.String()), nil
}

// UnmarshalText reads a source name written by MarshalText. An unknown name,
// including a different capitalisation, returns an INVALID_VALUE error and
// leaves the receiver unchanged.
//
// Example: UnmarshalText("static") sets the receiver to SourceStatic;
// UnmarshalText("learned") returns an INVALID_VALUE error.
func (source *Source) UnmarshalText(text []byte) error {
	parsed, ok := ParseSource(string(text))
	if !ok {
		return types.Errf(types.ErrInvalidValue, "route source", "unknown source %q: want connected, computed, backup or static", text)
	}
	*source = parsed
	return nil
}

// Route is one way a node can reach a block of addresses.
//
// A Route is plain data. Copying it copies the Path slice header, not its
// contents, so treat Path as read-only once a route is in a table.
type Route struct {
	// Prefix is the block of destination addresses this route covers.
	Prefix netip.Prefix `json:"prefix"`
	// NextHop is the neighbouring node the packet is handed to. It is empty for
	// a connected route, because the destination is on this node.
	NextHop types.NodeID `json:"next_hop,omitempty"`
	// Via is the link the packet leaves on. It is empty for a connected route.
	Via types.LinkID `json:"via,omitempty"`
	// Metric is the total cost of the route. Lower is better.
	Metric int `json:"metric"`
	// Source is where the route came from.
	Source Source `json:"source"`
	// Path is the full list of nodes the packet crosses, starting at this node
	// and ending at the node that owns the prefix. It is used to explain a
	// route and to check that no route loops.
	Path []types.NodeID `json:"path,omitempty"`
	// Active says the route is in use. Backup routes are inactive until the
	// route they back up fails.
	Active bool `json:"active"`
}

// Compare orders two routes. It returns -1, 0 or +1 and is a total order, so
// sorting with it always gives the same sequence for the same set of routes.
//
// Routes are ordered by prefix (see cidr.Compare), then metric, then next hop,
// then link, then source, then inactive before active, then path, comparing
// node IDs one at a time with a shorter path first when one is a prefix of
// the other. Prefixes are compared in their canonical form, so 10.42.16.5/20
// and 10.42.16.0/20 are the same block here; a route written with host bits set
// sorts by the block it stands for, and only as the very last tie-break does the
// spelling count, so the order stays total. It returns 0 only for two routes that
// are identical.
//
// Example: of two routes for 10.42.32.0/20, the one with metric 10 sorts before
// the one with metric 40.
func Compare(first, second Route) int {
	firstBlock, _ := cidr.Canonical(first.Prefix)
	secondBlock, _ := cidr.Canonical(second.Prefix)
	return cmp.Or(
		cidr.Compare(firstBlock, secondBlock),
		cmp.Compare(first.Metric, second.Metric),
		cmp.Compare(first.NextHop, second.NextHop),
		cmp.Compare(first.Via, second.Via),
		cmp.Compare(first.Source, second.Source),
		compareBool(first.Active, second.Active),
		slices.Compare(first.Path, second.Path),
		first.Prefix.Addr().Compare(second.Prefix.Addr()),
	)
}

// compareBool orders false before true.
func compareBool(first, second bool) int {
	switch {
	case first == second:
		return 0
	case !first:
		return -1
	}
	return 1
}

// Table is one node's routes.
//
// Routes holds every route the node knows, including backups, which is what a
// router calls the RIB. Call Sort after building or changing it so the order
// is the one Compare defines. FIB returns the routes in use.
type Table struct {
	// Node is the node this table belongs to.
	Node types.NodeID
	// Routes is every route, active or not.
	Routes []Route
}

// Sort puts the routes in the order Compare defines: by prefix, then metric,
// then next hop, and so on. It sorts in place and does nothing on a nil table.
//
// Example: after Sort, a table holding 10.42.32.0/20 and 10.42.0.0/20 lists
// 10.42.0.0/20 first.
func (table *Table) Sort() {
	if table == nil {
		return
	}
	slices.SortFunc(table.Routes, Compare)
}

// FIB returns the active routes, in the table's order, as a new slice. A nil
// table, or one with no active route, returns nil. Backup routes are not
// included until they become active.
//
// Example: a table with an active computed route and an inactive backup for the
// same prefix returns only the computed route.
func (table *Table) FIB() []Route {
	if table == nil {
		return nil
	}
	var active []Route
	for _, route := range table.Routes {
		if route.Active {
			active = append(active, route)
		}
	}
	return active
}

// Tables holds one Table for every node, keyed by node ID.
type Tables map[types.NodeID]*Table

// Nodes returns the node IDs that have a table, sorted. Use it to range over
// the tables in a fixed order, because ranging over a map directly is not
// deterministic.
//
// Example: tables for "dgx-spark" and "aws-hub" give [aws-hub dgx-spark].
func (tables Tables) Nodes() []types.NodeID {
	nodes := make([]types.NodeID, 0, len(tables))
	for node := range tables {
		nodes = append(nodes, node)
	}
	slices.Sort(nodes)
	return nodes
}
