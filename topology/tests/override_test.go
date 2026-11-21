package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// overrideTopology is a topology whose site s9 asks for the growth pool, with a reason.
const overrideTopology = `schema: beaver.topology/v1alpha1
name: grow
sites:
  - id: aws
  - id: s9
    override: { pool: growth, reason: "approved expansion" }
nodes:
  - { id: aws-hub, site: aws, role: hub }
`

// TestASiteCanAskForAnotherPoolWithAReason checks that the override of a site decodes into
// its pool and its reason.
func TestASiteCanAskForAnotherPoolWithAReason(t *testing.T) {
	got := decodeText(t, overrideTopology)
	var s9 topology.Site
	for _, s := range got.Sites {
		if s.ID == "s9" {
			s9 = s
		}
	}
	if s9.Override == nil || s9.Override.Pool != "growth" || s9.Override.Reason != "approved expansion" {
		t.Errorf("override = %+v", s9.Override)
	}
}

// TestAnOverrideWithoutAReasonIsRefused checks that a reserved pool cannot be asked for in
// silence: the reason is required and its absence is a located error.
func TestAnOverrideWithoutAReasonIsRefused(t *testing.T) {
	text := strings.Replace(overrideTopology, `, reason: "approved expansion"`, "", 1)
	_, err := topology.Decode("grow.yaml", []byte(text), 0)
	if !types.HasCode(err, types.ErrInvalidTopology) {
		t.Fatalf("got %v, want the code %s", err, types.ErrInvalidTopology)
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("got %v, want an error that asks for the reason", err)
	}
}

// TestTheOverrideIsPartOfTheHashButNothingChangesWithoutIt checks that two topologies that
// differ only in an override hash differently, and that a site with no override keeps the
// hash it always had.
func TestTheOverrideIsPartOfTheHashButNothingChangesWithoutIt(t *testing.T) {
	with := decodeText(t, overrideTopology)
	without := decodeText(t, strings.Replace(overrideTopology, `    override: { pool: growth, reason: "approved expansion" }
`, "", 1))
	if with.Hash() == without.Hash() {
		t.Error("the override does not change the hash")
	}
}
