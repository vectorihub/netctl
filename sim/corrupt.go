package sim

import (
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// CorruptTable returns a copy of tables in which one node's routes for one
// prefix have been spoiled on purpose. It exists so a test can plant a fault and
// check the simulator catches it: a detector that has never seen a fault has not
// shown it can.
//
// What happens depends on nextHop:
//
//   - An empty nextHop deletes every route the node has for exactly prefix,
//     active and backup, which plants a black hole. A less specific route that
//     still covers the prefix keeps working.
//   - Otherwise the node's active route for exactly prefix is pointed at nextHop,
//     over the link LinkIDOf(node, nextHop), with a path of just the two nodes.
//     If nextHop is not a neighbour of the node that link does not exist, which
//     plants a wrong neighbour; if it points back along the way the packet came,
//     it plants a loop. Backup routes are left as they were.
//   - If the node has no active route for exactly prefix, one is added, with
//     metric 1 and the source static, so a hand-planted route is never mistaken
//     for a computed one.
//
// The prefix is made canonical first, so 10.42.32.7/20 means 10.42.32.0/20, and
// is matched against the node's routes in their canonical form too. The input is
// not changed. Only the corrupted node's table is copied; the other
// tables are shared with the input, so treat both results as read-only. The
// returned table is sorted. It returns NOT_FOUND if the node has no table and
// INVALID_PREFIX if prefix is not valid.
//
// Example: CorruptTable(tables, "aws-hub", 10.42.32.0/20, "") returns tables in
// which the hub no longer has any route for dgx-spark's site block.
func CorruptTable(tables route.Tables, node types.NodeID, prefix netip.Prefix, nextHop types.NodeID) (route.Tables, error) {
	table, ok := tables[node]
	if !ok || table == nil {
		return nil, types.Errf(types.ErrNotFound, "node "+string(node), "there is no routing table for it to corrupt")
	}
	if !prefix.IsValid() {
		return nil, types.Errf(types.ErrInvalidPrefix, "prefix", "%v is not a valid prefix", prefix)
	}
	prefix, _ = cidr.Canonical(prefix)
	same := func(r route.Route) bool {
		stored, _ := cidr.Canonical(r.Prefix)
		return stored == prefix
	}
	routes := slices.Clone(table.Routes)
	switch {
	case nextHop == "":
		routes = slices.DeleteFunc(routes, same)
	default:
		planted := false
		for i := range routes {
			if same(routes[i]) && routes[i].Active {
				routes[i].NextHop = nextHop
				routes[i].Via = types.LinkIDOf(node, nextHop)
				routes[i].Path = []types.NodeID{node, nextHop}
				planted = true
			}
		}
		if !planted {
			routes = append(routes, route.Route{
				Prefix: prefix, NextHop: nextHop, Via: types.LinkIDOf(node, nextHop), Metric: 1,
				Source: route.SourceStatic, Path: []types.NodeID{node, nextHop}, Active: true,
			})
		}
	}
	corrupted := &route.Table{Node: table.Node, Routes: routes}
	corrupted.Sort()
	out := make(route.Tables, len(tables))
	for name, original := range tables {
		out[name] = original
	}
	out[node] = corrupted
	return out, nil
}
