package tests

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// TestEnumErrorsCarryTheInvalidValueCode checks the three enums of the package
// (hop result, verdict, flow family) refuse a bad name or an unset value with an
// error carrying INVALID_VALUE, found with errors.Is, directly and through
// encoding/json.
func TestEnumErrorsCarryTheInvalidValueCode(t *testing.T) {
	want := &types.Error{Code: types.ErrInvalidValue}
	var hop sim.HopResult
	var verdict sim.Verdict
	var family sim.Family
	for name, err := range map[string]error{
		"hop unmarshal":     hop.UnmarshalText([]byte("nope")),
		"verdict unmarshal": verdict.UnmarshalText([]byte("nope")),
		"family unmarshal":  family.UnmarshalText([]byte("nope")),
		"hop json":          json.Unmarshal([]byte(`"nope"`), &hop),
		"verdict json":      json.Unmarshal([]byte(`"nope"`), &verdict),
		"family json":       json.Unmarshal([]byte(`"nope"`), &family),
	} {
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want INVALID_VALUE", name, err)
		}
	}
	_, hopErr := sim.HopUnknown.MarshalText()
	_, verdictErr := sim.VerdictUnknown.MarshalText()
	_, familyErr := sim.FamilyUnknown.MarshalText()
	_, jsonErr := json.Marshal(sim.Flow{Label: "x"})
	for name, err := range map[string]error{"hop": hopErr, "verdict": verdictErr, "family": familyErr, "flow json": jsonErr} {
		if !errors.Is(err, want) {
			t.Errorf("marshal %s: %v, want INVALID_VALUE", name, err)
		}
	}
}

// TestFlowsRefusesNilInputWithACode checks Flows with no topology or no address
// plan returns a coded error and no flows, not a bare message.
func TestFlowsRefusesNilInputWithACode(t *testing.T) {
	flows, err := sim.Flows(nil, nil, sim.Options{})
	if !types.HasCode(err, types.ErrInvalidValue) || flows != nil {
		t.Errorf("Flows(nil, nil) = %v, %v; want INVALID_VALUE and no flows", flows, err)
	}
}
