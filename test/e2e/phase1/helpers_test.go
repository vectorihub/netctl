package phase1

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl"
	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/types"
)

// link returns the health the plan saved for a link, failing the test if there is none.
func link(w *e2e.W, out *e2e.Out, id string) *health.Link {
	w.T.Helper()
	state := out.FailoverState()
	if state == nil {
		w.Fatalf("%s carries no failover state", out.Name)
		return nil
	}
	l, ok := state.Link(types.LinkID(id))
	if !ok {
		w.Fatalf("%s has no health for %s", out.Name, id)
		return nil
	}
	return l
}

// decisions returns the failover decisions a plan made, in order.
func decisions(out *e2e.Out) []failover.Decision {
	if out.Result.Failover == nil {
		return nil
	}
	return out.Result.Failover.Decisions
}

// expectDecision fails unless the plan made a decision for the link going from one health to
// another at the offset from T0, and returns it.
func expectDecision(w *e2e.W, out *e2e.Out, id string, before, after health.LinkHealth, at time.Duration) failover.Decision {
	w.T.Helper()
	for _, d := range decisions(out) {
		if string(d.Link) == id && d.Before == before && d.After == after && d.At.Equal(e2e.T0.Add(at)) {
			return d
		}
	}
	w.Fatalf("%s: no decision %s %v -> %v at T0+%v; decisions: %v", out.Name, id, before, after, at, decisions(out))
	return failover.Decision{}
}

// failProbes is n failed probes from both ends of the direct link, starting at the offset
// and five seconds apart.
func failProbes(n int, start time.Duration) *e2e.Signals {
	return new(e2e.Signals).Probes(e2e.DirectLink, false, n, start, 5*time.Second)
}

// goodProbes is n good probes from both ends of the direct link, starting at the offset and
// five seconds apart.
func goodProbes(n int, start time.Duration) *e2e.Signals {
	return new(e2e.Signals).Probes(e2e.DirectLink, true, n, start, 5*time.Second)
}

// expectNoDiff fails the test unless the two plans would put the same configs on the nodes.
func expectNoDiff(w *e2e.W, before, after *e2e.Out) {
	w.T.Helper()
	if d := netctl.Diff(before.Result, after.Result); !d.Empty() {
		w.Fatalf("%s and %s differ:\n%s", before.Name, after.Name, d.Text())
	}
}

// expectDiffNodes fails the test unless the configs differ on exactly the named nodes.
func expectDiffNodes(w *e2e.W, before, after *e2e.Out, nodes ...string) {
	w.T.Helper()
	d := netctl.Diff(before.Result, after.Result)
	var got []string
	for _, n := range d.Nodes {
		got = append(got, string(n.Node))
	}
	slices.Sort(got)
	want := slices.Clone(nodes)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		w.Fatalf("%s -> %s changes nodes %v, want %v", before.Name, after.Name, got, want)
	}
}

// expectFlowPath fails unless the flow went forward through the nodes, came back through them
// reversed, and is symmetric.
func expectFlowPath(w *e2e.W, out *e2e.Out, label string, hops ...string) {
	w.T.Helper()
	f := w.Flow(out, label)
	var fwd, ret []string
	for _, h := range f.Forward.Hops {
		fwd = append(fwd, string(h.Node))
	}
	if f.Return != nil {
		for _, h := range f.Return.Hops {
			ret = append(ret, string(h.Node))
		}
	}
	rev := slices.Clone(hops)
	slices.Reverse(rev)
	if !slices.Equal(fwd, hops) || !slices.Equal(ret, rev) || !f.Symmetric {
		w.Fatalf("%s: %s forward %v, return %v, symmetric %v; want %v and its reverse", out.Name, label, fwd, ret, f.Symmetric, hops)
	}
}

// allowedKeys are the keys a node config may hold. Anything else, an interface name or a
// table number for instance, is an operating-system detail that must not be in a config (AC9).
var allowedKeys = map[string]bool{
	"schema": true, "plan_id": true, "node": true, "role": true, "forwarding": true, "overlay": true,
	"address": true, "listen_port": true, "site_prefix": true, "advertise": true, "peers": true,
	"link": true, "public_key_ref": true, "endpoints": true, "needs_traversal": true,
	"keepalive_seconds": true, "probe": true, "local": true, "remote": true, "allowed_prefixes": true,
	"routes": true, "prefix": true, "via": true, "metric": true, "active": true, "backup": true,
	"health": true, "probes": true, "target": true, "interval_seconds": true, "fail_threshold": true,
	"recover_threshold": true, "hold_down_seconds": true, "policy": true, "hash": true,
}

// stray returns the keys of a decoded JSON document that are not on the allow-list.
func stray(v any, found map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, item := range t {
			if !allowedKeys[k] {
				found[k] = true
			}
			stray(item, found)
		}
	case []any:
		for _, item := range t {
			stray(item, found)
		}
	}
}

// expectCleanKeys fails the test if any node config holds a key outside the allow-list.
func expectCleanKeys(w *e2e.W, out *e2e.Out) {
	w.T.Helper()
	found := map[string]bool{}
	for _, node := range out.Nodes() {
		stray(w.JSON(out, "nodes/"+node+".json").Get(""), found)
	}
	if len(found) > 0 {
		var keys []string
		for k := range found {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		w.Fatalf("%s: configs hold keys outside the schema allow-list: %s", out.Name, strings.Join(keys, ", "))
	}
}

// addr parses an address for an explain call.
func addr(text string) netip.Addr { return netip.MustParseAddr(text) }

// listed writes a list of strings on one line for a failure message.
func listed(s []string) string { return fmt.Sprintf("%q", s) }

// nodeID converts text to a node ID.
func nodeID(s string) types.NodeID { return types.NodeID(s) }

// mustText returns the plain-text explanation of a route, failing the test on an error.
func mustText(w *e2e.W, out *e2e.Out, from, to string) []byte {
	w.T.Helper()
	text, err := out.Result.ExplainText(nodeID(from), addr(to))
	if err != nil {
		w.Fatalf("explain: %v", err)
	}
	return []byte(text)
}

// prefix parses a prefix for a fixture.
func prefix(text string) netip.Prefix { return netip.MustParsePrefix(text) }

// expectSameConfig fails unless a node's config is the same in two plans apart from the plan
// ID and hash, which are derived from every node's content and so change whenever any node
// does. The guide's "byte-identical" holds for everything else.
func expectSameConfig(w *e2e.W, a, b *e2e.Out, node string) {
	w.T.Helper()
	path := "nodes/" + node + ".json"
	if string(e2e.Masked(a.Files[path])) != string(e2e.Masked(b.Files[path])) {
		w.Fatalf("%s differs between %s and %s", node, a.Name, b.Name)
	}
}
