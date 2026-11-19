package topology_test

// This file holds the runnable examples shown on the package documentation.
// They live beside the code, not in tests/, because pkg.go.dev only shows
// examples from the package's own directory. They use only the exported API.

import (
	"fmt"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// exampleYAML is a small network used by the examples: one cloud hub and two
// office machines behind NAT, each linked to the hub, plus a direct link
// between the offices and two traffic intents.
const exampleYAML = `schema: beaver.topology/v1alpha1
name: three-site

sites:
  - { id: cloud, provider: aws, region: eu-west-1 }
  - { id: london, provider: owned }
  - { id: paris, provider: owned }

nodes:
  - { id: hub-1, site: cloud, role: hub, nat: none, endpoints: [203.0.113.10:51820] }
  - { id: ldn-1, site: london, role: spoke, nat: behind, labels: { env: prod } }
  - { id: par-1, site: paris, role: spoke, nat: behind, labels: { env: dev } }

links:
  - { kind: hub-spoke, a: hub-1, b: ldn-1 }
  - { kind: hub-spoke, a: hub-1, b: par-1 }
  - { kind: direct, a: ldn-1, b: par-1, cost: 10 }

intents:
  - allow: { from: "site:london", to: "site:paris" }
  - deny: { from: "label:env=dev", to: "label:env=prod", ports: [5432] }
`

// mustLoad loads exampleYAML, panicking on error because it is known to be
// valid.
func mustLoad() *topology.Topology {
	network, _, err := topology.Load("three-site.yaml", []byte(exampleYAML), 0)
	if err != nil {
		panic(err)
	}
	return network
}

// Example loads a network description, prints what it contains and any
// warnings, and fingerprints it.
func Example() {
	network, report, err := topology.Load("three-site.yaml", []byte(exampleYAML), 0)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(network.Name, len(network.Sites), "sites,", len(network.Nodes), "nodes,", len(network.Links), "links")
	for _, warning := range report.Warnings() {
		fmt.Println("warning:", warning)
	}
	fmt.Println("hash:", network.Hash()[:12])
	// Output:
	// three-site 3 sites, 3 nodes, 3 links
	// warning: T8 warning: link ldn-1~par-1: needs-traversal: ldn-1 and par-1 are both behind NAT, so this link needs NAT traversal and the hub path must stay as its fallback
	// hash: 5207b0ec5ea2
}

// ExampleLoad shows that a mistake in the file is reported with the file name,
// the line, and a suggestion when a field name is misspelt.
func ExampleLoad() {
	data := []byte(`schema: beaver.topology/v1alpha1
name: broken
sites:
  - { id: cloud, provider: aws }
nodes:
  - { id: hub-1, site: cloud, rol: hub }
`)

	_, _, err := topology.Load("broken.yaml", data, 0)
	fmt.Println(types.HasCode(err, types.ErrInvalidTopology))
	fmt.Println(err)
	// Output:
	// true
	// INVALID_TOPOLOGY: broken.yaml:6: nodes[0]: unknown field "rol"; did you mean "role"?
	// INVALID_TOPOLOGY: broken.yaml:6: nodes[0]: missing required field "role"
}

// ExampleTopology_Check shows the rules being applied to a network built in
// Go code. Every violation is reported at once, each naming its rule.
func ExampleTopology_Check() {
	network := &topology.Topology{
		Schema: topology.Schema,
		Name:   "in-code",
		Sites:  []topology.Site{{ID: "office", Provider: "owned"}},
		Nodes: []topology.Node{
			{ID: "ldn-1", Site: "office", Role: types.RoleSpoke},
			{ID: "par-1", Site: "nowhere", Role: types.RoleSpoke},
		},
		Links: []topology.Link{
			{Kind: types.LinkDirect, A: "ldn-1", B: "ghost"},
		},
	}
	network.Normalize()

	report := network.Check()
	for _, finding := range report.Errors() {
		fmt.Println(finding)
	}
	// Output:
	// T3 error: node par-1: unknown site "nowhere"
	// T4 error: link ghost~ldn-1: unknown node "ghost"
	// T9 error: node ldn-1: spoke has no path to a hub
	// T9 error: node par-1: spoke has no path to a hub
}

// ExampleParseSelector shows selecting nodes by site, label or ID. Results are
// always sorted.
func ExampleParseSelector() {
	network := mustLoad()

	for _, text := range []string{"*", "site:paris", "label:env=prod", "hub-1"} {
		selector, err := topology.ParseSelector(text)
		if err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Println(text, "=>", selector.Resolve(network))
	}
	// Output:
	// * => [hub-1 ldn-1 par-1]
	// site:paris => [par-1]
	// label:env=prod => [ldn-1]
	// hub-1 => [hub-1]
}

// ExampleBuildGraph shows the graph view of a network. A link marked unusable
// keeps its edges, flagged as unusable, so Edges skips it while Neighbours
// still lists it.
func ExampleBuildGraph() {
	network := mustLoad()

	direct := types.LinkIDOf("ldn-1", "par-1")
	graph := topology.BuildGraph(network, func(link types.LinkID) bool { return link != direct })
	fmt.Println("nodes:", graph.Nodes(), "edges:", graph.EdgeCount())

	for _, edge := range graph.Neighbours("ldn-1") {
		fmt.Println("ldn-1 ->", edge.To, "cost", edge.Cost, "usable", edge.Usable)
	}
	for _, edge := range graph.Edges("ldn-1") {
		fmt.Println("usable: ldn-1 ->", edge.To)
	}
	// Output:
	// nodes: [hub-1 ldn-1 par-1] edges: 6
	// ldn-1 -> hub-1 cost 20 usable true
	// ldn-1 -> par-1 cost 10 usable false
	// usable: ldn-1 -> hub-1
}

// ExampleTopology_Hash shows that the hash ignores how a file is written, such
// as the order of nodes, but changes when the network itself changes.
func ExampleTopology_Hash() {
	original := mustLoad()
	reordered := mustLoad()
	reordered.Nodes[0], reordered.Nodes[2] = reordered.Nodes[2], reordered.Nodes[0]
	fmt.Println("same network, different order:", original.Hash() == reordered.Hash())

	changed := mustLoad()
	changed.Links[0].Cost++
	fmt.Println("different cost, same hash:", original.Hash() == changed.Hash())
	// Output:
	// same network, different order: true
	// different cost, same hash: false
}
