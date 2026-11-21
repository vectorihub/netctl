package route_test

// This file holds the runnable examples shown on the package documentation.
// They live beside the code, not in tests/, because pkg.go.dev only shows
// examples from the package's own directory. They use only the exported API.

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// builderTable returns the routing table of one machine, builder-1, in a
// three-site network. The direct link to dgx-spark is healthy, so the route
// through it is active and the route through the hub is an inactive backup.
func builderTable() *route.Table {
	return &route.Table{
		Node: "builder-1",
		Routes: []route.Route{
			{
				Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: "aws-hub", Via: "aws-hub~builder-1",
				Metric: 40, Source: route.SourceBackup, Active: false,
				Path: []types.NodeID{"builder-1", "aws-hub", "dgx-spark"},
			},
			{
				Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: "dgx-spark", Via: "builder-1~dgx-spark",
				Metric: 10, Source: route.SourceComputed, Active: true,
				Path: []types.NodeID{"builder-1", "dgx-spark"},
			},
			{
				Prefix: netip.MustParsePrefix("10.42.16.0/20"),
				Source: route.SourceConnected, Active: true,
			},
			{
				Prefix: netip.MustParsePrefix("10.42.0.0/20"), NextHop: "aws-hub", Via: "aws-hub~builder-1",
				Metric: 20, Source: route.SourceComputed, Active: true,
				Path: []types.NodeID{"builder-1", "aws-hub"},
			},
		},
	}
}

// Example shows the usual use of the package: build a machine's table, sort it
// so it has one fixed order, and ask which way packets for different
// addresses go.
func Example() {
	table := builderTable()
	table.Sort()

	for _, dst := range []string{"10.42.1.1", "10.42.16.9", "10.42.33.7", "192.168.1.1"} {
		found, ok := table.Lookup(netip.MustParseAddr(dst))
		switch {
		case !ok:
			fmt.Printf("%s: no route\n", dst)
		case found.Source == route.SourceConnected:
			fmt.Printf("%s: local, on this machine\n", dst)
		default:
			fmt.Printf("%s: via %s over %s, metric %d\n", dst, found.NextHop, found.Via, found.Metric)
		}
	}
	// Output:
	// 10.42.1.1: via aws-hub over aws-hub~builder-1, metric 20
	// 10.42.16.9: local, on this machine
	// 10.42.33.7: via dgx-spark over builder-1~dgx-spark, metric 10
	// 192.168.1.1: no route
}

// ExampleTable_Lookup shows longest-prefix matching: the most specific route
// wins, a route for one machine beats the site that contains it, and an
// address outside every prefix has no route.
func ExampleTable_Lookup() {
	table := &route.Table{
		Node: "aws-hub",
		Routes: []route.Route{
			{Prefix: netip.MustParsePrefix("10.42.0.0/16"), NextHop: "builder-1", Source: route.SourceComputed, Active: true},
			{Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: "dgx-spark", Source: route.SourceComputed, Active: true},
			{Prefix: netip.MustParsePrefix("10.42.33.7/32"), NextHop: "builder-1", Source: route.SourceStatic, Active: true},
		},
	}

	for _, dst := range []string{"10.42.1.1", "10.42.33.1", "10.42.33.7", "172.16.0.1"} {
		found, ok := table.Lookup(netip.MustParseAddr(dst))
		if !ok {
			fmt.Println(dst, "-> no route")
			continue
		}
		fmt.Println(dst, "->", found.Prefix, "via", found.NextHop)
	}
	// Output:
	// 10.42.1.1 -> 10.42.0.0/16 via builder-1
	// 10.42.33.1 -> 10.42.32.0/20 via dgx-spark
	// 10.42.33.7 -> 10.42.33.7/32 via builder-1
	// 172.16.0.1 -> no route
}

// ExampleTable_Lookup_backup shows that a backup route is ignored while it is
// inactive, and used as soon as it becomes active, for example after the
// primary link fails.
func ExampleTable_Lookup_backup() {
	table := builderTable()
	dst := netip.MustParseAddr("10.42.33.7")

	found, _ := table.Lookup(dst)
	fmt.Println("link healthy:", found.NextHop, found.Source)

	// The direct link fails: the primary goes inactive, the backup takes over.
	for i := range table.Routes {
		switch {
		case table.Routes[i].NextHop == "dgx-spark":
			table.Routes[i].Active = false
		case table.Routes[i].Source == route.SourceBackup:
			table.Routes[i].Active = true
		}
	}
	found, _ = table.Lookup(dst)
	fmt.Println("link failed:", found.NextHop, found.Source)
	// Output:
	// link healthy: dgx-spark computed
	// link failed: aws-hub backup
}

// ExampleTable_Sort shows the fixed order of a table: by prefix, and for the
// same prefix by metric, so the cheaper route is listed first.
func ExampleTable_Sort() {
	table := builderTable()
	table.Sort()

	for _, r := range table.Routes {
		fmt.Println(r.Prefix, r.Source, "metric", r.Metric)
	}
	// Output:
	// 10.42.0.0/20 computed metric 20
	// 10.42.16.0/20 connected metric 0
	// 10.42.32.0/20 computed metric 10
	// 10.42.32.0/20 backup metric 40
}

// ExampleTable_FIB shows the two views of a table: Routes holds everything,
// including the backup, while FIB holds only the routes in use.
func ExampleTable_FIB() {
	table := builderTable()
	table.Sort()

	fmt.Println("all routes:", len(table.Routes))
	for _, r := range table.FIB() {
		fmt.Println("in use:", r.Prefix, r.Source)
	}
	// Output:
	// all routes: 4
	// in use: 10.42.0.0/20 computed
	// in use: 10.42.16.0/20 connected
	// in use: 10.42.32.0/20 computed
}

// ExampleCompare shows how two routes are ordered: by prefix first, then by
// metric.
func ExampleCompare() {
	cheap := route.Route{Prefix: netip.MustParsePrefix("10.42.32.0/20"), Metric: 10}
	dear := route.Route{Prefix: netip.MustParsePrefix("10.42.32.0/20"), Metric: 40}
	earlier := route.Route{Prefix: netip.MustParsePrefix("10.42.0.0/20"), Metric: 99}

	fmt.Println(route.Compare(cheap, dear))
	fmt.Println(route.Compare(dear, cheap))
	fmt.Println(route.Compare(earlier, cheap))
	fmt.Println(route.Compare(cheap, cheap))
	// Output:
	// -1
	// 1
	// -1
	// 0
}

// ExampleTables_Nodes shows ranging over a network's tables in a fixed order.
func ExampleTables_Nodes() {
	tables := route.Tables{
		"dgx-spark": {Node: "dgx-spark"},
		"aws-hub":   {Node: "aws-hub"},
		"builder-1": {Node: "builder-1"},
	}
	for _, node := range tables.Nodes() {
		fmt.Println(node)
	}
	// Output:
	// aws-hub
	// builder-1
	// dgx-spark
}

// ExampleParseSource shows reading a source from text.
func ExampleParseSource() {
	source, ok := route.ParseSource("backup")
	fmt.Println(source, ok)

	source, ok = route.ParseSource("learned")
	fmt.Println(source, ok)
	// Output:
	// backup true
	// unknown false
}

// ExampleRoute shows a route as JSON. The source is written as a word, and a
// connected route leaves out the next hop and link.
func ExampleRoute() {
	computed := route.Route{
		Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: "dgx-spark", Via: "builder-1~dgx-spark",
		Metric: 10, Source: route.SourceComputed, Active: true,
		Path: []types.NodeID{"builder-1", "dgx-spark"},
	}
	local := route.Route{Prefix: netip.MustParsePrefix("10.42.16.0/20"), Source: route.SourceConnected, Active: true}

	for _, r := range []route.Route{computed, local} {
		data, _ := json.Marshal(r)
		fmt.Println(string(data))
	}
	// Output:
	// {"prefix":"10.42.32.0/20","next_hop":"dgx-spark","via":"builder-1~dgx-spark","metric":10,"source":"computed","path":["builder-1","dgx-spark"],"active":true}
	// {"prefix":"10.42.16.0/20","metric":0,"source":"connected","active":true}
}

// ExampleTable_Index builds an index of a machine's table once and asks it
// several times. It gives the same answers as Table.Lookup, without scanning
// the whole table for each question.
func ExampleTable_Index() {
	index := builderTable().Index()

	for _, dst := range []string{"10.42.1.1", "10.42.33.7", "192.168.1.1"} {
		found, ok := index.Lookup(netip.MustParseAddr(dst))
		if !ok {
			fmt.Printf("%s: no route\n", dst)
			continue
		}
		fmt.Printf("%s: via %s over %s\n", dst, found.NextHop, found.Via)
	}
	// Output:
	// 10.42.1.1: via aws-hub over aws-hub~builder-1
	// 10.42.33.7: via dgx-spark over builder-1~dgx-spark
	// 192.168.1.1: no route
}

// ExampleSource_String shows the name of each source of a route.
func ExampleSource_String() {
	fmt.Println(route.SourceBackup, route.SourceComputed, route.SourceUnknown)
	// Output:
	// backup computed unknown
}

// ExampleSource_MarshalText shows a source written as its name, and the zero
// source refused with a coded error so that a half-filled route is never written.
func ExampleSource_MarshalText() {
	text, _ := route.SourceComputed.MarshalText()
	fmt.Println(string(text))
	_, err := route.SourceUnknown.MarshalText()
	fmt.Println(types.HasCode(err, types.ErrInvalidValue))
	// Output:
	// computed
	// true
}

// ExampleSource_UnmarshalText shows a name read back, and a name that is not one
// refused with a coded error and the receiver left alone.
func ExampleSource_UnmarshalText() {
	var source route.Source
	fmt.Println(source.UnmarshalText([]byte("static")), source)
	err := source.UnmarshalText([]byte("learned"))
	fmt.Println(types.HasCode(err, types.ErrInvalidValue), source)
	// Output:
	// <nil> static
	// true static
}

// ExampleIndex_Lookup builds an index once and asks it many times. It answers
// exactly as Table.Lookup does: the longest matching prefix wins, a route
// written with host bits set comes back as its masked block, and an address
// nothing covers has no route.
func ExampleIndex_Lookup() {
	table := &route.Table{Node: "builder-1", Routes: []route.Route{
		{Prefix: netip.MustParsePrefix("10.42.0.0/16"), NextHop: "aws-hub", Via: "aws-hub~builder-1", Metric: 20, Source: route.SourceComputed, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.32.7/20"), NextHop: "dgx-spark", Via: "builder-1~dgx-spark", Metric: 10, Source: route.SourceComputed, Active: true},
	}}
	index := table.Index()
	for _, dst := range []string{"10.42.33.7", "10.42.1.1", "192.168.1.1"} {
		r, ok := index.Lookup(netip.MustParseAddr(dst))
		fmt.Println(dst, r.Prefix, r.NextHop, ok)
	}
	// Output:
	// 10.42.33.7 10.42.32.0/20 dgx-spark true
	// 10.42.1.1 10.42.0.0/16 aws-hub true
	// 192.168.1.1 invalid Prefix  false
}
