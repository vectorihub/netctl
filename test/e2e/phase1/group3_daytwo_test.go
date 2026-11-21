package phase1

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl"
	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// init registers Group 3, day-two changes: adding, removing and changing things touches only
// what it should, machines keep their addresses, and released addresses are not reused early.
// Every test starts from the healthy baseline and edits a copy of the topology, planning with
// the previous plan's address plan, health and plan in force, as a user would.
func init() {
	register("P1-E2E-040", e2e040AddSite)
	register("P1-E2E-041", e2e041AddDirectLink)
	register("P1-E2E-042", e2e042RemoveNode)
	register("P1-E2E-043", e2e043ReaddInQuarantine)
	register("P1-E2E-044", e2e044ReaddAfterQuarantine)
	register("P1-E2E-045", e2e045Rename)
	register("P1-E2E-046", e2e046Cost)
	register("P1-E2E-047", e2e047FillSites)
	register("P1-E2E-048", e2e048GrowthPool)
}

// baselineTopo builds the healthy baseline and copies its topology to topo.yaml, which the
// test then edits.
func baselineTopo(w *e2e.W) *e2e.Out {
	w.T.Helper()
	_, base := e2e.Baseline(w)
	w.Write("topo.yaml", w.Read("three-site.yaml"))
	return base
}

// addNode adds a spoke and its site, with a hub-spoke link, to topo.yaml.
func addNode(w *e2e.W, node, site string) {
	w.T.Helper()
	w.Edit("topo.yaml",
		e2e.Append("sites", map[string]any{"id": site, "provider": "owned"}),
		e2e.Append("nodes", map[string]any{"id": node, "site": site, "role": "spoke", "nat": "behind", "advertises": []string{"site"}}),
		e2e.Append("links", map[string]any{"kind": "hub-spoke", "a": "aws-hub", "b": node}),
	)
}

// dropNode removes the Mac Studio, its link and its site from topo.yaml.
func dropNode(w *e2e.W, node, site string) {
	w.T.Helper()
	w.Edit("topo.yaml", e2e.Delete("nodes[id="+node+"]"), e2e.Delete("links[id=aws-hub~"+node+"]"), e2e.Delete("sites[id="+site+"]"))
}

// plan plans topo.yaml following another plan.
func plan(w *e2e.W, from *e2e.Out, name string, signals *e2e.Signals) *e2e.Out {
	w.T.Helper()
	o := e2e.Options{Topology: w.Load("topo.yaml"), From: from, Name: name}
	if signals != nil {
		o.Signals = signals.List()
	}
	return w.Plan(o)
}

// owner finds the allocation an owner holds in a plan's address plan.
func owner(out *e2e.Out, o ipam.Owner) (ipam.Allocation, bool) {
	for _, a := range out.Result.IPAM.Allocations {
		if a.Owner == o && a.State != ipam.Quarantined {
			return a, true
		}
	}
	return ipam.Allocation{}, false
}

// expectAddress fails unless the owner holds exactly the prefix in the plan.
func expectAddress(w *e2e.W, out *e2e.Out, o ipam.Owner, want string) {
	w.T.Helper()
	a, ok := owner(out, o)
	if !ok || a.Prefix.String() != want {
		w.Fatalf("%s: %s holds %v, want %s", out.Name, o, a.Prefix, want)
	}
}

// expectKept fails unless every allocation of the earlier plan is in the later one with the
// same prefix, owner, pool and state (addresses never move).
func expectKept(w *e2e.W, before, after *e2e.Out) {
	w.T.Helper()
	for _, a := range before.Result.IPAM.Allocations {
		if a.State != ipam.Allocated {
			continue
		}
		if !slices.ContainsFunc(after.Result.IPAM.Allocations, func(b ipam.Allocation) bool {
			return b.Prefix == a.Prefix && b.Owner == a.Owner && b.Pool == a.Pool && b.State == a.State
		}) {
			w.Fatalf("%s lost %s %s of %s", after.Name, a.Owner, a.Prefix, before.Name)
		}
	}
}

// peerNames lists the peers of a node in a rendered config, sorted.
func peerNames(w *e2e.W, out *e2e.Out, node string) []string {
	w.T.Helper()
	j := w.JSON(out, "nodes/"+node+".json")
	var names []string
	for i := 0; i < j.Len("peers"); i++ {
		names = append(names, j.Str(fmt.Sprintf("peers.%d.node", i)))
	}
	slices.Sort(names)
	return names
}

// gained fails unless the peer's allowed prefixes in the later plan are those of the earlier
// plan plus exactly the extra prefixes.
func gained(w *e2e.W, before, after *e2e.Out, node, peer string, extra ...string) {
	w.T.Helper()
	path := "peers[node=" + peer + "].allowed_prefixes"
	want := append(w.JSON(before, "nodes/"+node+".json").Strings(path), extra...)
	slices.Sort(want)
	got := w.JSON(after, "nodes/"+node+".json").Strings(path)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		w.Fatalf("%s's %s peer allows %v, want %v", node, peer, got, want)
	}
}

// e2e040AddSite is P1-E2E-040: a fourth site (the Mac Studio). Existing allocations never
// move, the accounting still adds up (AC2), and only what the new site needs changes.
func e2e040AddSite(w *e2e.W) {
	base := baselineTopo(w)
	addNode(w, "mac-studio", "studio")
	w.Step("add the Mac Studio")
	out := plan(w, base, "out-2", nil)
	w.ExpectOK(out)
	expectAddress(w, out, ipam.Site("studio"), "10.42.48.0/20")
	expectAddress(w, out, ipam.Node("mac-studio"), "10.42.240.4/32")
	expectKept(w, base, out)
	rep := w.Accounting(out)
	if rep.Allocated != 16392 || rep.Free != 49144 || rep.Allocated+rep.Free != rep.Total {
		w.Fatalf("accounting: allocated %d free %d", rep.Allocated, rep.Free)
	}
	if got := peerNames(w, out, "mac-studio"); !slices.Equal(got, []string{"aws-hub"}) {
		w.Fatalf("mac-studio's peers are %v, want only the hub", got)
	}
	if got := w.JSON(out, "nodes/mac-studio.json").Strings("peers[node=aws-hub].allowed_prefixes"); !slices.Equal(got, []string{"10.42.0.0/20", "10.42.16.0/20", "10.42.32.0/20", "10.42.240.1/32", "10.42.240.2/32", "10.42.240.3/32"}) {
		w.Fatalf("mac-studio's hub peer allows %v", got)
	}
	w.Step("what changed on the existing nodes")
	if got := peerNames(w, out, "aws-hub"); !slices.Equal(got, []string{"builder-1", "dgx-spark", "mac-studio"}) {
		w.Fatalf("the hub's peers are %v", got)
	}
	if got := w.JSON(out, "nodes/aws-hub.json").Strings("peers[node=mac-studio].allowed_prefixes"); !slices.Equal(got, []string{"10.42.48.0/20", "10.42.240.4/32"}) {
		w.Fatalf("the hub's mac-studio peer allows %v", got)
	}
	for _, n := range []string{"builder-1", "dgx-spark"} {
		gained(w, base, out, n, "aws-hub", "10.42.48.0/20", "10.42.240.4/32")
		if via := w.JSON(out, "nodes/"+n+".json").Str("routes[prefix=10.42.48.0/20].via"); via != "aws-hub" {
			w.Fatalf("%s routes the new site via %q", n, via)
		}
	}
	expectDiffNodes(w, base, out, "aws-hub", "builder-1", "dgx-spark")
	if added := netctl.Diff(base.Result, out.Result).NodesAdded; len(added) != 1 || added[0] != "mac-studio" {
		w.Fatalf("nodes added: %v, want only mac-studio", added)
	}
	w.ExpectSimPass(out, 29)
	w.ExpectOracleOK(out)
}

// e2e041AddDirectLink is P1-E2E-041: a new direct link between two spokes is used only once
// it is proven healthy, and adding it never disturbs the hub.
func e2e041AddDirectLink(w *e2e.W) {
	base := baselineTopo(w)
	addNode(w, "mac-studio", "studio")
	out2 := plan(w, base, "out-2", nil)
	w.ExpectOK(out2)
	w.Step("add the direct link")
	w.Edit("topo.yaml", e2e.Append("links", map[string]any{"kind": "direct", "a": "builder-1", "b": "mac-studio", "cost": 10, "health": "required"}))
	out3 := plan(w, out2, "out-3", nil)
	w.ExpectOK(out3)
	expectAddress(w, out3, ipam.Link("builder-1~mac-studio"), "10.42.252.4/30")
	if got := w.JSON(out3, "nodes/builder-1.json").Strings("peers[node=mac-studio].allowed_prefixes"); !slices.Equal(got, []string{"10.42.252.6/32"}) {
		w.Fatalf("builder-1's mac-studio peer allows %v, want only the probe address", got)
	}
	if got := w.JSON(out3, "nodes/mac-studio.json").Strings("peers[node=builder-1].allowed_prefixes"); !slices.Equal(got, []string{"10.42.252.5/32"}) {
		w.Fatalf("mac-studio's builder-1 peer allows %v", got)
	}
	if via := w.JSON(out3, "nodes/builder-1.json").Str("routes[prefix=10.42.48.0/20].via"); via != "aws-hub" {
		w.Fatalf("the unproven link carries traffic: builder-1 routes the studio via %q", via)
	}
	w.Step("prove the new link healthy")
	w.At(time.Minute)
	proof := new(e2e.Signals).Probes("builder-1~mac-studio", true, 5, 35*time.Second, 5*time.Second)
	out4 := plan(w, out3, "out-4", proof)
	w.ExpectOK(out4)
	b := w.JSON(out4, "nodes/builder-1.json")
	for _, p := range []string{"10.42.48.0/20", "10.42.240.4/32"} {
		sel := "routes[prefix=" + p + "]"
		if b.Str(sel+".via") != "mac-studio" || b.Num(sel+".metric") != 10 {
			w.Fatalf("builder-1 reaches %s via %s metric %v, want mac-studio, 10", p, b.Str(sel+".via"), b.Num(sel+".metric"))
		}
	}
	if path := w.OraclePath(out4, "builder-1", "mac-studio"); len(path.Hops) != 2 {
		w.Fatalf("builder-1 to mac-studio goes %v, want direct", path.Hops)
	}
	for _, o := range []*e2e.Out{out3, out4} {
		expectSameConfig(w, out2, o, "aws-hub")
	}
	w.ExpectFinding(out4, "V-TOPO-004", "warning", "builder-1~mac-studio")
	w.ExpectSimPass(out4, -1)
	w.ExpectOracleOK(out4)
}

// removed builds the end state of P1-E2E-042: the Mac Studio added and then removed again.
func removed(w *e2e.W) (base, added, gone *e2e.Out) {
	w.T.Helper()
	base = baselineTopo(w)
	addNode(w, "mac-studio", "studio")
	added = plan(w, base, "out-2", nil)
	w.ExpectOK(added)
	w.Step("remove the Mac Studio")
	dropNode(w, "mac-studio", "studio")
	gone = plan(w, added, "out-3", nil)
	w.ExpectOK(gone)
	return base, added, gone
}

// e2e042RemoveNode is P1-E2E-042: removing a node is clean. Its addresses are quarantined, not
// freed, and the rest of the network returns exactly to its earlier state.
func e2e042RemoveNode(w *e2e.W) {
	base, _, gone := removed(w)
	w.Step("the released addresses are quarantined")
	if _, ok := gone.Files["nodes/mac-studio.json"]; ok {
		w.Fatalf("a config was written for the removed node")
	}
	until := e2e.T0.Add(30*time.Second + 24*time.Hour)
	quarantined := map[string]bool{}
	for _, a := range gone.Result.IPAM.Allocations {
		if a.State == ipam.Quarantined {
			quarantined[a.Prefix.String()] = a.QuarantineUntil.Equal(until)
		}
	}
	if !quarantined["10.42.240.4/32"] || !quarantined["10.42.48.0/20"] || len(quarantined) != 2 {
		w.Fatalf("quarantined (until %v): %v", until, quarantined)
	}
	rep := w.Accounting(gone)
	if rep.Allocated != 12295 || rep.Quarantined != 4097 || rep.Free != 49144 {
		w.Fatalf("accounting: allocated %d quarantined %d free %d", rep.Allocated, rep.Quarantined, rep.Free)
	}
	if gone.Result.PlanID != base.Result.PlanID || gone.SubtreeHash("nodes/") != base.SubtreeHash("nodes/") {
		w.Fatalf("the network did not return to the baseline: %s and %s", gone.Result.PlanID, base.Result.PlanID)
	}
	w.ExpectIPAMOK(gone)
}

// e2e043ReaddInQuarantine is P1-E2E-043: while the old addresses are quarantined a new node
// does not get them.
func e2e043ReaddInQuarantine(w *e2e.W) {
	_, _, gone := removed(w)
	w.Step("add a new node while the old addresses are quarantined")
	addNode(w, "mac-studio-2", "studio-2")
	out := plan(w, gone, "out-4", nil)
	w.ExpectOK(out)
	expectAddress(w, out, ipam.Node("mac-studio-2"), "10.42.240.5/32")
	expectAddress(w, out, ipam.Site("studio-2"), "10.42.64.0/20")
	for _, a := range gone.Result.IPAM.Allocations {
		if a.State == ipam.Quarantined && !slices.ContainsFunc(out.Result.IPAM.Allocations, func(b ipam.Allocation) bool {
			return b.Prefix == a.Prefix && b.State == ipam.Quarantined && b.QuarantineUntil.Equal(a.QuarantineUntil)
		}) {
			w.Fatalf("the quarantined %s changed", a.Prefix)
		}
	}
}

// e2e044ReaddAfterQuarantine is P1-E2E-044: once the quarantine has run out the lowest free
// address is used again.
func e2e044ReaddAfterQuarantine(w *e2e.W) {
	_, _, gone := removed(w)
	w.Step("add a new node a day and an hour later")
	w.Advance(25 * time.Hour)
	addNode(w, "mac-studio-2", "studio-2")
	out := plan(w, gone, "out-4", nil)
	w.ExpectOK(out)
	expectAddress(w, out, ipam.Node("mac-studio-2"), "10.42.240.4/32")
	expectAddress(w, out, ipam.Site("studio-2"), "10.42.48.0/20")
	for _, a := range out.Result.IPAM.Allocations {
		if a.State == ipam.Quarantined {
			w.Fatalf("%s is still quarantined", a.Prefix)
		}
	}
}

// e2e045Rename is P1-E2E-045: a rename is a removal and an addition. The new name gets a new
// address, the old one is quarantined, and the plan says so.
func e2e045Rename(w *e2e.W) {
	base := baselineTopo(w)
	w.Step("rename builder-1 to builder-01 everywhere")
	w.RewriteAll("topo.yaml", "builder-1", "builder-01")
	out := plan(w, base, "out-2", nil)
	w.ExpectOK(out)
	expectAddress(w, out, ipam.Node("builder-01"), "10.42.240.4/32")
	expectAddress(w, out, ipam.Site("builder"), "10.42.16.0/20")
	expectAddress(w, out, ipam.Link("builder-01~dgx-spark"), "10.42.252.4/30")
	quarantined := map[string]bool{}
	for _, a := range out.Result.IPAM.Allocations {
		if a.State == ipam.Quarantined {
			quarantined[a.Prefix.String()] = true
		}
	}
	if !quarantined["10.42.240.2/32"] || !quarantined["10.42.252.0/30"] {
		w.Fatalf("quarantined: %v, want the old overlay address and transit block", quarantined)
	}
	if got := w.JSON(out, "nodes/builder-01.json").Str("routes[prefix=10.42.32.0/20].via"); got != "aws-hub" {
		w.Fatalf("the renamed node's new direct link is trusted: it routes dgx via %q", got)
	}
	var kinds []string
	for _, c := range out.Result.Changes {
		kinds = append(kinds, c.String())
	}
	for _, want := range []string{"released node:builder-1 10.42.240.2/32", "allocated node:builder-01 10.42.240.4/32"} {
		if !slices.Contains(kinds, want) {
			w.Fatalf("the plan does not list %q: %v", want, kinds)
		}
	}
	noted := false
	for _, d := range out.Result.Decisions {
		if d.Kind == "AddressNotCarried" {
			noted = true
		}
	}
	if !noted {
		w.Fatalf("no AddressNotCarried decision: %v", out.Result.Decisions)
	}
}

// e2e046Cost is P1-E2E-046: raising the cost of the direct link above the hub path makes the
// hub the preferred path, the direct link a backup, and the validator says it is unhelpful.
func e2e046Cost(w *e2e.W) {
	base := baselineTopo(w)
	w.Step("raise the direct link's cost to 50")
	w.Edit("topo.yaml", e2e.Set("links[id=builder-1~dgx-spark].cost", 50))
	out := plan(w, base, "out-2", nil)
	w.ExpectOK(out)
	for _, c := range []struct{ node, prefix, other string }{{"builder-1", "10.42.32.0/20", "dgx-spark"}, {"builder-1", "10.42.240.3/32", "dgx-spark"}, {"dgx-spark", "10.42.16.0/20", "builder-1"}, {"dgx-spark", "10.42.240.2/32", "builder-1"}} {
		j := w.JSON(out, "nodes/"+c.node+".json")
		var active, backup string
		var backupMetric float64
		for i := 0; i < j.Len("routes"); i++ {
			if j.Str(fmt.Sprintf("routes.%d.prefix", i)) != c.prefix {
				continue
			}
			if j.Bool(fmt.Sprintf("routes.%d.active", i)) {
				active = j.Str(fmt.Sprintf("routes.%d.via", i))
			} else {
				backup, backupMetric = j.Str(fmt.Sprintf("routes.%d.via", i)), j.Num(fmt.Sprintf("routes.%d.metric", i))
			}
		}
		if active != "aws-hub" || backup != c.other || backupMetric != 50 {
			w.Fatalf("%s %s: active via %q, backup via %q metric %v; want aws-hub and a backup via %s at 50", c.node, c.prefix, active, backup, backupMetric, c.other)
		}
	}
	if got := w.JSON(out, "nodes/builder-1.json").Strings("peers[node=dgx-spark].allowed_prefixes"); !slices.Equal(got, []string{"10.42.252.2/32"}) {
		w.Fatalf("builder-1's direct peer allows %v, want only the probe address", got)
	}
	w.ExpectFinding(out, "V-LINK-004", "warning", "builder-1~dgx-spark")
	expectSameConfig(w, base, out, "aws-hub")
}

// addSites adds the sites s<from> to s<to>, each with one spoke, to topo.yaml.
func addSites(w *e2e.W, from, to int) {
	w.T.Helper()
	for i := from; i <= to; i++ {
		addNode(w, fmt.Sprintf("s%d-1", i), fmt.Sprintf("s%d", i))
	}
}

// filled builds the end state of P1-E2E-047 up to the refused ninth site: the sites pool full.
func filled(w *e2e.W) (base, full *e2e.Out) {
	w.T.Helper()
	base = baselineTopo(w)
	w.Step("add sites s4 to s8")
	addSites(w, 4, 8)
	full = plan(w, base, "out-2", nil)
	w.ExpectOK(full)
	return base, full
}

// e2e047FillSites is P1-E2E-047: filling the sites pool is allowed, going past it is refused
// cleanly, and the refusal leaves nothing half-written.
func e2e047FillSites(w *e2e.W) {
	_, full := filled(w)
	for i, want := range []string{"10.42.48.0/20", "10.42.64.0/20", "10.42.80.0/20", "10.42.96.0/20", "10.42.112.0/20"} {
		expectAddress(w, full, ipam.Site(fmt.Sprintf("s%d", i+4)), want)
	}
	w.ExpectFinding(full, "V-ADDR-006", "warning", "sites")
	before := w.Snapshot("out-2")
	w.Step("a ninth site")
	addSites(w, 9, 9)
	out := plan(w, full, "out-3", nil)
	w.ExpectRefused(out, types.ErrPoolExhausted)
	if text := out.Err.Error(); !strings.Contains(text, "sites") || !strings.Contains(text, "s9") {
		w.Fatalf("the refusal does not name the pool and the site: %v", out.Err)
	}
	w.ExpectUnchanged("out-2", before)
}

// e2e048GrowthPool is P1-E2E-048: the reserved growth pool can be used only on purpose, and
// the reason is recorded with the block.
func e2e048GrowthPool(w *e2e.W) {
	_, full := filled(w)
	w.Step("a ninth site that asks for the growth pool")
	addSites(w, 9, 9)
	w.Edit("topo.yaml", e2e.Set("sites[id=s9].override", map[string]any{"pool": "growth", "reason": "approved expansion"}))
	out := plan(w, full, "out-3", nil)
	w.ExpectOK(out)
	expectAddress(w, out, ipam.Site("s9"), "10.42.128.0/20")
	a, _ := owner(out, ipam.Site("s9"))
	if a.Note != "override: approved expansion" || a.Pool != "growth" {
		w.Fatalf("the allocation is %+v, want the note \"override: approved expansion\" in the growth pool", a)
	}
	w.ExpectFinding(out, "V-ADDR-006", "warning", "sites")
	for _, f := range out.Result.Validation.Findings {
		if f.Rule == "V-ADDR-006" && f.Subject == "growth" {
			w.Fatalf("the growth pool is reported nearly full: %+v", f)
		}
		if f.Rule == "V-ADDR-005" {
			w.Fatalf("V-ADDR-005 fired although the override has a reason: %+v", f)
		}
	}
}
