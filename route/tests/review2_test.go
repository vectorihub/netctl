package tests

import (
	"math"
	"net/netip"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// TestDuplicateOverrideSpellingIsDecidedTotally gives two overrides for the same
// block, spelled 10.9.0.0/24 and 10.9.0.5/24 and otherwise identical, in both
// orders, and checks the same spelling is accepted and the same one rejected each
// time: the sort that decides between them is total.
func TestDuplicateOverrideSpellingIsDecidedTotally(t *testing.T) {
	plain := route.Override{Node: "builder-1", Prefix: netip.MustParsePrefix("10.9.0.0/24"), NextHop: "aws-hub"}
	odd := route.Override{Node: "builder-1", Prefix: netip.MustParsePrefix("10.9.0.5/24"), NextHop: "aws-hub"}
	var rejected []netip.Prefix
	for _, order := range [][]route.Override{{plain, odd}, {odd, plain}} {
		input := fixture.ThreeSiteInput()
		input.Overrides = order
		result, err := route.Compute(fixture.ThreeSite(true), input)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rejected) != 1 || result.Rejected[0].Reason != route.RejectDuplicate {
			t.Fatalf("rejected %+v, want one duplicate", result.Rejected)
		}
		rejected = append(rejected, result.Rejected[0].Override.Prefix)
	}
	if rejected[0] != rejected[1] {
		t.Errorf("the rejected spelling depends on the input order: %v then %v", rejected[0], rejected[1])
	}
}

// TestCollectNilRegistryHasACode checks Collect with no registry returns a coded
// error, found with errors.Is, and nothing else.
func TestCollectNilRegistryHasACode(t *testing.T) {
	owned, local, err := route.Collect(nil, nil, nil)
	if !types.HasCode(err, types.ErrInvalidValue) || owned != nil || local != nil {
		t.Errorf("Collect(nil) = %v, %v, %v; want INVALID_VALUE and nothing", owned, local, err)
	}
}

// TestComputeRefusesCostsThatCouldOverflow checks two links of cost 1,500,000,000,
// whose path would not fit a 32-bit int, are refused with INVALID_TOPOLOGY
// instead of producing a metric that wrapped round to a negative number, and that
// costs adding up to exactly MaxTotalCost are accepted.
func TestComputeRefusesCostsThatCouldOverflow(t *testing.T) {
	g := fixture.NewGraph().Hub("h").Spoke("a", "b")
	g.Link("h", "a", 1_500_000_000)
	g.Link("h", "b", 1_500_000_000)
	if _, err := route.Compute(g, route.Input{}); !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("Compute with costs that could overflow: %v, want INVALID_TOPOLOGY", err)
	}
	ok := fixture.NewGraph().Hub("h").Spoke("a")
	ok.Link("h", "a", route.MaxTotalCost/2) // two edges, one each way
	if _, err := route.Compute(ok, route.Input{}); err != nil {
		t.Errorf("costs adding up to at most MaxTotalCost were refused: %v", err)
	}
}

// TestSPFNeverReturnsANegativeCost hands SPF, which returns no error, a graph whose
// path cost exceeds the largest int, and checks the cost stops at math.MaxInt
// instead of wrapping round to a negative number.
func TestSPFNeverReturnsANegativeCost(t *testing.T) {
	g := fixture.NewGraph().Hub("h").Spoke("a", "b")
	g.Link("h", "a", math.MaxInt/2+1)
	g.Link("h", "b", math.MaxInt/2+1)
	best, ok := route.SPF(g, "a", "")["b"]
	if !ok || best.Cost < 0 || best.Cost != math.MaxInt {
		t.Errorf("SPF cost to b = %d (found %v), want math.MaxInt, never negative", best.Cost, ok)
	}
}
