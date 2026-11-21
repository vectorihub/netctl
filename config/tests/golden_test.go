package tests

import (
	"encoding/json"
	"testing"

	"github.com/vectorihub/netctl/config"
)

// P1-CFG-001
//
// TestGoldenConfigs checks the three-site configs, healthy and failed over, are
// byte for byte the reviewed files, one per node: the exact text an agent would be
// handed. The reference values in the end-to-end guide are checked field by field
// in generate_test.go; these files pin every byte, hash included.
func TestGoldenConfigs(t *testing.T) {
	for _, state := range []struct {
		dir string
		up  bool
	}{{"healthy", true}, {"failed-over", false}} {
		for node, c := range threeSite(t, state.up) {
			data, err := c.Clone().Encode()
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "config/"+state.dir+"-"+string(node)+".json", data)
		}
	}
}

// P1-CFG-040
//
// TestGoldenDiffBetweenHealthyAndFailedOver checks the diff between the healthy and
// failed-over plans against the reviewed files, as text for a person and as JSON for
// a machine. The reading is the one in the end-to-end guide: builder-1 and dgx-spark
// change, in each two prefixes move from the direct peer to the hub, and the hub
// does not appear.
func TestGoldenDiffBetweenHealthyAndFailedOver(t *testing.T) {
	healthy, failed := threeSite(t, true), threeSite(t, false)
	var before, after []*config.NodeConfig
	for _, node := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		before = append(before, healthy[typesNode(node)])
		after = append(after, failed[typesNode(node)])
	}
	d := config.DiffPlans(before, after)
	if len(d.Nodes) != 2 || d.Nodes[0].Node != builder || d.Nodes[1].Node != dgx || len(d.NodesAdded)+len(d.NodesRemoved) != 0 {
		t.Fatalf("the plans differ on %+v, want builder-1 and dgx-spark only", d)
	}
	for _, nd := range d.Nodes {
		if len(nd.Moves) != 2 || len(nd.RoutesChanged) != 2 || len(nd.RoutesRemoved) != 2 || len(nd.PeersChanged) != 0 || len(nd.Other) != 0 {
			t.Errorf("%s: %+v, want two prefixes moved, two backups made active and two direct routes removed", nd.Node, nd)
		}
		for _, m := range nd.Moves {
			if m.To != hub || m.From == hub {
				t.Errorf("%s: moved %+v, want from the direct peer to the hub", nd.Node, m)
			}
		}
	}
	golden(t, "config/diff-healthy-vs-failed-over.txt", []byte(d.Text()))
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "config/diff-healthy-vs-failed-over.json", append(data, '\n'))
}
