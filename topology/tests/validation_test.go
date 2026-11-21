package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// withValidation returns the three-site fixture with a validation section added.
func withValidation(t *testing.T, section string) []byte {
	t.Helper()
	return append(fixture(t, "three-site.yaml"), []byte("\n"+section)...)
}

// TestValidationSuppressionsAreRead checks the validation section is read: the rule,
// the subject, the reason, and an entry with no subject or no reason, which the
// validator (not the loader) refuses.
func TestValidationSuppressionsAreRead(t *testing.T) {
	data := withValidation(t, `validation:
  suppress:
    - rule: V-TOPO-003
      subject: dgx-spark
      reason: "single uplink accepted until the second ISP is installed (ticket BC-41)"
    - rule: V-TOPO-004
    - rule: V-TOPO-020
      reason: "two hubs on purpose"
`)
	top, err := topology.Decode("t.yaml", data, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := top.Validation.Suppress
	if len(got) != 3 {
		t.Fatalf("suppressions %+v", got)
	}
	if got[0] != (topology.Suppression{Rule: "V-TOPO-003", Subject: "dgx-spark", Reason: "single uplink accepted until the second ISP is installed (ticket BC-41)"}) {
		t.Errorf("first %+v", got[0])
	}
	if got[1].Subject != "" || got[1].Reason != "" || got[2].Reason != "two hubs on purpose" {
		t.Errorf("optional fields: %+v %+v", got[1], got[2])
	}
}

// TestValidationDoesNotChangeTheHash checks what the file says about the validator is
// not part of the network: the same network with and without suppressions has one hash.
func TestValidationDoesNotChangeTheHash(t *testing.T) {
	plain := decode(t, "three-site.yaml")
	with, err := topology.Decode("t.yaml", withValidation(t, "validation:\n  suppress:\n    - { rule: V-TOPO-004, subject: builder-1~dgx-spark, reason: accepted }\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Hash() != with.Hash() {
		t.Error("a suppression changed the hash of the network")
	}
}

// TestValidationSectionIsStrict checks an unknown key, a missing rule and a wrong
// shape are refused with the loader's usual problems, and that the section may be
// absent or empty.
func TestValidationSectionIsStrict(t *testing.T) {
	for name, section := range map[string]string{
		"unknown key in section":  "validation:\n  ignore: []\n",
		"unknown key in entry":    "validation:\n  suppress:\n    - { rule: V-TOPO-003, because: x }\n",
		"entry without a rule":    "validation:\n  suppress:\n    - { subject: dgx-spark, reason: x }\n",
		"entry that is not a map": "validation:\n  suppress:\n    - V-TOPO-003\n",
	} {
		if _, err := topology.Decode("t.yaml", withValidation(t, section), 0); !types.HasCode(err, types.ErrInvalidTopology) {
			t.Errorf("%s: %v, want INVALID_TOPOLOGY", name, err)
		}
	}
	for _, ok := range []string{"validation: {}\n", "validation:\n  suppress: []\n"} {
		if top, err := topology.Decode("t.yaml", withValidation(t, ok), 0); err != nil || len(top.Validation.Suppress) != 0 {
			t.Errorf("%q: %v", strings.TrimSpace(ok), err)
		}
	}
}
