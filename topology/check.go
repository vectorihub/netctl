package topology

import (
	"fmt"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// Rule names one of the topology rules T1 to T11.
type Rule string

// The rules a topology must satisfy. T11 (unknown YAML fields) is enforced by
// Decode, because only the YAML tree knows about fields; the rest are checked by
// Check.
const (
	// RuleSchema (T1): the schema is beaver.topology/v1alpha1.
	RuleSchema Rule = "T1"
	// RuleIDs (T2): all IDs are valid and unique within their kind.
	RuleIDs Rule = "T2"
	// RuleNodeSite (T3): every node's site exists.
	RuleNodeSite Rule = "T3"
	// RuleLinkEnds (T4): every link's ends exist and are different nodes.
	RuleLinkEnds Rule = "T4"
	// RuleDuplicateLink (T5): at most one link per node pair per kind.
	RuleDuplicateLink Rule = "T5"
	// RuleHubSpokeEnds (T6): a hub-spoke link has exactly one hub end.
	RuleHubSpokeEnds Rule = "T6"
	// RuleHubExists (T7): a hub exists if any hub-spoke link does.
	RuleHubExists Rule = "T7"
	// RuleNeedsTraversal (T8): a direct link between two nodes behind NAT needs
	// NAT traversal. A warning, not an error.
	RuleNeedsTraversal Rule = "T8"
	// RuleSpokeReachesHub (T9): every spoke has a path to a hub.
	RuleSpokeReachesHub Rule = "T9"
	// RuleSelectorMatches (T10): every intent selector matches at least one node.
	RuleSelectorMatches Rule = "T10"
	// RuleUnknownField (T11): unknown YAML fields are errors. Decode reports it.
	RuleUnknownField Rule = "T11"
)

// Level says how serious a Finding is.
type Level int

const (
	// LevelError means the topology is unusable until it is fixed.
	LevelError Level = iota
	// LevelWarning means the topology works but needs attention.
	LevelWarning
)

// String returns "error" or "warning".
func (level Level) String() string {
	if level == LevelWarning {
		return "warning"
	}
	return "error"
}

// Finding is one thing Check found wrong, or worth knowing.
type Finding struct {
	// Rule is the rule it breaks, for example "T4".
	Rule Rule
	// Level is error or warning.
	Level Level
	// Subject is what it is about: a node, site, link ID or "intents[2].from".
	Subject string
	// Msg explains it.
	Msg string
}

// String returns "T4 error: subject: message".
func (finding Finding) String() string {
	return fmt.Sprintf("%s %s: %s: %s", finding.Rule, finding.Level, finding.Subject, finding.Msg)
}

// Report is everything Check found, in a fixed order: by rule, then by the
// sorted order of the topology.
type Report struct {
	// Findings holds the errors and warnings together.
	Findings []Finding
}

// Errors returns the findings that are errors.
func (report Report) Errors() []Finding { return report.level(LevelError) }

// Warnings returns the findings that are warnings.
func (report Report) Warnings() []Finding { return report.level(LevelWarning) }

// level returns the findings of one level, in report order, or nil when there
// are none. Errors and Warnings are thin wrappers over it.
func (report Report) level(level Level) []Finding {
	var out []Finding
	for _, finding := range report.Findings {
		if finding.Level == level {
			out = append(out, finding)
		}
	}
	return out
}

// Has reports whether any finding, of either level, breaks rule.
func (report Report) Has(rule Rule) bool {
	return slices.ContainsFunc(report.Findings, func(finding Finding) bool { return finding.Rule == rule })
}

// Err returns every error as a types.List of INVALID_TOPOLOGY errors, each
// message starting with its rule ("T4: ..."), or nil when there are none.
// Warnings never make Err non-nil.
func (report Report) Err() error {
	var list types.List
	for _, finding := range report.Errors() {
		list.Add(types.Errf(types.ErrInvalidTopology, finding.Subject, "%s: %s", finding.Rule, finding.Msg))
	}
	return list.Err()
}

// Load decodes YAML and then checks it: Decode followed by Check. A decode
// failure returns a nil Topology. When only Check fails, the Topology is returned
// as well, so a caller can show it next to the errors. The error lists every
// problem at once; warnings are in the Report only.
//
// Example: a file whose spoke has no link to a hub fails with
// "INVALID_TOPOLOGY: builder-1: T9: spoke has no path to a hub".
func Load(name string, data []byte, maxBytes int) (*Topology, Report, error) {
	topology, err := Decode(name, data, maxBytes)
	if err != nil {
		return nil, Report{}, err
	}
	report := topology.Check()
	return topology, report, report.Err()
}

// Check tests a topology against rules T1 to T10 and reports every violation,
// not just the first. It changes nothing, so call Normalize first on a Topology
// built in code (sorting makes the order of findings stable). T11 is Decode's job.
//
// Several rules skip what an earlier rule already reported, so one mistake is
// one finding: a link to a missing node (T4) is not also checked for hub ends,
// and no spoke is called isolated (T9) when T7 has already said there is no hub.
//
// Example: a link from "builder-1" to "builder-1" reports T4 "link joins a node
// to itself".
func (t *Topology) Check() Report {
	var check checker
	if t == nil {
		check.add(RuleSchema, LevelError, "topology", "there is no topology")
		return Report{Findings: check.found}
	}
	check.schema(t)
	check.ids(t)
	check.nodeSites(t)
	check.peers(t)
	check.links(t)
	check.selectors(t)
	return Report{Findings: check.found}
}

type checker struct {
	found []Finding
}

// add records one finding. The message is built with fmt.Sprintf, so callers
// pass a format and its arguments; the finding keeps the order it was added in,
// which is what makes a Report's order stable.
func (check *checker) add(rule Rule, level Level, subject, format string, args ...any) {
	check.found = append(check.found, Finding{Rule: rule, Level: level, Subject: subject, Msg: fmt.Sprintf(format, args...)})
}

// schema is rule T1: the topology must name the schema this version reads. A
// missing schema and a different one are separate messages, because the fix
// differs (add the line, or migrate the file).
func (check *checker) schema(t *Topology) {
	switch t.Schema {
	case Schema:
	case "":
		check.add(RuleSchema, LevelError, "schema", "missing; write schema: %s", Schema)
	default:
		check.add(RuleSchema, LevelError, "schema", "%q is not supported; this version reads %s", t.Schema, Schema)
	}
}

// ids is rule T2: every site ID and every node ID must be valid (see
// types.ValidID) and unique within its own kind. A site and a node may share an
// ID; two sites or two nodes may not.
func (check *checker) ids(t *Topology) {
	seen := map[string]bool{}
	for _, site := range t.Sites {
		check.id("site", string(site.ID), seen)
	}
	seen = map[string]bool{}
	for _, node := range t.Nodes {
		check.id("node", string(node.ID), seen)
	}
}

// id checks one ID against the IDs seen so far in its kind, and records it. kind
// is "site" or "node" and only labels the message. An invalid ID is reported once,
// not also as a duplicate.
func (check *checker) id(kind, id string, seen map[string]bool) {
	switch {
	case !types.ValidID(id):
		check.add(RuleIDs, LevelError, kind+" "+id, "%q is not a valid %s ID", id, kind)
	case seen[id]:
		check.add(RuleIDs, LevelError, kind+" "+id, "duplicate %s ID %q", kind, id)
	}
	seen[id] = true
}

// nodeSites is rule T3: every node's Site must be one of the declared sites.
func (check *checker) nodeSites(t *Topology) {
	sites := map[types.SiteID]bool{}
	for _, site := range t.Sites {
		sites[site.ID] = true
	}
	for _, node := range t.Nodes {
		if !sites[node.Site] {
			check.add(RuleNodeSite, LevelError, "node "+string(node.ID), "unknown site %q", node.Site)
		}
	}
}

// Peers that were never imported are links nobody turned into links (T4).
func (check *checker) peers(t *Topology) {
	for _, node := range t.Nodes {
		if len(node.Peers) > 0 {
			check.add(RuleLinkEnds, LevelError, "node "+string(node.ID), "peers were not imported; call ImportPeers")
		}
	}
}

// links runs the rules that look at links and the graph they make, in order:
// T4 (ends exist and differ), T5 (no duplicate pair and kind), T6 (a hub-spoke
// link has exactly one hub end), T7 (a hub exists), T8 (NAT warning) and T9
// (every spoke reaches a hub). A link that fails T4 is left out of the later
// rules, so one mistake is one finding; T9 is skipped when T7 already fired.
func (check *checker) links(t *Topology) {
	nodes := map[types.NodeID]Node{}
	for _, node := range t.Nodes {
		nodes[node.ID] = node
	}

	// T4: the ends. Links that fail here are left out of the rules below.
	var sound []Link
	for _, link := range t.Links {
		subject := "link " + string(link.ID)
		_, hasA := nodes[link.A]
		_, hasB := nodes[link.B]
		ok := true
		if !hasA {
			check.add(RuleLinkEnds, LevelError, subject, "unknown node %q", link.A)
			ok = false
		}
		if !hasB && link.B != link.A {
			check.add(RuleLinkEnds, LevelError, subject, "unknown node %q", link.B)
			ok = false
		}
		if link.A == link.B {
			check.add(RuleLinkEnds, LevelError, subject, "link joins node %q to itself", link.A)
			ok = false
		}
		if ok {
			sound = append(sound, link)
		}
	}

	// T5: one link per pair per kind.
	type pair struct {
		id   types.LinkID
		kind types.LinkKind
	}
	counts := map[pair]int{}
	for _, link := range sound {
		key := pair{link.ID, link.Kind}
		counts[key]++
		if counts[key] == 2 {
			check.add(RuleDuplicateLink, LevelError, "link "+string(link.ID), "more than one %s link joins %s and %s", link.Kind, link.A, link.B)
		}
	}

	// T6: a hub-spoke link has exactly one hub end.
	hubSpoke := false
	for _, link := range sound {
		if link.Kind != types.LinkHubSpoke {
			continue
		}
		hubSpoke = true
		hubs := 0
		for _, end := range []types.NodeID{link.A, link.B} {
			if nodes[end].Role == types.RoleHub {
				hubs++
			}
		}
		if hubs != 1 {
			check.add(RuleHubSpokeEnds, LevelError, "link "+string(link.ID), "a hub-spoke link needs exactly one hub end; %s and %s have %d", link.A, link.B, hubs)
		}
	}

	// T7: a hub exists if any hub-spoke link does.
	var hubs []types.NodeID
	for _, node := range t.Nodes {
		if node.Role == types.RoleHub {
			hubs = append(hubs, node.ID)
		}
	}
	noHub := hubSpoke && len(hubs) == 0
	if noHub {
		check.add(RuleHubExists, LevelError, "nodes", "hub-spoke links exist but no node has role hub")
	}

	// T8: a direct link between two nodes behind NAT.
	for _, link := range sound {
		if link.Kind == types.LinkDirect && nodes[link.A].NAT == NATBehind && nodes[link.B].NAT == NATBehind {
			check.add(RuleNeedsTraversal, LevelWarning, "link "+string(link.ID), "needs-traversal: %s and %s are both behind NAT, so this link needs NAT traversal and the hub path must stay as its fallback", link.A, link.B)
		}
	}

	// T9: every spoke reaches a hub, over links of any kind.
	if noHub {
		return
	}
	neighbours := map[types.NodeID][]types.NodeID{}
	for _, link := range sound {
		neighbours[link.A] = append(neighbours[link.A], link.B)
		neighbours[link.B] = append(neighbours[link.B], link.A)
	}
	reached := map[types.NodeID]bool{}
	queue := slices.Clone(hubs)
	for _, hub := range hubs {
		reached[hub] = true
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range neighbours[current] {
			if !reached[next] {
				reached[next] = true
				queue = append(queue, next)
			}
		}
	}
	for _, node := range t.Nodes {
		if node.Role == types.RoleSpoke && !reached[node.ID] {
			check.add(RuleSpokeReachesHub, LevelError, "node "+string(node.ID), "spoke has no path to a hub")
		}
	}
}

// selectors is rule T10: both selectors of every intent must pick at least one
// node. A selector with no kind at all (never set) is reported separately, since
// there is nothing to match.
func (check *checker) selectors(t *Topology) {
	for i, intent := range t.Intents {
		for _, end := range []struct {
			name     string
			selector Selector
		}{{"from", intent.From}, {"to", intent.To}} {
			subject := fmt.Sprintf("intents[%d].%s", i, end.name)
			switch {
			case end.selector.Kind == SelectorUnknown:
				check.add(RuleSelectorMatches, LevelError, subject, "no selector given")
			case !slices.ContainsFunc(t.Nodes, end.selector.Matches):
				check.add(RuleSelectorMatches, LevelError, subject, "selector %q matches no node", end.selector)
			}
		}
	}
}
