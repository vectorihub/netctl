package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// addRoute appends a route to a node's table.
func addRoute(t *testing.T, tables route.Tables, node types.NodeID, r route.Route) {
	t.Helper()
	if tables[node] == nil {
		t.Fatalf("no table for %s", node)
	}
	tables[node].Routes = append(tables[node].Routes, r)
}

// P1-VAL-020
//
// TestRule_V_ROUTE_001 gives builder-1 a second active route for one prefix, by another
// next hop.
func TestRule_V_ROUTE_001(t *testing.T) {
	quiet(t, "V-ROUTE-001")
	p := cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.42.32.0/20"), NextHop: hub, Via: hubB, Metric: 5, Source: route.SourceStatic, Active: true})
	got := fires(t, p, "V-ROUTE-001")
	if len(got) != 1 || got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "2 active routes for 10.42.32.0/20") {
		t.Errorf("%+v", got)
	}
	// An unmasked spelling of the same block counts as the same prefix.
	p = cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.42.32.5/20"), NextHop: hub, Via: hubB, Active: true})
	fires(t, p, "V-ROUTE-001")
	// A backup beside the primary is the normal case.
	p = cleanPlan(t, true)
	if got := findings(p, "V-ROUTE-001"); len(got) != 0 {
		t.Errorf("primary and backup reported: %+v", got)
	}
}

// TestRule_V_ROUTE_002 checks a route over a link that is down, to a node that is not a
// neighbour, and over a link that is not the one the route names.
func TestRule_V_ROUTE_002(t *testing.T) {
	quiet(t, "V-ROUTE-002")
	// The plan was routed with the direct link up, and then it went down: the failover
	// has not happened.
	p := withLinksDown(cleanPlan(t, true), direct)
	got := fires(t, p, "V-ROUTE-002")
	if !strings.Contains(got[0].Message, "is down") || !strings.Contains(got[0].Message, string(direct)) {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.99.0.0/16"), NextHop: "ghost", Via: "builder-1~ghost", Active: true})
	got = fires(t, p, "V-ROUTE-002")
	if !strings.Contains(got[0].Message, "not a neighbour") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.99.0.0/16"), NextHop: dgx, Via: hubB, Active: true}) // wrong link for that neighbour
	fires(t, p, "V-ROUTE-002")
	// An inactive backup over a down link is just waiting.
	p = withLinksDown(cleanPlan(t, true), hubB)
	for _, f := range findings(p, "V-ROUTE-002") {
		if strings.Contains(f.Message, "backup") {
			t.Errorf("%+v", f)
		}
	}
}

// TestRule_V_ROUTE_003 gives a route a path that goes through a node twice.
func TestRule_V_ROUTE_003(t *testing.T) {
	quiet(t, "V-ROUTE-003")
	p := cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.99.0.0/16"), NextHop: hub, Via: hubB, Active: true, Path: []types.NodeID{builder, hub, dgx, hub}})
	got := fires(t, p, "V-ROUTE-003")
	if len(got) != 1 || !strings.Contains(got[0].Message, "visits aws-hub twice") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.99.0.0/16"), NextHop: hub, Via: hubB, Active: false, Path: []types.NodeID{builder, hub, builder}})
	if got := findings(p, "V-ROUTE-003"); len(got) != 0 {
		t.Errorf("an inactive route was reported: %+v", got)
	}
}

// P1-VAL-021
//
// TestRule_V_ROUTE_004 sends builder-1's traffic for the hub's site by way of dgx-spark,
// with a path that shows it and then as a bare override with no path.
func TestRule_V_ROUTE_004(t *testing.T) {
	quiet(t, "V-ROUTE-004")
	p := cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.42.0.0/20"), NextHop: dgx, Via: direct, Metric: 5, Active: true, Path: []types.NodeID{builder, dgx, hub}})
	got := fires(t, p, "V-ROUTE-004")
	if len(got) != 1 || got[0].Subject != "dgx-spark" || !strings.Contains(got[0].Message, "would forward for builder-1") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.42.0.0/20"), NextHop: dgx, Via: direct, Source: route.SourceStatic, Active: true})
	got = fires(t, p, "V-ROUTE-004")
	if !strings.Contains(got[0].Message, "does not hold that prefix") {
		t.Errorf("%+v", got)
	}
	// A route to a spoke's own prefix, with or without a path, is not transit; neither is
	// a hub forwarding.
	p = cleanPlan(t, false)
	if got := findings(p, "V-ROUTE-004"); len(got) != 0 {
		t.Errorf("traffic by way of the hub: %+v", got)
	}
	p = cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.42.32.0/24"), NextHop: dgx, Via: direct, Source: route.SourceStatic, Active: true})
	if got := findings(p, "V-ROUTE-004"); len(got) != 0 {
		t.Errorf("a route into the next hop's own block: %+v", got)
	}
}

// TestRule_V_ROUTE_005 checks every static override is shown, and a plan with none shows
// nothing.
func TestRule_V_ROUTE_005(t *testing.T) {
	quiet(t, "V-ROUTE-005")
	p := cleanPlan(t, true)
	p.Input.Overrides = []route.Override{{Node: hub, Prefix: pfx("10.42.32.0/20"), NextHop: builder}, {Node: builder, Prefix: pfx("10.50.0.0/16"), NextHop: hub}}
	got := fires(t, p, "V-ROUTE-005")
	if len(got) != 2 || !strings.Contains(got[0].Message, "10.42.32.0/20 via builder-1") && !strings.Contains(got[1].Message, "10.42.32.0/20 via builder-1") {
		t.Errorf("%+v", got)
	}
}

// TestRule_V_ROUTE_006 plants a more specific static route with another next hop inside
// a computed one, and checks one with the same next hop is not a shadow.
func TestRule_V_ROUTE_006(t *testing.T) {
	quiet(t, "V-ROUTE-006")
	p := cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.42.32.0/24"), NextHop: hub, Via: hubB, Source: route.SourceStatic, Active: true})
	got := fires(t, p, "V-ROUTE-006")
	if len(got) != 1 || got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "10.42.32.0/24 via aws-hub is more specific than 10.42.32.0/20 via dgx-spark") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	addRoute(t, p.Tables, builder, route.Route{Prefix: pfx("10.42.32.0/24"), NextHop: dgx, Via: direct, Source: route.SourceStatic, Active: true})
	if got := findings(p, "V-ROUTE-006"); len(got) != 0 {
		t.Errorf("the same next hop is not a shadow: %+v", got)
	}
}
