package config_test

// This file holds the runnable examples shown on the package documentation. They use
// the exported API only, and the three-site reference network: builder-1 and
// dgx-spark joined directly, and each joined to aws-hub.

import (
	"fmt"
	"net/netip"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// plans generates the configs of the three-site network, with the direct link
// healthy or failed, keyed by node.
func plans(directUp bool) map[string]*config.NodeConfig {
	graph := fixture.ThreeSite(true)
	if !directUp {
		graph = fixture.ThreeSiteDirectDown()
	}
	computed, _ := route.Compute(graph, fixture.ThreeSiteInput())
	registry, _ := fixture.ThreeSitePlan()
	inputs, _ := config.FromPlan(fixture.ThreeSiteTopology(), registry, computed.Tables)
	configs, _ := config.Generate(inputs)
	out := map[string]*config.NodeConfig{}
	for _, c := range configs {
		out[string(c.Node)] = c
	}
	return out
}

// ExampleGenerate shows builder-1's peers, healthy: the hub carries the hub's site
// and address, the direct peer carries dgx-spark's, and each prefix is with one peer.
func ExampleGenerate() {
	c := plans(true)["builder-1"]
	for _, p := range c.Peers {
		fmt.Println(p.Node, p.Role, p.AllowedPrefixes)
	}
	// Output:
	// aws-hub hub [10.42.0.0/20 10.42.240.1/32]
	// dgx-spark direct [10.42.32.0/20 10.42.240.3/32 10.42.252.2/32]
}

// ExampleGenerate_failover shows the same node after the direct link fails: dgx-spark's
// site and address have moved from the direct peer to the hub, and only the probe
// address stays, so probes keep testing the direct link.
func ExampleGenerate_failover() {
	c := plans(false)["builder-1"]
	for _, p := range c.Peers {
		fmt.Println(p.Node, p.Role, p.AllowedPrefixes)
	}
	// Output:
	// aws-hub hub [10.42.0.0/20 10.42.32.0/20 10.42.240.1/32 10.42.240.3/32]
	// dgx-spark direct [10.42.252.2/32]
}

// ExampleFromPlan shows the inputs collected from an address plan: each node's overlay
// address and the block of the probed link.
func ExampleFromPlan() {
	computed, _ := route.Compute(fixture.ThreeSite(true), fixture.ThreeSiteInput())
	registry, _ := fixture.ThreeSitePlan()
	inputs, err := config.FromPlan(fixture.ThreeSiteTopology(), registry, computed.Tables)
	fmt.Println(err)
	fmt.Println(inputs.Overlay["builder-1"], inputs.Transit["builder-1~dgx-spark"])
	// Output:
	// <nil>
	// 10.42.240.2/32 10.42.252.0/30
}

// ExampleAllowedPrefixes shows rule G3 on one table: only active routes put a prefix
// with a peer, and the probe address is added to the direct peer.
func ExampleAllowedPrefixes() {
	computed, _ := route.Compute(fixture.ThreeSite(true), fixture.ThreeSiteInput())
	probes := map[types.NodeID]netip.Addr{"dgx-spark": netip.MustParseAddr("10.42.252.2")}
	allowed, conflicts := config.AllowedPrefixes(computed.Tables["builder-1"], probes)
	fmt.Println(allowed["aws-hub"])
	fmt.Println(allowed["dgx-spark"])
	fmt.Println(len(conflicts))
	// Output:
	// [10.42.0.0/20 10.42.240.1/32]
	// [10.42.32.0/20 10.42.240.3/32 10.42.252.2/32]
	// 0
}

// ExampleNodeConfig_Encode shows a config written out: the same bytes whatever order
// its lists were built in, with the hash in the last field.
func ExampleNodeConfig_Encode() {
	c := &config.NodeConfig{Schema: config.Schema, Node: "builder-1", Role: "spoke"}
	c.Advertise = []netip.Prefix{netip.MustParsePrefix("10.42.240.2/32"), netip.MustParsePrefix("10.42.16.0/20")}
	_, err := c.Encode()
	fmt.Println(err, c.Advertise)
	fmt.Println(len(c.Hash), c.Hash[:7])
	// Output:
	// <nil> [10.42.16.0/20 10.42.240.2/32]
	// 71 sha256:
}

// ExampleNodeConfig_Canonicalise shows the lists sorted and a prefix masked to its
// block.
func ExampleNodeConfig_Canonicalise() {
	c := &config.NodeConfig{Advertise: []netip.Prefix{netip.MustParsePrefix("10.42.240.2/32"), netip.MustParsePrefix("10.42.16.5/20")}}
	c.Canonicalise()
	fmt.Println(c.Advertise, c.Peers == nil)
	// Output:
	// [10.42.16.0/20 10.42.240.2/32] false
}

// ExampleNodeConfig_Clone shows a copy being changed without touching the original.
func ExampleNodeConfig_Clone() {
	c := plans(true)["builder-1"]
	copy := c.Clone()
	copy.Peers[0].Keepalive = 0
	fmt.Println(c.Peers[0].Keepalive, copy.Peers[0].Keepalive)
	// Output:
	// 25 0
}

// ExampleNodeConfig_Verify shows a config checked after it has been edited by hand.
func ExampleNodeConfig_Verify() {
	c := plans(true)["builder-1"]
	fmt.Println(c.Verify())
	c.Forwarding = true
	fmt.Println(c.Verify() != nil)
	// Output:
	// <nil>
	// true
}

// ExamplePlanID shows the plan ID, which carries the topology's name and follows the
// content of every node.
func ExamplePlanID() {
	healthy, failed := plans(true), plans(false)
	fmt.Println(healthy["aws-hub"].PlanID[:11], healthy["aws-hub"].PlanID == healthy["builder-1"].PlanID)
	fmt.Println(healthy["aws-hub"].PlanID == failed["aws-hub"].PlanID)
	// Output:
	// three-site@ true
	// false
}

// ExampleDecode shows a config read back, strictly: a key the schema does not have is
// refused.
func ExampleDecode() {
	data, _ := plans(true)["aws-hub"].Clone().Encode()
	c, err := config.Decode(data)
	fmt.Println(c.Node, err, c.Verify())
	_, err = config.Decode([]byte(`{"schema": "beaver.netctl/v1alpha1", "interface": "wg0"}`))
	fmt.Println(err != nil)
	// Output:
	// aws-hub <nil> <nil>
	// true
}

// ExampleByNode shows the configs looked up by node.
func ExampleByNode() {
	computed, _ := route.Compute(fixture.ThreeSite(true), fixture.ThreeSiteInput())
	registry, _ := fixture.ThreeSitePlan()
	inputs, _ := config.FromPlan(fixture.ThreeSiteTopology(), registry, computed.Tables)
	configs, _ := config.Generate(inputs)
	fmt.Println(config.ByNode(configs)["dgx-spark"].Overlay.Address)
	// Output:
	// 10.42.240.3/32
}

// ExampleDiff shows what a failover changes in builder-1's config: two prefixes move
// from the direct peer to the hub, and the two backups become the active routes.
func ExampleDiff() {
	d := config.Diff(plans(true)["builder-1"], plans(false)["builder-1"])
	for _, m := range d.Moves {
		fmt.Printf("%s moved from %s to %s\n", m.Prefix, m.From, m.To)
	}
	fmt.Println(len(d.RoutesRemoved), "routes removed,", len(d.RoutesChanged), "routes changed")
	// Output:
	// 10.42.32.0/20 moved from dgx-spark to aws-hub
	// 10.42.240.3/32 moved from dgx-spark to aws-hub
	// 2 routes removed, 2 routes changed
}

// ExampleNodeDiff_Text shows the diff written for people.
func ExampleNodeDiff_Text() {
	d := config.Diff(plans(true)["builder-1"], plans(false)["builder-1"])
	fmt.Print(d.Text())
	// Output:
	// builder-1
	//   moved 10.42.32.0/20 from dgx-spark to aws-hub
	//   moved 10.42.240.3/32 from dgx-spark to aws-hub
	//   route 10.42.32.0/20 via dgx-spark removed
	//   route 10.42.240.3/32 via dgx-spark removed
	//   route 10.42.32.0/20 via aws-hub: backup, metric 40, now active, metric 40
	//   route 10.42.240.3/32 via aws-hub: backup, metric 40, now active, metric 40
}

// ExampleNodeDiff_Empty shows that a config does not differ from itself.
func ExampleNodeDiff_Empty() {
	c := plans(true)["builder-1"]
	fmt.Println(config.Diff(c, c.Clone()).Empty())
	// Output:
	// true
}

// ExampleDiffPlans shows which nodes a failover changes: the hub is not among them.
func ExampleDiffPlans() {
	var before, after []*config.NodeConfig
	for _, node := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		before = append(before, plans(true)[node])
		after = append(after, plans(false)[node])
	}
	d := config.DiffPlans(before, after)
	for _, n := range d.Nodes {
		fmt.Println(n.Node)
	}
	// Output:
	// builder-1
	// dgx-spark
}

// ExamplePlanDiff_Empty shows that two plans with the same content are the same.
func ExamplePlanDiff_Empty() {
	a := plans(true)
	list := []*config.NodeConfig{a["aws-hub"], a["builder-1"], a["dgx-spark"]}
	fmt.Println(config.DiffPlans(list, list).Empty())
	// Output:
	// true
}

// ExamplePlanDiff_Text shows a plan diff written for people.
func ExamplePlanDiff_Text() {
	before := []*config.NodeConfig{plans(true)["dgx-spark"]}
	after := []*config.NodeConfig{plans(false)["dgx-spark"], plans(false)["aws-hub"]}
	fmt.Print(config.DiffPlans(before, after).Text()[:len("aws-hub added")])
	// Output:
	// aws-hub added
}

// ExampleScanSecrets shows a planted key being found, and the finding not repeating
// it.
func ExampleScanSecrets() {
	clean, _ := plans(true)["builder-1"].Clone().Encode()
	fmt.Println(len(config.ScanSecrets(clean)))
	planted := []byte(`{"peers": [{"private_key": "x", "public_key_ref": "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="}]}`)
	for _, f := range config.ScanSecrets(planted) {
		fmt.Println(f.Path, f.Rule)
	}
	// Output:
	// 0
	// peers[0].private_key secret-key-name
	// peers[0].public_key_ref wireguard-key
}

// ExampleScanOSFields shows a config that says how, not what, being found out.
func ExampleScanOSFields() {
	bad := []byte(`{"interface": "wg0", "routes": ["ip route add 10.0.0.0/8 dev wg0"]}`)
	for _, f := range config.ScanOSFields(bad) {
		fmt.Println(f.Path, f.Rule)
	}
	// Output:
	// interface os-key-name
	// interface interface-name
	// routes[0] command
	// routes[0] interface-name
}

// ExampleFinding_String shows a finding on one line.
func ExampleFinding_String() {
	f := config.Finding{Path: "peers[0].private_key", Rule: "secret-key-name", Why: "the key is named like a secret"}
	fmt.Println(f)
	// Output:
	// peers[0].private_key: secret-key-name: the key is named like a secret
}

// ExampleSchemaJSON shows the schema being handed out for a consumer's own validator.
func ExampleSchemaJSON() {
	schema := config.SchemaJSON()
	fmt.Println(len(schema) > 1000, string(schema[:1]))
	// Output:
	// true {
}
