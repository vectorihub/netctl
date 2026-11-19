package tests

import (
	"reflect"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// resolve parses a selector and resolves it against topo, failing the test if the text does not
// parse.
func resolve(t *testing.T, topo *topology.Topology, text string) []types.NodeID {
	t.Helper()
	selector, err := topology.ParseSelector(text)
	if err != nil {
		t.Fatal(err)
	}
	return selector.Resolve(topo)
}

// TestResolveEveryKind checks Resolve for each selector kind against labelled nodes, including
// selectors that match nothing.
func TestResolveEveryKind(t *testing.T) {
	topo := decode(t, "three-site.yaml")
	topo.Nodes[1].Labels = map[string]string{"env": "prod", "gpu": "no"}
	topo.Nodes[2].Labels = map[string]string{"env": "prod"}
	for text, want := range map[string][]types.NodeID{
		"*":                {"aws-hub", "builder-1", "dgx-spark"},
		"node:builder-1":   {"builder-1"},
		"dgx-spark":        {"dgx-spark"},
		"site:aws":         {"aws-hub"},
		"label:env=prod":   {"builder-1", "dgx-spark"},
		"label:gpu=no":     {"builder-1"},
		"label:gpu=yes":    nil,
		"label:env=Prod":   nil,
		"label:tier=prod":  nil,
		"site:mars":        nil,
		"node:ghost":       nil,
		"site:builder":     {"builder-1"},
		"label:env=prod  ": nil, // trailing spaces are not a label value (ParseSelector refuses it)
	} {
		selector, err := topology.ParseSelector(text)
		if err != nil {
			if want != nil {
				t.Errorf("%q: %v", text, err)
			}
			continue
		}
		if got := selector.Resolve(topo); !reflect.DeepEqual(got, want) {
			t.Errorf("Resolve(%q) = %v, want %v", text, got, want)
		}
	}
}

// Deterministic order, whatever order the nodes are in; nil topology and unknown
// selector pick nothing; Resolve changes nothing.
func TestResolveIsOrderedAndPure(t *testing.T) {
	topo := &topology.Topology{Nodes: []topology.Node{
		{ID: "n-2", Site: "s"}, {ID: "N-1", Site: "s"}, {ID: "n-10", Site: "s"}, {ID: "n-1", Site: "t"}, {ID: "n-2", Site: "s"},
	}}
	before := slices.Clone(topo.Nodes)
	got := resolve(t, topo, "site:s")
	if want := []types.NodeID{"N-1", "n-10", "n-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (byte order, no repeats)", got, want)
	}
	if !reflect.DeepEqual(before, topo.Nodes) {
		t.Error("Resolve changed the topology")
	}
	if got := (topology.Selector{}).Resolve(topo); got != nil {
		t.Errorf("unknown selector picked %v", got)
	}
	if got := (topology.Selector{Kind: topology.SelectorAll}).Resolve(nil); got != nil {
		t.Errorf("nil topology gave %v", got)
	}
	if (topology.Selector{}).Matches(topo.Nodes[0]) {
		t.Error("unknown selector matched")
	}
}

// What the intents of three-site pick, end to end through the parser.
func TestResolveIntentsOfThreeSite(t *testing.T) {
	topo := decode(t, "three-site.yaml")
	deny := topo.Intents[3]
	if got := deny.From.Resolve(topo); !reflect.DeepEqual(got, []types.NodeID{"builder-1"}) {
		t.Errorf("from = %v", got)
	}
	if got := deny.To.Resolve(topo); !reflect.DeepEqual(got, []types.NodeID{"aws-hub"}) {
		t.Errorf("to = %v", got)
	}
}
