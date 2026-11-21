package route

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/types"
)

// Notes is the extra knowledge Explain uses to say why. Everything in it is
// optional: a zero Notes gives a correct explanation without a name for the
// owner and without link health.
//
// Example:
//
//	notes := route.Notes{Owner: "dgx-spark, site dgx", Health: func(link types.LinkID) (bool, string) {
//		return true, "probe ok, 3/3"
//	}}
//	fmt.Print(route.Explain(table, netip.MustParseAddr("10.42.33.7"), notes))
type Notes struct {
	// Owner names who holds the destination, for the heading, for example
	// "dgx-spark, site dgx". Leave it empty to leave the heading bare.
	Owner string
	// Health reports whether a link is up and the evidence for it, for example
	// (true, "probe ok, 3/3"). Leave it nil if health is not known; the
	// explanation then says so.
	Health func(link types.LinkID) (up bool, detail string)
}

// Explanation says which route a node uses for an address, and why. It is the
// structured form; String writes it as text.
//
// Example:
//
//	e := route.Explain(table, netip.MustParseAddr("10.42.33.7"), route.Notes{})
//	if e.Match == nil { /* black hole */ }
//	fmt.Print(e.String())
type Explanation struct {
	// Node is the node whose table was asked.
	Node types.NodeID `json:"node"`
	// Dst is the destination address.
	Dst netip.Addr `json:"dst"`
	// Owner is the holder of the destination, from Notes. It may be empty.
	Owner string `json:"owner,omitempty"`
	// Candidates is how many routes in the table cover Dst, active or not.
	Candidates int `json:"candidates"`
	// Match is the active route a packet for Dst follows, the one Lookup
	// returns. It is nil if no active route covers Dst, which the simulator
	// calls a black hole.
	Match *Route `json:"match,omitempty"`
	// Backup is the inactive backup for the matched prefix, if there is one.
	Backup *Route `json:"backup,omitempty"`
	// Replaced is the computed route that a static override took out of use,
	// if Match is a static override and there was one.
	Replaced *Route `json:"replaced,omitempty"`
	// Reason says why Match is the route in use.
	Reason string `json:"reason"`
}

// Explain says which route node's table uses for dst, which route would take
// over if it failed, and why. The same facts can be had from Lookup; Explain is
// for people and for reports.
//
// A static override is always flagged: its line says so, and when it replaced a
// computed route that route is named too.
//
// Routes are masked to their canonical block (cidr.Canonical) before they are
// compared, as in Lookup, so Match, Backup and Replaced all carry masked
// prefixes. A nil table gives an explanation with no match.
//
// Example:
//
//	for builder-1 and 10.42.33.7 in the three-site network it says the
//	match is 10.42.32.0/20, the active route goes straight to dgx-spark at
//	metric 10, and the backup goes through aws-hub at metric 40.
func Explain(table *Table, dst netip.Addr, notes Notes) Explanation {
	explanation := Explanation{Dst: dst, Owner: notes.Owner}
	if table == nil {
		explanation.Reason = fmt.Sprintf("there is no table, so nothing covers %s and the packet is dropped", dst)
		return explanation
	}
	explanation.Node = table.Node
	for _, candidate := range table.Routes {
		if candidate.Prefix.Contains(dst) {
			explanation.Candidates++
		}
	}
	match, ok := table.Lookup(dst)
	if !ok {
		explanation.Reason = fmt.Sprintf("no active route covers %s, so the packet is dropped", dst)
		return explanation
	}
	explanation.Match = &match
	for _, given := range table.Routes {
		other := canonical(given)
		if other.Prefix != match.Prefix || other.Active {
			continue
		}
		switch other.Source {
		case SourceBackup:
			if explanation.Backup == nil || Compare(other, *explanation.Backup) < 0 {
				picked := other
				explanation.Backup = &picked
			}
		case SourceComputed:
			if match.Source == SourceStatic && (explanation.Replaced == nil || Compare(other, *explanation.Replaced) < 0) {
				picked := other
				explanation.Replaced = &picked
			}
		}
	}
	explanation.Reason = reasonFor(match, notes)
	return explanation
}

// reasonFor writes why a route is in use, with the health of its link if known.
func reasonFor(match Route, notes Notes) string {
	var reason string
	switch match.Source {
	case SourceConnected:
		return fmt.Sprintf("%s is on this node", match.Prefix)
	case SourceStatic:
		reason = "a static override set by an operator; it does not follow link health"
	case SourceBackup:
		reason = fmt.Sprintf("a backup that has taken over, metric %d", match.Metric)
	default:
		reason = fmt.Sprintf("the cheapest path, metric %d", match.Metric)
	}
	if notes.Health == nil {
		return reason + "; link health is not known"
	}
	up, detail := notes.Health(match.Via)
	state := "up"
	if !up {
		state = "DOWN, packets on it are lost"
	}
	if detail != "" {
		state += " (" + detail + ")"
	}
	return fmt.Sprintf("%s; link %s is %s", reason, match.Via, state)
}

// pathText writes a path as "a → b → c".
func pathText(path []types.NodeID) string {
	names := make([]string, len(path))
	for i, node := range path {
		names[i] = string(node)
	}
	return strings.Join(names, " → ")
}

// describe writes a route as the text of the active and backup lines.
func describe(r Route) string {
	if r.Source == SourceConnected {
		return "local, on this node"
	}
	text := fmt.Sprintf("via %s over %s, metric %d, path %s", r.NextHop, r.Via, r.Metric, pathText(r.Path))
	if r.Source == SourceStatic {
		text += " [static override]"
	}
	return text
}

// String writes the explanation as text, one fact per line:
//
//	builder-1 → 10.42.33.7 (dgx-spark, site dgx)
//	   match:   10.42.32.0/20 (longest of 2 candidates)
//	   active:  via dgx-spark over builder-1~dgx-spark, metric 10, path builder-1 → dgx-spark
//	   backup:  via aws-hub over aws-hub~builder-1, metric 40, path builder-1 → aws-hub → dgx-spark
//	   reason:  the cheapest path, metric 10; link builder-1~dgx-spark is up (probe ok, 3/3)
//
// The text is the same for the same explanation, so it can be kept as a golden
// file. A node with no matching route says "none" on the match and active lines.
//
// Example:
//
//	see the block above: the text of the explanation for builder-1 and 10.42.33.7.
func (e Explanation) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s → %s", e.Node, e.Dst)
	if e.Owner != "" {
		fmt.Fprintf(&out, " (%s)", e.Owner)
	}
	out.WriteString("\n")
	switch {
	case e.Match == nil:
		out.WriteString("   match:   none\n")
		out.WriteString("   active:  none, the packet would be dropped (black hole)\n")
	default:
		candidates := fmt.Sprintf("longest of %d candidates", e.Candidates)
		if e.Candidates == 1 {
			candidates = "the only candidate"
		}
		fmt.Fprintf(&out, "   match:   %s (%s)\n", e.Match.Prefix, candidates)
		fmt.Fprintf(&out, "   active:  %s\n", describe(*e.Match))
	}
	if e.Backup != nil {
		fmt.Fprintf(&out, "   backup:  %s\n", describe(*e.Backup))
	} else {
		out.WriteString("   backup:  none\n")
	}
	if e.Replaced != nil {
		fmt.Fprintf(&out, "   replaced: %s (computed, taken out of use by the override)\n", describe(*e.Replaced))
	}
	fmt.Fprintf(&out, "   reason:  %s\n", e.Reason)
	return out.String()
}

// sortedCopy returns the routes with every prefix masked (see canonical) and
// sorted by Compare, without changing the input.
func sortedCopy(routes []Route) []Route {
	out := make([]Route, len(routes))
	for i, r := range routes {
		out[i] = canonical(r)
	}
	slices.SortFunc(out, Compare)
	return out
}
