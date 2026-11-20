package route_test

// This file holds the runnable examples for computing routes: shortest paths,
// the tables of a whole network, backups, overrides and the address plan.
// They use only the exported API.

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// network is a small route.Graph for the examples. Any type with these three
// methods will do; in a real program the planner adapts topology.Graph.
type network struct {
	// roles holds each node's role.
	roles map[types.NodeID]types.Role
	// edges holds each node's edges.
	edges map[types.NodeID][]route.Edge
}

// Nodes returns every node, in no particular order. Compute sorts them.
func (n network) Nodes() []types.NodeID {
	nodes := make([]types.NodeID, 0, len(n.roles))
	for node := range n.roles {
		nodes = append(nodes, node)
	}
	return nodes
}

// Role returns a node's role.
func (n network) Role(node types.NodeID) types.Role { return n.roles[node] }

// Edges returns every edge out of a node.
func (n network) Edges(node types.NodeID) []route.Edge { return n.edges[node] }

// join adds a link between two nodes, as an edge in each direction.
func (n network) join(a, b types.NodeID, cost int, down bool) {
	link := types.LinkIDOf(a, b)
	n.edges[a] = append(n.edges[a], route.Edge{Link: link, To: b, Cost: cost, Down: down})
	n.edges[b] = append(n.edges[b], route.Edge{Link: link, To: a, Cost: cost, Down: down})
}

// threeSite returns the three-site network: aws-hub is the hub, builder-1 and
// dgx-spark are spokes, and the two spokes are also joined directly. directDown
// says whether the direct link is down.
func threeSite(directDown bool) network {
	n := network{
		roles: map[types.NodeID]types.Role{"aws-hub": types.RoleHub, "builder-1": types.RoleSpoke, "dgx-spark": types.RoleSpoke},
		edges: map[types.NodeID][]route.Edge{},
	}
	n.join("aws-hub", "builder-1", 20, false)
	n.join("aws-hub", "dgx-spark", 20, false)
	n.join("builder-1", "dgx-spark", 10, directDown)
	return n
}

// threeSiteOwned is what each node of the three-site network advertises: its
// site block.
func threeSiteOwned() route.Owned {
	return route.Owned{
		"aws-hub":   {netip.MustParsePrefix("10.42.0.0/20")},
		"builder-1": {netip.MustParsePrefix("10.42.16.0/20")},
		"dgx-spark": {netip.MustParsePrefix("10.42.32.0/20")},
	}
}

// ExampleSPF shows the shortest paths from one spoke. Spokes a and b, and
// spokes b and c, are joined directly, but a spoke never forwards for others,
// so a reaches c through the hub (cost 40) and not through b (cost 20).
func ExampleSPF() {
	n := network{roles: map[types.NodeID]types.Role{}, edges: map[types.NodeID][]route.Edge{}}
	n.roles["hub"] = types.RoleHub
	for _, spoke := range []types.NodeID{"a", "b", "c"} {
		n.roles[spoke] = types.RoleSpoke
		n.join("hub", spoke, 20, false)
	}
	n.join("a", "b", 10, false)
	n.join("b", "c", 10, false)

	best := route.SPF(n, "a", "")
	for _, dst := range []types.NodeID{"b", "c"} {
		path := make([]string, len(best[dst].Path))
		for i, node := range best[dst].Path {
			path[i] = string(node)
		}
		fmt.Printf("%s: cost %d, path %s\n", dst, best[dst].Cost, strings.Join(path, " > "))
	}
	// Output:
	// b: cost 10, path a > b
	// c: cost 40, path a > hub > c
}

// printRoutes prints the routes of a table for one prefix, in table order.
func printRoutes(table *route.Table, prefix string) {
	for _, r := range table.Routes {
		if r.Prefix == netip.MustParsePrefix(prefix) {
			state := "inactive"
			if r.Active {
				state = "active"
			}
			fmt.Printf("%s via %s, metric %d, %s, %s\n", r.Prefix, r.NextHop, r.Metric, r.Source, state)
		}
	}
}

// ExampleCompute shows the routes builder-1 holds for dgx-spark's block. While
// the direct link is healthy, it is the active route and the way through the
// hub waits as an inactive backup. When the direct link fails there is only
// the way through the hub, and no backup is left.
func ExampleCompute() {
	input := route.Input{Owned: threeSiteOwned()}
	prefix := "10.42.32.0/20"

	healthy, _ := route.Compute(threeSite(false), input)
	fmt.Println("direct link healthy:")
	printRoutes(healthy.Tables["builder-1"], prefix)

	failed, _ := route.Compute(threeSite(true), input)
	fmt.Println("direct link down:")
	printRoutes(failed.Tables["builder-1"], prefix)
	// Output:
	// direct link healthy:
	// 10.42.32.0/20 via dgx-spark, metric 10, computed, active
	// 10.42.32.0/20 via aws-hub, metric 40, backup, inactive
	// direct link down:
	// 10.42.32.0/20 via aws-hub, metric 40, computed, active
}

// ExampleCompute_unreachable shows that a node nobody can reach is reported as
// data, with a reason, and gets no route. Here dgx-spark has lost both links.
func ExampleCompute_unreachable() {
	n := network{
		roles: map[types.NodeID]types.Role{"aws-hub": types.RoleHub, "builder-1": types.RoleSpoke, "dgx-spark": types.RoleSpoke},
		edges: map[types.NodeID][]route.Edge{},
	}
	n.join("aws-hub", "builder-1", 20, false)
	n.join("aws-hub", "dgx-spark", 20, true)

	result, _ := route.Compute(n, route.Input{Owned: threeSiteOwned()})
	for _, pair := range result.Unreachable {
		fmt.Printf("%s cannot reach %s: %s\n", pair.From, pair.To, pair.Reason)
	}
	_, found := result.Tables["builder-1"].Lookup(netip.MustParseAddr("10.42.33.7"))
	fmt.Println("builder-1 has a route to dgx-spark:", found)
	// Output:
	// aws-hub cannot reach dgx-spark: no healthy path
	// builder-1 cannot reach dgx-spark: no healthy path
	// dgx-spark cannot reach aws-hub: no healthy path
	// dgx-spark cannot reach builder-1: no healthy path
	// builder-1 has a route to dgx-spark: false
}

// ExampleCompute_override shows static overrides. One that points at a real
// neighbour becomes a route flagged "static". One that points at a node that is
// not a neighbour is rejected, with an explanation, and changes nothing.
func ExampleCompute_override() {
	result, _ := route.Compute(threeSite(false), route.Input{
		Owned: threeSiteOwned(),
		Overrides: []route.Override{
			{Node: "builder-1", Prefix: netip.MustParsePrefix("192.168.0.0/16"), NextHop: "aws-hub"},
			{Node: "builder-1", Prefix: netip.MustParsePrefix("10.51.0.0/16"), NextHop: "nowhere"},
		},
	})
	found, _ := result.Tables["builder-1"].Lookup(netip.MustParseAddr("192.168.1.1"))
	fmt.Println(found.Prefix, "via", found.NextHop, found.Source)
	for _, rejection := range result.Rejected {
		fmt.Println("rejected:", rejection.Detail)
	}
	// Output:
	// 192.168.0.0/16 via aws-hub static
	// rejected: builder-1 has no link to "nowhere", so it cannot send 10.51.0.0/16 there
}

// ExampleCollect shows reading the destinations of a network from its address
// plan: the site block and overlay address each node announces, and the
// transit block of a direct link, which is local to its two ends.
func ExampleCollect() {
	registry, _ := ipam.New(ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: netip.MustParsePrefix("10.42.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: netip.MustParsePrefix("10.42.0.0/17"), ChildBits: 20},
			{Name: "overlay", Prefix: netip.MustParsePrefix("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
			{Name: "transit", Prefix: netip.MustParsePrefix("10.42.252.0/22"), ChildBits: 30},
		},
	}, &types.FixedClock{T: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}, ipam.DefaultQuarantine)
	registry.Allocate("sites", ipam.Site("aws"))
	registry.Allocate("overlay", ipam.Node("aws-hub"))
	registry.Allocate("overlay", ipam.Node("builder-1"))
	registry.AllocateTransit("aws-hub~builder-1")

	owned, local, _ := route.Collect(registry,
		[]route.Announcer{
			{Node: "aws-hub", Site: "aws", AdvertiseSite: true},
			{Node: "builder-1", Site: "builder"},
		},
		[]types.LinkID{"aws-hub~builder-1"})

	for _, node := range []types.NodeID{"aws-hub", "builder-1"} {
		fmt.Println(node, "announces", owned[node], "and holds locally", local[node])
	}
	// Output:
	// aws-hub announces [10.42.0.0/20 10.42.240.1/32] and holds locally [10.42.252.0/30]
	// builder-1 announces [10.42.240.2/32] and holds locally [10.42.252.0/30]
}
