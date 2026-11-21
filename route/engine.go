package route

import (
	"cmp"
	"fmt"
	"math"
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// Owned lists, for each node, the prefixes it holds: the blocks whose
// traffic ends at that node.
//
// It is used twice. As Input.Owned it holds what a node advertises to the
// whole network (its site block, its overlay address, any extra prefix), and
// every other node gets a route to it. As Input.Local it holds what is only
// local to a node, such as the /30 of a direct link, which each end connects
// to but never advertises further.
//
// Example:
//
//	owned := route.Owned{"dgx-spark": {netip.MustParsePrefix("10.42.32.0/20")}}
//	local := route.Owned{"builder-1": {netip.MustParsePrefix("10.42.252.0/30")}}
type Owned map[types.NodeID][]netip.Prefix

// Override is an operator's instruction to send traffic for one prefix to one
// neighbour, whatever the shortest-path calculation says.
//
// An override is accepted only if it points at a real neighbour. An accepted
// one is always flagged: its route has Source static, so it is never
// mistaken for a computed one.
//
// Example:
//
//	force := route.Override{Node: "builder-1", Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: "aws-hub"}
//	result, err := route.Compute(graph, route.Input{Owned: owned, Overrides: []route.Override{force}})
type Override struct {
	// Node is the node whose table gets the route.
	Node types.NodeID `json:"node"`
	// Prefix is the destination block.
	Prefix netip.Prefix `json:"prefix"`
	// NextHop is the neighbour the traffic is handed to.
	NextHop types.NodeID `json:"next_hop"`
	// Link is the link to leave on. Leave it empty to use the cheapest link
	// to NextHop, preferring one that is up.
	Link types.LinkID `json:"link,omitempty"`
	// Metric is the cost of the route. Zero means the cost of the link.
	Metric int `json:"metric,omitempty"`
}

// Input is everything Compute needs besides the graph.
//
// Example:
//
//	result, err := route.Compute(graph, route.Input{Owned: owned, Local: local})
type Input struct {
	// Owned is what each node advertises to the whole network.
	Owned Owned
	// Local is what is local to a node and never advertised, such as the
	// transit /30 of a direct link, which both ends hold.
	Local Owned
	// Overrides are operator-forced routes. Each one is accepted or appears
	// in Result.Rejected; none is dropped silently.
	Overrides []Override
}

// Reason says why a node cannot reach another.
//
// Example:
//
//	for _, pair := range result.Unreachable {
//		fmt.Printf("%s cannot reach %s: %s\n", pair.From, pair.To, pair.Reason)
//	}
type Reason string

const (
	// ReasonNoPath means no chain of working links joins the two nodes.
	//
	// Example:
	//
	//	pair.Reason == route.ReasonNoPath // the pair's Reason reads "no healthy path"
	ReasonNoPath Reason = "no healthy path"
	// ReasonSpokeTransit means the only way between them passes through a
	// spoke, and a spoke never forwards traffic for others.
	//
	// Example:
	//
	//	pair.Reason == route.ReasonSpokeTransit // the pair's Reason reads "the only path passes through a spoke, which never forwards for others"
	ReasonSpokeTransit Reason = "the only path passes through a spoke, which never forwards for others"
)

// Pair is a source node and a destination node with no path between them. It
// is data, not an error: a plan with unreachable pairs is still a plan, and
// the caller decides whether that is acceptable.
//
// Example:
//
//	for _, pair := range result.Unreachable {
//		fmt.Println(pair.From, "->", pair.To, pair.Prefixes, pair.Reason)
//	}
type Pair struct {
	// From is the node that cannot reach To.
	From types.NodeID `json:"from"`
	// To is the node that cannot be reached.
	To types.NodeID `json:"to"`
	// Prefixes are the destination's advertised blocks, none of which has a
	// route on From. It is empty if To advertises nothing.
	Prefixes []netip.Prefix `json:"prefixes,omitempty"`
	// Reason says why there is no path.
	Reason Reason `json:"reason"`
}

// RejectReason says why an override was not accepted.
//
// Example:
//
//	for _, rejection := range result.Rejected {
//		if rejection.Reason == route.RejectNotNeighbour { /* fix the next hop */ }
//	}
type RejectReason string

const (
	// RejectUnknownNode means the override names a node that is not in the graph.
	//
	// Example:
	//
	//	rejection.Reason == route.RejectUnknownNode // "the node does not exist"
	RejectUnknownNode RejectReason = "the node does not exist"
	// RejectBadPrefix means the override's prefix is not a valid IPv4 prefix.
	//
	// Example:
	//
	//	rejection.Reason == route.RejectBadPrefix // "the prefix is not a valid IPv4 prefix"
	RejectBadPrefix RejectReason = "the prefix is not a valid IPv4 prefix"
	// RejectBadMetric means the override's metric is negative.
	//
	// Example:
	//
	//	rejection.Reason == route.RejectBadMetric // "the metric is negative"
	RejectBadMetric RejectReason = "the metric is negative"
	// RejectNotNeighbour means the next hop is not joined to the node by any link.
	//
	// Example:
	//
	//	rejection.Reason == route.RejectNotNeighbour // "the next hop is not a neighbour of the node"
	RejectNotNeighbour RejectReason = "the next hop is not a neighbour of the node"
	// RejectWrongLink means the named link does not join the node and its next hop.
	//
	// Example:
	//
	//	rejection.Reason == route.RejectWrongLink // "the link does not join the node and its next hop"
	RejectWrongLink RejectReason = "the link does not join the node and its next hop"
	// RejectConnected means the prefix belongs to the node itself, where a
	// connected route already answers.
	//
	// Example:
	//
	//	rejection.Reason == route.RejectConnected // "the prefix is connected on the node"
	RejectConnected RejectReason = "the prefix is connected on the node"
	// RejectDuplicate means another accepted override already covers this
	// node and prefix.
	//
	// Example:
	//
	//	rejection.Reason == route.RejectDuplicate // "another override already covers this prefix on the node"
	RejectDuplicate RejectReason = "another override already covers this prefix on the node"
)

// Rejection is an override that was not accepted, with the reason.
//
// Example:
//
//	for _, rejection := range result.Rejected {
//		fmt.Println(rejection.Override.Node, rejection.Reason, rejection.Detail)
//	}
type Rejection struct {
	// Override is the override as given.
	Override Override `json:"override"`
	// Reason says which rule it broke.
	Reason RejectReason `json:"reason"`
	// Detail explains it in words that name the node, prefix and link.
	Detail string `json:"detail"`
}

// Result is what Compute returns.
//
// Example:
//
//	result, err := route.Compute(graph, input)
//	if err != nil { return err }
//	fib := result.Tables["builder-1"].FIB()
type Result struct {
	// Tables holds one table for every node, each sorted by Compare.
	Tables Tables
	// Unreachable lists every (from, to) pair with no path, sorted by from and
	// then to. Nothing is dropped silently: a pair with no route is here.
	Unreachable []Pair
	// Rejected lists the overrides that were not accepted, in the order they
	// were considered (sorted by node, prefix and next hop).
	Rejected []Rejection
}

// snapshot is a Graph copied once, so that Compute asks the caller's graph
// each question only once and the answers cannot change during the run.
type snapshot struct {
	// nodes are the node IDs, sorted and without repeats.
	nodes []types.NodeID
	// roles holds each node's role.
	roles map[types.NodeID]types.Role
	// edges holds each node's edges, sorted so the order is fixed.
	edges map[types.NodeID][]Edge
}

// Nodes returns the sorted node IDs.
func (snap *snapshot) Nodes() []types.NodeID { return snap.nodes }

// Role returns the role of node.
func (snap *snapshot) Role(node types.NodeID) types.Role { return snap.roles[node] }

// Edges returns every edge out of node, sorted.
func (snap *snapshot) Edges(node types.NodeID) []Edge { return snap.edges[node] }

// compareEdges orders edges by neighbour, link, cost and then up before down,
// so a snapshot never depends on the order the caller listed them in.
func compareEdges(first, second Edge) int {
	return cmp.Or(
		cmp.Compare(first.To, second.To),
		cmp.Compare(first.Link, second.Link),
		cmp.Compare(first.Cost, second.Cost),
		compareBool(first.Down, second.Down),
	)
}

// MaxTotalCost is the most the costs of all a graph's edges may add up to. Any
// path is a subset of the edges, so no path cost can be larger, and every sum
// fits a 32-bit int: metrics never wrap round to a negative number, on any
// platform. Compute refuses a graph over it.
//
// Example:
//
//	route.MaxTotalCost // 2147483647, the largest int32
const MaxTotalCost = math.MaxInt32

// takeSnapshot copies graph. It adds a problem for every edge that leads to a
// node the graph does not list, for every negative cost, because such a graph
// has no meaningful shortest path, and when the costs of all edges add up to
// more than MaxTotalCost, because a path cost could then overflow.
func takeSnapshot(graph Graph, problems *types.List) *snapshot {
	snap := &snapshot{roles: map[types.NodeID]types.Role{}, edges: map[types.NodeID][]Edge{}}
	var total int64
	snap.nodes = slices.Clone(graph.Nodes())
	slices.Sort(snap.nodes)
	snap.nodes = slices.Compact(snap.nodes)
	known := make(map[types.NodeID]bool, len(snap.nodes))
	for _, node := range snap.nodes {
		known[node] = true
	}
	for _, node := range snap.nodes {
		snap.roles[node] = graph.Role(node)
		edges := slices.Clone(graph.Edges(node))
		slices.SortFunc(edges, compareEdges)
		for _, edge := range edges {
			if !known[edge.To] {
				problems.Add(types.Errf(types.ErrInvalidTopology, "node "+string(node),
					"link %s leads to %q, which is not a node of the graph", edge.Link, edge.To))
			}
			if edge.Cost < 0 {
				problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(edge.Link),
					"cost %d from %s is negative, and shortest paths need costs of zero or more", edge.Cost, node))
			}
		}
		snap.edges[node] = edges
		for _, edge := range edges {
			total = addTotal(total, edge.Cost)
		}
	}
	if total > MaxTotalCost {
		problems.Add(types.Errf(types.ErrInvalidTopology, "graph",
			"the link costs add up to more than %d, so a path cost could overflow", int64(MaxTotalCost)))
	}
	return snap
}

// addTotal adds one edge's cost to a running total of edge costs and returns the
// new total. The total saturates at MaxTotalCost+1: once it is over the limit it
// stays just over it, and the remaining headroom is checked before adding, so
// the sum can never wrap round however large the costs are (two edges of
// math.MaxInt each give MaxTotalCost+1, not a negative number). Costs of zero
// or less add nothing; negative costs are reported separately.
func addTotal(total int64, cost int) int64 {
	const over = int64(MaxTotalCost) + 1
	if cost <= 0 || total >= over {
		return total
	}
	if int64(cost) >= over-total {
		return over
	}
	return total + int64(cost)
}

// normalise checks one Owned map against the known nodes and returns it with
// every prefix in canonical form, sorted and without repeats. label names the
// map in error messages. A prefix that is not a valid IPv4 prefix, or that
// belongs to a node the graph does not have, is added to problems.
func normalise(label string, owned Owned, known map[types.NodeID]bool, problems *types.List) map[types.NodeID][]netip.Prefix {
	out := make(map[types.NodeID][]netip.Prefix, len(owned))
	nodes := make([]types.NodeID, 0, len(owned))
	for node := range owned {
		nodes = append(nodes, node)
	}
	slices.Sort(nodes)
	for _, node := range nodes {
		if !known[node] {
			problems.Add(types.Errf(types.ErrInvalidTopology, label+" "+string(node), "the node is not in the graph"))
			continue
		}
		var prefixes []netip.Prefix
		for _, prefix := range owned[node] {
			if !prefix.IsValid() || !prefix.Addr().Is4() {
				problems.Add(types.Errf(types.ErrInvalidPrefix, label+" "+string(node), "%s is not a valid IPv4 prefix", prefix))
				continue
			}
			canonical, _ := cidr.Canonical(prefix)
			prefixes = append(prefixes, canonical)
		}
		slices.SortFunc(prefixes, cidr.Compare)
		out[node] = slices.Compact(prefixes)
	}
	return out
}

// checkOwners adds a problem for every prefix that two nodes both advertise,
// and for every local prefix that another node advertises. Two owners of one
// block would make the routes to it a matter of luck.
func checkOwners(owned, local map[types.NodeID][]netip.Prefix, nodes []types.NodeID, problems *types.List) {
	owner := map[netip.Prefix]types.NodeID{}
	for _, node := range nodes {
		for _, prefix := range owned[node] {
			if first, taken := owner[prefix]; taken {
				problems.Add(types.Errf(types.ErrOverlap, "prefix "+prefix.String(), "advertised by both %s and %s", first, node))
				continue
			}
			owner[prefix] = node
		}
	}
	for _, node := range nodes {
		for _, prefix := range local[node] {
			if first, taken := owner[prefix]; taken && first != node {
				problems.Add(types.Errf(types.ErrOverlap, "prefix "+prefix.String(), "local to %s but advertised by %s", node, first))
			}
		}
	}
}

// Compute builds the routing table of every node.
//
// For each node it adds:
//   - a connected route for every prefix the node holds, advertised or local;
//   - an active computed route for every prefix another node advertises,
//     along the best path to that node (see SPF), so the first hop of the
//     best path is the next hop;
//   - one inactive backup route for the same prefix, found by searching again
//     with the primary's first link removed, if such a path exists;
//   - an active static route for every accepted override.
//
// Overlay addresses need no special case: a node's /32 is advertised by that
// node, so it follows the same next hop as the node's site block.
//
// A node that cannot be reached gets no route, and the pair is listed in
// Result.Unreachable with a reason. An override that is not accepted is listed
// in Result.Rejected. Nothing is dropped silently.
//
// An accepted override replaces the computed route for the same prefix: the
// computed route stays in the table, inactive, so a table never has two
// active routes for one prefix. An override does not follow link health. If
// its link goes down, the route stays active, and it is the validator's job
// to report that.
//
// The result does not depend on the order of the graph's nodes or edges, of
// the prefixes, or of the overrides: the same network gives the same tables
// on every run.
//
// Compute returns an error, and no tables, if the input is broken: an edge to
// a node that does not exist or with a negative cost, link costs that add up
// to more than MaxTotalCost (INVALID_TOPOLOGY), a prefix that is not an IPv4
// prefix, a node in Owned or Local that is not in the graph, or a
// prefix advertised by two nodes (OVERLAP). Every problem is reported at once
// as a types.List that types.HasCode searches.
//
// Example:
//
//	in the three-site network with the direct link up, builder-1 gets an
//	active route to dgx-spark's block with metric 10 and an inactive backup via
//	aws-hub with metric 40; with the direct link down, only the route via
//	aws-hub, metric 40, and no backup.
func Compute(graph Graph, input Input) (Result, error) {
	var problems types.List
	snap := takeSnapshot(graph, &problems)
	known := make(map[types.NodeID]bool, len(snap.nodes))
	for _, node := range snap.nodes {
		known[node] = true
	}
	owned := normalise("owned", input.Owned, known, &problems)
	local := normalise("local", input.Local, known, &problems)
	checkOwners(owned, local, snap.nodes, &problems)
	if err := problems.Err(); err != nil {
		return Result{}, err
	}

	result := Result{Tables: Tables{}}
	accepted, rejected := screenOverrides(snap, owned, local, input.Overrides)
	result.Rejected = rejected
	ix := newIndex(snap)
	primary, alternative := newSearch(ix), newSearch(ix)
	for srcNumber, src := range ix.nodes {
		table := &Table{Node: src}
		addConnected(table, owned[src], local[src])
		primary.run(int32(srcNumber), none)

		// Group the reachable destinations by the link their primary leaves
		// on, so each distinct first link costs one search for backups.
		byLink := map[int32][]int32{}
		var blind map[types.NodeID]bool
		for dstNumber, dst := range ix.nodes {
			if dstNumber == srcNumber {
				continue
			}
			if primary.state[dstNumber] != settled {
				if blind == nil {
					blind = reachableIgnoringRoles(snap, src)
				}
				reason := ReasonNoPath
				if blind[dst] {
					reason = ReasonSpokeTransit
				}
				result.Unreachable = append(result.Unreachable, Pair{From: src, To: dst, Prefixes: slices.Clone(owned[dst]), Reason: reason})
				continue
			}
			byLink[primary.link[dstNumber]] = append(byLink[primary.link[dstNumber]], int32(dstNumber))
		}
		links := make([]int32, 0, len(byLink))
		for link := range byLink {
			links = append(links, link)
		}
		slices.Sort(links)
		for _, link := range links {
			alternative.run(int32(srcNumber), link)
			for _, dstNumber := range byLink[link] {
				prefixes := owned[ix.nodes[dstNumber]]
				for _, prefix := range prefixes {
					table.Routes = append(table.Routes, routeFrom(primary, dstNumber, prefix, SourceComputed, true))
					if alternative.state[dstNumber] == settled {
						table.Routes = append(table.Routes, routeFrom(alternative, dstNumber, prefix, SourceBackup, false))
					}
				}
			}
		}
		applyOverrides(table, accepted[src])
		table.Sort()
		result.Tables[src] = table
	}
	return result, nil
}

// addConnected adds a connected route for every prefix the node holds. owned
// and local are sorted, and a prefix in both is added once.
func addConnected(table *Table, owned, local []netip.Prefix) {
	all := append(slices.Clone(owned), local...)
	slices.SortFunc(all, cidr.Compare)
	for _, prefix := range slices.Compact(all) {
		table.Routes = append(table.Routes, Route{
			Prefix: prefix, Source: SourceConnected, Active: true,
			Path: []types.NodeID{table.Node},
		})
	}
}

// routeFrom builds a route for prefix along the best path the search found to
// a node. Every route gets its own copy of the path.
func routeFrom(found *search, node int32, prefix netip.Prefix, source Source, active bool) Route {
	best := found.best(node)
	return Route{
		Prefix: prefix, NextHop: best.First, Via: best.Link, Metric: best.Cost,
		Source: source, Path: best.Path, Active: active,
	}
}

// reachableIgnoringRoles returns the nodes src could reach if every node were
// allowed to forward. Comparing it with the real answer tells "the only way
// crosses a spoke" apart from "there is no way at all".
func reachableIgnoringRoles(graph Graph, src types.NodeID) map[types.NodeID]bool {
	seen := map[types.NodeID]bool{src: true}
	pending := []types.NodeID{src}
	for len(pending) > 0 {
		node := pending[0]
		pending = pending[1:]
		for _, edge := range graph.Edges(node) {
			if edge.Down || edge.Cost < 0 || seen[edge.To] {
				continue
			}
			seen[edge.To] = true
			pending = append(pending, edge.To)
		}
	}
	return seen
}

// compareOverrides orders overrides by node, canonical prefix, next hop, link
// and metric, and last by the prefix exactly as it was given (address, then
// length), so 10.9.0.0/24 sorts before 10.9.0.5/24. Every field of an Override
// takes part, so it returns 0 only for two identical overrides and the order is
// total: which of two duplicates is kept, and which spelling a rejection
// reports, never depends on the order they were given in.
func compareOverrides(first, second Override) int {
	firstBlock, _ := cidr.Canonical(first.Prefix)
	secondBlock, _ := cidr.Canonical(second.Prefix)
	return cmp.Or(
		cmp.Compare(first.Node, second.Node),
		cidr.Compare(firstBlock, secondBlock),
		cmp.Compare(first.NextHop, second.NextHop),
		cmp.Compare(first.Link, second.Link),
		cmp.Compare(first.Metric, second.Metric),
		compareSpelling(first.Prefix, second.Prefix),
	)
}

// compareSpelling orders two prefixes exactly as written, host bits and all:
// by address, then by length. It is used only as a last tie-break between
// spellings of one block, after the canonical blocks have already been compared.
func compareSpelling(first, second netip.Prefix) int {
	return cmp.Or(first.Addr().Compare(second.Addr()), cmp.Compare(first.Bits(), second.Bits()))
}

// screenOverrides decides every override. It returns the accepted ones as
// routes, by node, and the rejected ones with the reason.
func screenOverrides(snap *snapshot, owned, local map[types.NodeID][]netip.Prefix, overrides []Override) (map[types.NodeID][]Route, []Rejection) {
	sorted := slices.Clone(overrides)
	slices.SortFunc(sorted, compareOverrides)
	accepted := map[types.NodeID][]Route{}
	taken := map[types.NodeID]map[netip.Prefix]bool{}
	var rejected []Rejection
	reject := func(given Override, reason RejectReason, format string, args ...any) {
		rejected = append(rejected, Rejection{Override: given, Reason: reason, Detail: fmt.Sprintf(format, args...)})
	}
	for _, given := range sorted {
		edges, exists := snap.edges[given.Node]
		if !exists {
			reject(given, RejectUnknownNode, "%q is not a node of the network", given.Node)
			continue
		}
		if !given.Prefix.IsValid() || !given.Prefix.Addr().Is4() {
			reject(given, RejectBadPrefix, "%s is not a valid IPv4 prefix", given.Prefix)
			continue
		}
		prefix, _ := cidr.Canonical(given.Prefix)
		if given.Metric < 0 {
			reject(given, RejectBadMetric, "metric %d for %s on %s is below zero", given.Metric, prefix, given.Node)
			continue
		}
		var toHop []Edge
		for _, edge := range edges {
			if edge.To == given.NextHop {
				toHop = append(toHop, edge)
			}
		}
		if len(toHop) == 0 || given.NextHop == given.Node {
			reject(given, RejectNotNeighbour, "%s has no link to %q, so it cannot send %s there", given.Node, given.NextHop, prefix)
			continue
		}
		chosen, found := chooseEdge(toHop, given.Link)
		if !found {
			reject(given, RejectWrongLink, "link %q does not join %s and %s", given.Link, given.Node, given.NextHop)
			continue
		}
		if slices.Contains(owned[given.Node], prefix) || slices.Contains(local[given.Node], prefix) {
			reject(given, RejectConnected, "%s is on %s itself, where a connected route already answers", prefix, given.Node)
			continue
		}
		if taken[given.Node][prefix] {
			reject(given, RejectDuplicate, "%s on %s is already covered by another override", prefix, given.Node)
			continue
		}
		if taken[given.Node] == nil {
			taken[given.Node] = map[netip.Prefix]bool{}
		}
		taken[given.Node][prefix] = true
		metric := given.Metric
		if metric == 0 {
			metric = chosen.Cost
		}
		accepted[given.Node] = append(accepted[given.Node], Route{
			Prefix: prefix, NextHop: given.NextHop, Via: chosen.Link, Metric: metric,
			Source: SourceStatic, Path: []types.NodeID{given.Node, given.NextHop}, Active: true,
		})
	}
	return accepted, rejected
}

// chooseEdge picks the edge an override leaves on, from the edges that lead to
// its next hop. If link is not empty it must be one of them. Otherwise the
// best is one that is up, then the cheapest, then the smallest link ID. It
// reports false if link is named and none of the edges uses it.
func chooseEdge(edges []Edge, link types.LinkID) (Edge, bool) {
	var best Edge
	found := false
	for _, edge := range edges {
		if link != "" && edge.Link != link {
			continue
		}
		if !found || cmp.Or(compareBool(edge.Down, best.Down), cmp.Compare(edge.Cost, best.Cost), cmp.Compare(edge.Link, best.Link)) < 0 {
			best, found = edge, true
		}
	}
	return best, found
}

// applyOverrides adds the accepted static routes to a table and takes the
// computed routes for the same prefixes out of use, so the table never has two
// active routes for one prefix. The computed routes stay in the table, inactive.
func applyOverrides(table *Table, statics []Route) {
	for _, static := range statics {
		for i := range table.Routes {
			if table.Routes[i].Prefix == static.Prefix && table.Routes[i].Source == SourceComputed {
				table.Routes[i].Active = false
			}
		}
		table.Routes = append(table.Routes, static)
	}
}
