package tests

import (
	"flag"
	"net/netip"
	"os"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// update rewrites the golden files from the current output instead of comparing
// with them: go test ./config/tests -run Golden -update. Read the diff before
// committing it.
var update = flag.Bool("update", false, "rewrite the golden config files")

// The nodes and links of the three-site network.
const (
	hub     = types.NodeID("aws-hub")
	builder = types.NodeID("builder-1")
	dgx     = types.NodeID("dgx-spark")
	direct  = types.LinkID("builder-1~dgx-spark")
)

// prefixes parses prefixes, panicking on bad text because test data is fixed.
func prefixes(texts ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(texts))
	for i, text := range texts {
		out[i] = netip.MustParsePrefix(text)
	}
	return out
}

// threeSiteInputs returns Generate's inputs for the three-site network with the
// direct link healthy (directUp) or failed.
func threeSiteInputs(t testing.TB, directUp bool) config.Inputs {
	t.Helper()
	graph := fixture.ThreeSite(true)
	if !directUp {
		graph = fixture.ThreeSiteDirectDown()
	}
	computed, err := route.Compute(graph, fixture.ThreeSiteInput())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.ThreeSitePlan()
	if err != nil {
		t.Fatal(err)
	}
	in, err := config.FromPlan(fixture.ThreeSiteTopology(), plan, computed.Tables)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// threeSite generates the configs of the three-site network, keyed by node.
func threeSite(t testing.TB, directUp bool) map[types.NodeID]*config.NodeConfig {
	t.Helper()
	configs, err := config.Generate(threeSiteInputs(t, directUp))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return config.ByNode(configs)
}

// peerOf returns the peer of the config with the given node, failing the test if
// there is none.
func peerOf(t testing.TB, c *config.NodeConfig, node types.NodeID) config.Peer {
	t.Helper()
	for _, p := range c.Peers {
		if p.Node == node {
			return p
		}
	}
	t.Fatalf("%s has no peer %s", c.Node, node)
	return config.Peer{}
}

// golden compares got with the golden file, or rewrites the file under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "../../testdata/golden/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden file: %v (create it with -update)", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the golden file; run with -update and review the change\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

// equalPrefixes reports whether two lists hold the same prefixes in the same order.
func equalPrefixes(a, b []netip.Prefix) bool { return slices.Equal(a, b) }

// newEmptyPlan returns an address plan with its pools and nothing allocated.
func newEmptyPlan() (*ipam.IPAM, error) { return fixture.NewPlan() }

// typesNode converts text to a node ID.
func typesNode(text string) types.NodeID { return types.NodeID(text) }
