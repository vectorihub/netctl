package tests

import (
	"go/parser"
	"go/token"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// The done-when of V23: a Topology is constructible in code with no YAML (D7).
func TestTopologyIsConstructibleInCode(t *testing.T) {
	built := topology.Topology{
		Schema: topology.Schema,
		Name:   "from-code",
		Sites:  []topology.Site{{ID: "aws", Provider: "aws"}, {ID: "builder"}},
		Nodes: []topology.Node{
			{ID: "aws-hub", Site: "aws", Role: types.RoleHub, NAT: topology.NATNone,
				Endpoints: []netip.AddrPort{netip.MustParseAddrPort("203.0.113.10:51820")}},
			{ID: "builder-1", Site: "builder", Role: types.RoleSpoke, NAT: topology.NATBehind,
				Advertises: []topology.Advertisement{{Site: true}}, Labels: map[string]string{"env": "prod"}},
		},
		Links: []topology.Link{{Kind: types.LinkHubSpoke, A: "aws-hub", B: "builder-1", Cost: 20, Health: topology.HealthAssumed}},
		Intents: []topology.Intent{{
			Action: topology.ActionAllow,
			From:   topology.Selector{Kind: topology.SelectorAll},
			To:     topology.Selector{Kind: topology.SelectorNode, ID: "aws-hub"},
			Ports:  []uint16{443},
		}},
	}
	if built.Intents[0].To.String() != "node:aws-hub" || built.Links[0].Kind.String() != "hub-spoke" || built.Nodes[1].NAT.String() != "behind" {
		t.Errorf("%+v", built)
	}
	// The zero Topology is also a value you can hold and read.
	var empty topology.Topology
	if len(empty.Nodes) != 0 || empty.Schema != "" {
		t.Error("the zero Topology should be empty")
	}
}

// types.go is where a topology is defined, and it must not know about YAML, so
// nothing but the loader depends on the file format.
func TestTypesFileImportsNoYAML(t *testing.T) {
	for _, file := range []string{"types.go", "selector.go"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", file), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			if strings.Contains(imported.Path.Value, "yaml") {
				t.Errorf("%s imports %s", file, imported.Path.Value)
			}
		}
	}
}
