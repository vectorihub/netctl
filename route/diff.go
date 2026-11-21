package route

import (
	"cmp"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// RouteChange is one route that is in both tables but is not the same in each.
//
// Example:
//
//	for _, change := range diff.Changed {
//		if change.Activated() { // a backup took over
//			fmt.Println(change.New.Prefix, "now via", change.New.NextHop)
//		}
//	}
type RouteChange struct {
	// Old is the route in the old table.
	Old Route `json:"old"`
	// New is the route in the new table.
	New Route `json:"new"`
}

// Activated reports whether the route came into use: it was inactive and now
// is active. This is what happens to a backup when its primary fails.
//
// Example:
//
//	a backup that goes from inactive to active after its primary fails is Activated.
func (change RouteChange) Activated() bool { return !change.Old.Active && change.New.Active }

// Deactivated reports whether the route went out of use: it was active and now
// is not.
//
// Example:
//
//	the primary route that a backup replaced is Deactivated.
func (change RouteChange) Deactivated() bool { return change.Old.Active && !change.New.Active }

// TableDiff is what changed in one node's table.
//
// Example:
//
//	for _, diff := range route.Diff(before, after) {
//		fmt.Println(diff.Node, len(diff.Added), len(diff.Removed), len(diff.Changed))
//	}
type TableDiff struct {
	// Node is the node whose table changed.
	Node types.NodeID `json:"node"`
	// Added are routes only in the new table.
	Added []Route `json:"added,omitempty"`
	// Removed are routes only in the old table.
	Removed []Route `json:"removed,omitempty"`
	// Changed are routes in both tables that differ in metric, source, path or
	// whether they are active.
	Changed []RouteChange `json:"changed,omitempty"`
}

// Empty reports whether nothing changed.
//
// Example:
//
//	the diff of a table with itself is Empty.
func (diff TableDiff) Empty() bool {
	return len(diff.Added) == 0 && len(diff.Removed) == 0 && len(diff.Changed) == 0
}

// routeKey says which route is which across two tables: the prefix, the next
// hop and the link, plus a count so that two routes with the same three (which
// a healthy table never has) are still told apart.
type routeKey struct {
	// prefix is the destination block.
	prefix string
	// nextHop is the neighbour.
	nextHop types.NodeID
	// via is the link.
	via types.LinkID
	// ordinal counts routes with the same prefix, next hop and link, in order.
	ordinal int
}

// keyed indexes routes by routeKey, after sorting them so that the ordinals are
// the same whatever order the table was in.
func keyed(routes []Route) map[routeKey]Route {
	out := make(map[routeKey]Route, len(routes))
	seen := map[routeKey]int{}
	for _, r := range sortedCopy(routes) {
		base := routeKey{prefix: r.Prefix.String(), nextHop: r.NextHop, via: r.Via}
		key := base
		key.ordinal = seen[base]
		seen[base]++
		out[key] = r
	}
	return out
}

// diffTables compares two tables of one node.
func diffTables(node types.NodeID, oldRoutes, newRoutes []Route) TableDiff {
	before, after := keyed(oldRoutes), keyed(newRoutes)
	diff := TableDiff{Node: node}
	for key, was := range before {
		now, still := after[key]
		switch {
		case !still:
			diff.Removed = append(diff.Removed, was)
		case Compare(was, now) != 0:
			diff.Changed = append(diff.Changed, RouteChange{Old: was, New: now})
		}
	}
	for key, now := range after {
		if _, had := before[key]; !had {
			diff.Added = append(diff.Added, now)
		}
	}
	slices.SortFunc(diff.Added, Compare)
	slices.SortFunc(diff.Removed, Compare)
	slices.SortFunc(diff.Changed, func(first, second RouteChange) int {
		return cmp.Or(Compare(first.Old, second.Old), Compare(first.New, second.New))
	})
	return diff
}

// Diff compares two sets of tables and returns, for every node whose table
// differs, the routes added, removed and changed. Nodes whose tables are the
// same are left out, so identical tables give an empty result.
//
// Every route is first masked to its canonical block (cidr.Canonical), so
// 10.42.16.5/20 and 10.42.16.0/20 are one route here and the diff reports the
// masked form. Routes are matched by prefix, next hop and link. A route that is in both and
// differs in metric, source, path or whether it is active is Changed; a backup
// that took over from a failed primary is the typical case, and
// RouteChange.Activated says so. A node that has a table only in old has all
// its routes Removed, and one only in new has all its routes Added.
//
// The result is sorted by node, and each list by Compare, so the same two sets
// of tables always give the same diff.
//
// Example:
//
//	comparing the three-site tables with the direct link healthy and
//	with it down changes only builder-1 and dgx-spark. Each loses its direct
//	primary and activates its hub backup; aws-hub is not listed.
func Diff(old, updated Tables) []TableDiff {
	nodes := map[types.NodeID]bool{}
	for node := range old {
		nodes[node] = true
	}
	for node := range updated {
		nodes[node] = true
	}
	ordered := make([]types.NodeID, 0, len(nodes))
	for node := range nodes {
		ordered = append(ordered, node)
	}
	slices.Sort(ordered)

	var out []TableDiff
	for _, node := range ordered {
		var before, after []Route
		if table := old[node]; table != nil {
			before = table.Routes
		}
		if table := updated[node]; table != nil {
			after = table.Routes
		}
		if diff := diffTables(node, before, after); !diff.Empty() {
			out = append(out, diff)
		}
	}
	return out
}
