package tests

import (
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// healthyDirect reports every link as up, with the evidence the golden text
// shows for the direct link.
func healthyDirect(link types.LinkID) (bool, string) { return true, "probe ok, 3/3" }

// P1-ROUTE-032
//
// TestExplainGolden checks the explanation of builder-1 reaching 10.42.33.7 in the
// healthy three-site network against the reviewed text in
// testdata/golden/explain-builder-1-10.42.33.7.txt. A change to the wording is
// a change to that file, which is what makes it reviewable.
func TestExplainGolden(t *testing.T) {
	want, err := os.ReadFile("../../testdata/golden/explain-builder-1-10.42.33.7.txt")
	if err != nil {
		t.Fatalf("reading the golden file: %v", err)
	}
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	got := route.Explain(result.Tables["builder-1"], addrOf("10.42.33.7"),
		route.Notes{Owner: "dgx-spark, site dgx", Health: healthyDirect})
	if got.String() != string(want) {
		t.Errorf("explanation differs from the golden file\n got:\n%s\nwant:\n%s", got.String(), want)
	}
	again := route.Explain(result.Tables["builder-1"], addrOf("10.42.33.7"),
		route.Notes{Owner: "dgx-spark, site dgx", Health: healthyDirect})
	if again.String() != got.String() {
		t.Error("the same question gave two different explanations")
	}
}

// TestExplainFieldsMatchTheText checks the structured form: the match is the
// route Lookup returns, the backup is the inactive one, and the candidate count
// covers every route that contains the address.
func TestExplainFieldsMatchTheText(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	explanation := route.Explain(result.Tables["builder-1"], addrOf("10.42.33.7"), route.Notes{})
	if explanation.Node != "builder-1" || explanation.Candidates != 2 {
		t.Errorf("node %s with %d candidates, want builder-1 with 2", explanation.Node, explanation.Candidates)
	}
	if explanation.Match == nil || explanation.Match.NextHop != "dgx-spark" || !explanation.Match.Active {
		t.Errorf("match = %+v, want the active route via dgx-spark", explanation.Match)
	}
	if explanation.Backup == nil || explanation.Backup.NextHop != "aws-hub" || explanation.Backup.Active {
		t.Errorf("backup = %+v, want the inactive route via aws-hub", explanation.Backup)
	}
	if explanation.Replaced != nil {
		t.Errorf("replaced = %+v, want none", explanation.Replaced)
	}
}

// TestExplainFailedOver checks the explanation after the direct link fails: the
// route goes through the hub, there is no backup, and a health report of
// "down" is shown as such.
func TestExplainFailedOver(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSiteDirectDown(), fixture.ThreeSiteInput())
	down := func(link types.LinkID) (bool, string) {
		return link != "builder-1~dgx-spark", "no handshake for 3 probes"
	}
	text := route.Explain(result.Tables["builder-1"], addrOf("10.42.33.7"), route.Notes{Health: down}).String()
	for _, want := range []string{
		"active:  via aws-hub over aws-hub~builder-1, metric 40, path builder-1 → aws-hub → dgx-spark",
		"backup:  none",
		"link aws-hub~builder-1 is up (no handshake for 3 probes)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("explanation lacks %q:\n%s", want, text)
		}
	}
	downText := route.Explain(result.Tables["builder-1"], addrOf("10.42.33.7"), route.Notes{
		Health: func(types.LinkID) (bool, string) { return false, "timeout" },
	}).String()
	if !strings.Contains(downText, "is DOWN, packets on it are lost (timeout)") {
		t.Errorf("a down link is not called out:\n%s", downText)
	}
}

// TestExplainNoRoute checks an address nothing covers: the explanation says so
// and calls it a black hole, instead of inventing a route.
func TestExplainNoRoute(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	explanation := route.Explain(result.Tables["builder-1"], addrOf("192.168.1.1"), route.Notes{})
	want := "builder-1 → 192.168.1.1\n" +
		"   match:   none\n" +
		"   active:  none, the packet would be dropped (black hole)\n" +
		"   backup:  none\n" +
		"   reason:  no active route covers 192.168.1.1, so the packet is dropped\n"
	if explanation.String() != want || explanation.Match != nil {
		t.Errorf("explanation\n got:\n%s\nwant:\n%s", explanation.String(), want)
	}
}

// TestExplainConnectedAndOnlyCandidate checks a local address: the reason is
// that it is on the node, and a single candidate is worded as "the only
// candidate".
func TestExplainConnectedAndOnlyCandidate(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	text := route.Explain(result.Tables["builder-1"], addrOf("10.42.16.9"), route.Notes{}).String()
	for _, want := range []string{
		"match:   10.42.16.0/20 (the only candidate)",
		"active:  local, on this node",
		"reason:  10.42.16.0/20 is on this node",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("explanation lacks %q:\n%s", want, text)
		}
	}
}

// TestExplainFlagsStaticOverrides checks that an override is always flagged and
// that the computed route it replaced is named.
func TestExplainFlagsStaticOverrides(t *testing.T) {
	input := fixture.ThreeSiteInput()
	input.Overrides = []route.Override{{Node: "dgx-spark", Prefix: prefixOf("10.42.16.0/20"), NextHop: "builder-1"}}
	result := mustCompute(t, fixture.ThreeSite(true), input)
	explanation := route.Explain(result.Tables["dgx-spark"], addrOf("10.42.16.9"), route.Notes{})
	text := explanation.String()
	for _, want := range []string{
		"active:  via builder-1 over builder-1~dgx-spark, metric 10, path dgx-spark → builder-1 [static override]",
		"replaced: via builder-1 over builder-1~dgx-spark, metric 10, path dgx-spark → builder-1 (computed",
		"a static override set by an operator; it does not follow link health; link health is not known",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("explanation lacks %q:\n%s", want, text)
		}
	}
	if explanation.Replaced == nil || explanation.Replaced.Source != route.SourceComputed || explanation.Replaced.Active {
		t.Errorf("replaced = %+v, want the inactive computed route", explanation.Replaced)
	}
	if explanation.Backup == nil || explanation.Backup.Source != route.SourceBackup {
		t.Errorf("backup = %+v, want the backup through aws-hub", explanation.Backup)
	}
}

// TestExplainWithoutHealthSaysSo checks that a missing health callback is
// stated, not hidden, and that a nil table is handled.
func TestExplainWithoutHealthSaysSo(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	text := route.Explain(result.Tables["builder-1"], addrOf("10.42.33.7"), route.Notes{}).String()
	if !strings.Contains(text, "link health is not known") {
		t.Errorf("a missing health callback is not mentioned:\n%s", text)
	}
	explanation := route.Explain(nil, addrOf("10.42.33.7"), route.Notes{})
	if explanation.Match != nil || !strings.Contains(explanation.Reason, "no table") {
		t.Errorf("nil table gave %+v", explanation)
	}
}

// TestExplainDoesNotChangeTheTable checks that explaining leaves the table as it
// was, in content and in order.
func TestExplainDoesNotChangeTheTable(t *testing.T) {
	result := mustCompute(t, fixture.ThreeSite(true), fixture.ThreeSiteInput())
	table := result.Tables["builder-1"]
	before := routesOf(table)
	route.Explain(table, addrOf("10.42.33.7"), route.Notes{Health: healthyDirect})
	for i, r := range table.Routes {
		if route.Compare(r, before[i]) != 0 {
			t.Fatalf("route %d changed", i)
		}
	}
}

// TestExplainMatchesLookup checks, on 20,000 random tables and addresses, that
// the explanation's match is exactly what Lookup returns, that the candidate
// count is the number of routes containing the address, and that a backup, when
// present, is inactive and has the match's prefix.
func TestExplainMatchesLookup(t *testing.T) {
	rng := rand.New(rand.NewPCG(36, 1))
	for i := 0; i < 20000; i++ {
		table := randomTable(rng)
		dst := randomDestination(rng, table)
		explanation := route.Explain(table, dst, route.Notes{})
		found, ok := table.Lookup(dst)
		if (explanation.Match != nil) != ok || (ok && route.Compare(*explanation.Match, found) != 0) {
			t.Fatalf("case %d: match %+v, Lookup %+v (found %v)", i, explanation.Match, found, ok)
		}
		covering := 0
		for _, r := range table.Routes {
			if r.Prefix.Contains(dst) {
				covering++
			}
		}
		if explanation.Candidates != covering {
			t.Fatalf("case %d: %d candidates, want %d", i, explanation.Candidates, covering)
		}
		if explanation.Backup != nil && (explanation.Backup.Active || ok && explanation.Backup.Prefix != found.Prefix) {
			t.Fatalf("case %d: bad backup %+v for match %+v", i, explanation.Backup, found)
		}
		_ = explanation.String()
	}
}

// TestExplainShowsTheBestBackup checks that when a table holds several inactive
// backups for the matched prefix, the one that sorts first, the cheapest, is the
// one shown.
func TestExplainShowsTheBestBackup(t *testing.T) {
	primary := computed("10.1.0.0/24", "hub-a", "a~hub-a", 10)
	primary.Path = []types.NodeID{"a", "hub-a", "b"}
	dear := computed("10.1.0.0/24", "hub-c", "a~hub-c", 50)
	dear.Source, dear.Active = route.SourceBackup, false
	cheap := computed("10.1.0.0/24", "hub-b", "a~hub-b", 30)
	cheap.Source, cheap.Active = route.SourceBackup, false
	for _, order := range [][]route.Route{{primary, dear, cheap}, {cheap, dear, primary}} {
		table := &route.Table{Node: "a", Routes: order}
		explanation := route.Explain(table, addrOf("10.1.0.9"), route.Notes{})
		if explanation.Backup == nil || explanation.Backup.NextHop != "hub-b" {
			t.Errorf("backup = %+v, want the cheaper one via hub-b", explanation.Backup)
		}
	}
}
