package sim

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// DefaultTTL is the hop limit a packet gets when it does not set one. Beaver
// paths are never expected to be longer than four hops, so sixteen leaves a wide
// margin.
const DefaultTTL = 16

// HopResult says what happened to a packet at one node.
//
// The zero value is HopUnknown, so a hop that was never filled in cannot pass
// for a real result.
type HopResult int

const (
	// HopUnknown is the zero value: no result was set.
	HopUnknown HopResult = iota
	// Forwarded means the node found a route and passed the packet on.
	Forwarded
	// Delivered means the destination address is the node's own.
	Delivered
	// BlackHole means no route matched, so the packet was dropped.
	BlackHole
	// LinkDown means the route leaves over a link that is down.
	LinkDown
	// TTLExceeded means the packet ran out of hops.
	TTLExceeded
	// LoopDetected means the packet came back to a node it had already visited.
	LoopDetected
	// WrongNeighbour means the route's next hop is not joined to the node by the
	// route's link: the tables and the graph disagree.
	WrongNeighbour
)

// hopNames holds the text of each result.
var hopNames = map[HopResult]string{
	Forwarded:      "FORWARDED",
	Delivered:      "DELIVERED",
	BlackHole:      "BLACK_HOLE",
	LinkDown:       "LINK_DOWN",
	TTLExceeded:    "TTL_EXCEEDED",
	LoopDetected:   "LOOP",
	WrongNeighbour: "WRONG_NEIGHBOUR",
}

// String returns the result in capitals, for example "BLACK_HOLE", or "UNKNOWN".
func (result HopResult) String() string {
	if name, ok := hopNames[result]; ok {
		return name
	}
	return "UNKNOWN"
}

// ParseHopResult converts the text String returns back into a HopResult.
// Anything else, including a different capitalisation, returns HopUnknown and
// false.
//
// Example: ParseHopResult("LOOP") returns LoopDetected, true.
func ParseHopResult(text string) (HopResult, bool) {
	for result, name := range hopNames {
		if name == text {
			return result, true
		}
	}
	return HopUnknown, false
}

// MarshalText writes the result as its name. An unknown result returns an
// error, so a half-filled trace cannot be written out.
func (result HopResult) MarshalText() ([]byte, error) {
	if _, ok := hopNames[result]; !ok {
		return nil, fmt.Errorf("sim: cannot write unknown hop result %d", int(result))
	}
	return []byte(result.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
func (result *HopResult) UnmarshalText(text []byte) error {
	parsed, ok := ParseHopResult(string(text))
	if !ok {
		return fmt.Errorf("sim: unknown hop result %q", text)
	}
	*result = parsed
	return nil
}

// Verdict is the judgement on one flow, forward and return together.
//
// The zero value is VerdictUnknown. It fails the gate, so a flow that was never
// judged cannot pass.
type Verdict int

const (
	// VerdictUnknown is the zero value: the flow was not judged.
	VerdictUnknown Verdict = iota
	// VerdictOK means delivered both ways, along the same path reversed.
	VerdictOK
	// VerdictOKAsymmetricAllowed means delivered both ways along different paths,
	// and the pair is explicitly allowed to be asymmetric. It is a warning.
	VerdictOKAsymmetricAllowed
	// VerdictAsymmetric means delivered both ways along different paths, and
	// the pair is not allowed to be. WireGuard's cryptokey routing and stateful
	// firewalls both break on this.
	VerdictAsymmetric
	// VerdictNoReturn means the forward packet arrived but the reply could not
	// get back.
	VerdictNoReturn
	// VerdictBlackHole means the forward packet reached a node with no route.
	VerdictBlackHole
	// VerdictLoop means a packet looped, forward or on the way back.
	VerdictLoop
	// VerdictLinkDown means an active route uses a link that is down: failover
	// did not happen.
	VerdictLinkDown
	// VerdictWrongNeighbour means a route points at a node that is not adjacent:
	// the tables and the graph disagree.
	VerdictWrongNeighbour
	// VerdictTTLExceeded means a packet ran out of hops without looping, because
	// its path is longer than the limit.
	VerdictTTLExceeded
	// VerdictUnexpectedDelivery means a negative flow, one that must not be
	// delivered anywhere, was delivered.
	VerdictUnexpectedDelivery
	// VerdictNoHealthyPath means the routing engine found no healthy path
	// between the two nodes, so failing to deliver is a consequence of the
	// current link health and not a planning error.
	VerdictNoHealthyPath
	// VerdictUnreachableExpected means a negative flow was correctly not
	// delivered.
	VerdictUnreachableExpected
)

// verdictNames holds the text of each verdict.
var verdictNames = map[Verdict]string{
	VerdictOK:                  "OK",
	VerdictOKAsymmetricAllowed: "OK_ASYMMETRIC_ALLOWED",
	VerdictAsymmetric:          "ASYMMETRIC",
	VerdictNoReturn:            "NO_RETURN",
	VerdictBlackHole:           "BLACK_HOLE",
	VerdictLoop:                "LOOP",
	VerdictLinkDown:            "LINK_DOWN",
	VerdictWrongNeighbour:      "WRONG_NEIGHBOUR",
	VerdictTTLExceeded:         "TTL_EXCEEDED",
	VerdictUnexpectedDelivery:  "UNEXPECTED_DELIVERY",
	VerdictNoHealthyPath:       "NO_HEALTHY_PATH",
	VerdictUnreachableExpected: "UNREACHABLE_EXPECTED",
}

// verdictOrder is the fixed order verdicts are listed in, worst news last, so
// summaries and reports always list them the same way.
var verdictOrder = []Verdict{
	VerdictOK, VerdictOKAsymmetricAllowed, VerdictUnreachableExpected, VerdictNoHealthyPath,
	VerdictAsymmetric, VerdictNoReturn, VerdictBlackHole, VerdictLoop, VerdictLinkDown,
	VerdictWrongNeighbour, VerdictTTLExceeded, VerdictUnexpectedDelivery, VerdictUnknown,
}

// String returns the verdict in capitals, for example "NO_RETURN", or "UNKNOWN".
func (verdict Verdict) String() string {
	if name, ok := verdictNames[verdict]; ok {
		return name
	}
	return "UNKNOWN"
}

// ParseVerdict converts the text String returns back into a Verdict. Anything
// else returns VerdictUnknown and false.
//
// Example: ParseVerdict("NO_RETURN") returns VerdictNoReturn, true.
func ParseVerdict(text string) (Verdict, bool) {
	for verdict, name := range verdictNames {
		if name == text {
			return verdict, true
		}
	}
	return VerdictUnknown, false
}

// MarshalText writes the verdict as its name. An unknown verdict returns an
// error, so an unjudged flow cannot be written out.
func (verdict Verdict) MarshalText() ([]byte, error) {
	if _, ok := verdictNames[verdict]; !ok {
		return nil, fmt.Errorf("sim: cannot write unknown verdict %d", int(verdict))
	}
	return []byte(verdict.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
func (verdict *Verdict) UnmarshalText(text []byte) error {
	parsed, ok := ParseVerdict(string(text))
	if !ok {
		return fmt.Errorf("sim: unknown verdict %q", text)
	}
	*verdict = parsed
	return nil
}

// FailsGate reports whether the verdict stops a plan from being applied.
//
// These pass: OK, OK_ASYMMETRIC_ALLOWED (with a warning), UNREACHABLE_EXPECTED,
// and NO_HEALTHY_PATH, which is the honest result when links have failed and no
// path is left. Everything else fails, including an unknown verdict.
func (verdict Verdict) FailsGate() bool {
	switch verdict {
	case VerdictOK, VerdictOKAsymmetricAllowed, VerdictUnreachableExpected, VerdictNoHealthyPath:
		return false
	}
	return true
}

// Warning reports whether the verdict passes the gate but deserves attention:
// delivered, but along different paths that were explicitly allowed to differ.
func (verdict Verdict) Warning() bool { return verdict == VerdictOKAsymmetricAllowed }

// Packet is one simulated packet.
type Packet struct {
	// Src and Dst are the source and destination addresses.
	Src netip.Addr `json:"src"`
	// Dst is the destination address.
	Dst netip.Addr `json:"dst"`
	// TTL is the number of hops the packet may take. Zero means DefaultTTL.
	TTL int `json:"ttl"`
	// Flow labels the packet for reports, for example "builder-1→dgx-spark".
	Flow string `json:"flow,omitempty"`
}

// Hop is what happened to a packet at one node.
type Hop struct {
	// Node is where the packet was.
	Node types.NodeID `json:"node"`
	// Route is the route that matched, a copy of it, or nil if none did.
	Route *route.Route `json:"route,omitempty"`
	// Result is what the node did with the packet.
	Result HopResult `json:"result"`
}

// Trace is the journey of one packet.
type Trace struct {
	// Packet is the packet that was sent, with its TTL filled in.
	Packet Packet `json:"packet"`
	// Hops lists every node the packet was at, in order, ending with the node
	// where it was delivered or dropped.
	Hops []Hop `json:"hops"`
	// Final is how the journey ended: the result of the last hop.
	Final HopResult `json:"final"`
}

// Delivered reports whether the packet reached its destination.
func (trace Trace) Delivered() bool { return trace.Final == Delivered }

// Nodes returns the nodes the packet visited, in order.
func (trace Trace) Nodes() []types.NodeID {
	nodes := make([]types.NodeID, len(trace.Hops))
	for i, hop := range trace.Hops {
		nodes[i] = hop.Node
	}
	return nodes
}

// Links returns the links the packet crossed, in order: the link of every hop
// that forwarded it.
func (trace Trace) Links() []types.LinkID {
	var links []types.LinkID
	for _, hop := range trace.Hops {
		if hop.Result == Forwarded && hop.Route != nil {
			links = append(links, hop.Route.Via)
		}
	}
	return links
}

// Crossings returns how many links the packet crossed.
func (trace Trace) Crossings() int { return len(trace.Links()) }

// ProblemKind names a kind of problem a flow can have.
type ProblemKind string

const (
	// ProblemBlackHole is a packet dropped for want of a route.
	ProblemBlackHole ProblemKind = "black_hole"
	// ProblemLoop is a packet that came back to a node it had visited.
	ProblemLoop ProblemKind = "loop"
	// ProblemLinkDown is a route that uses a link that is down.
	ProblemLinkDown ProblemKind = "link_down"
	// ProblemWrongNeighbour is a route to a node that is not adjacent.
	ProblemWrongNeighbour ProblemKind = "wrong_neighbour"
	// ProblemTTLExceeded is a packet that ran out of hops.
	ProblemTTLExceeded ProblemKind = "ttl_exceeded"
	// ProblemAsymmetric is a return path that is not the forward path reversed.
	ProblemAsymmetric ProblemKind = "asymmetric"
	// ProblemUnexpectedDelivery is a negative flow that was delivered.
	ProblemUnexpectedDelivery ProblemKind = "unexpected_delivery"
)

// Problem is one thing wrong with a flow, with where and which way.
type Problem struct {
	// Kind says what is wrong.
	Kind ProblemKind `json:"kind"`
	// Direction is "forward", "return" or empty when it concerns both.
	Direction string `json:"direction,omitempty"`
	// Node is where it went wrong, if one node is to blame.
	Node types.NodeID `json:"node,omitempty"`
	// Detail explains it in words.
	Detail string `json:"detail"`
}

// Family says where a flow came from.
type Family int

const (
	// FamilyUnknown is the zero value: no family was set.
	FamilyUnknown Family = iota
	// FamilyOverlay is one node's overlay address to another's.
	FamilyOverlay
	// FamilySite is one site's representative address to another site's.
	FamilySite
	// FamilyDeny is a deny intent. Phase 1 reports it as reachable, because
	// policy is not enforced yet, so that Phase 3 has the list.
	FamilyDeny
	// FamilyNegative is a packet to an address outside the supernet, which must
	// not be delivered anywhere.
	FamilyNegative
)

// familyNames holds the text of each family.
var familyNames = map[Family]string{
	FamilyOverlay: "overlay", FamilySite: "site", FamilyDeny: "deny", FamilyNegative: "negative",
}

// String returns "overlay", "site", "deny", "negative" or "unknown".
func (family Family) String() string {
	if name, ok := familyNames[family]; ok {
		return name
	}
	return "unknown"
}

// MarshalText writes the family as its name. An unknown family returns an error.
func (family Family) MarshalText() ([]byte, error) {
	if _, ok := familyNames[family]; !ok {
		return nil, fmt.Errorf("sim: cannot write unknown flow family %d", int(family))
	}
	return []byte(family.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
func (family *Family) UnmarshalText(text []byte) error {
	for candidate, name := range familyNames {
		if name == string(text) {
			*family = candidate
			return nil
		}
	}
	return fmt.Errorf("sim: unknown flow family %q", text)
}

// Flow is one thing to test: a packet from one address to another, injected at a
// node, with the reply expected back.
type Flow struct {
	// Label names the flow in reports, for example "builder-1→dgx-spark (overlay)".
	// It is unique within a derived set.
	Label string `json:"label"`
	// Family says where the flow came from.
	Family Family `json:"family"`
	// Src and Dst are the packet's addresses.
	Src netip.Addr `json:"src"`
	// Dst is the destination address.
	Dst netip.Addr `json:"dst"`
	// From is the node the packet is injected at.
	From types.NodeID `json:"from"`
	// To is the node that holds Dst, where the reply is injected. It is empty for
	// a negative flow, which has no destination node.
	To types.NodeID `json:"to,omitempty"`
	// AllowAsymmetric lets the forward and return paths differ without failing
	// the gate.
	AllowAsymmetric bool `json:"allow_asymmetric,omitempty"`
	// Intent says an allow intent asked for this flow.
	Intent bool `json:"intent,omitempty"`
	// Ports are the destination ports of a deny intent. They are recorded for
	// Phase 3 and not simulated.
	Ports []uint16 `json:"ports,omitempty"`
	// Note says anything a reader of the report should know, for example that a
	// deny flow is reachable because policy is not enforced yet.
	Note string `json:"note,omitempty"`
}

// Negative reports whether the flow must not be delivered.
func (flow Flow) Negative() bool { return flow.Family == FamilyNegative }

// FlowResult is the outcome of one flow.
type FlowResult struct {
	// Flow is the flow that was run.
	Flow Flow `json:"flow"`
	// Forward is the packet's journey from the source.
	Forward Trace `json:"forward"`
	// Return is the reply's journey back. It is nil when the forward packet was
	// not delivered, because the destination never saw it to reply to.
	Return *Trace `json:"return,omitempty"`
	// Symmetric says the return path is exactly the forward path reversed, in
	// nodes and in links.
	Symmetric bool `json:"symmetric"`
	// Verdict is the judgement.
	Verdict Verdict `json:"verdict"`
	// Problems lists what is wrong, if anything.
	Problems []Problem `json:"problems,omitempty"`
}

// VerdictCount is how many flows got a verdict.
type VerdictCount struct {
	// Verdict is the verdict.
	Verdict Verdict `json:"verdict"`
	// Count is how many flows have it.
	Count int `json:"count"`
}

// ProblemCount is how many problems of a kind there were.
type ProblemCount struct {
	// Kind is the kind of problem.
	Kind ProblemKind `json:"kind"`
	// Count is how many there were.
	Count int `json:"count"`
}

// Summary counts a report's flows.
type Summary struct {
	// Total is the number of flows.
	Total int `json:"total"`
	// Failed is the number of flows whose verdict fails the gate.
	Failed int `json:"failed"`
	// Warnings is the number of flows that pass with a warning.
	Warnings int `json:"warnings"`
	// Verdicts lists how many flows have each verdict that occurs, in a fixed order.
	Verdicts []VerdictCount `json:"verdicts"`
	// Problems lists how many problems of each kind occur, sorted by kind.
	Problems []ProblemCount `json:"problems"`
}

// Count returns how many flows have the verdict.
func (summary Summary) Count(verdict Verdict) int {
	for _, entry := range summary.Verdicts {
		if entry.Verdict == verdict {
			return entry.Count
		}
	}
	return 0
}

// Report is the outcome of simulating a set of flows.
type Report struct {
	// Flows holds every flow's result, sorted by label.
	Flows []FlowResult `json:"flows"`
	// Summary counts the flows.
	Summary Summary `json:"summary"`
	// Passed is true if no flow fails the gate.
	Passed bool `json:"passed"`
}

// NewReport gathers flow results into a report: sorted by label, counted, and
// marked passed if no verdict fails the gate. The input is not changed. A report
// of no flows has passed vacuously; its Summary.Total says it tested nothing.
//
// Example: results with verdicts OK, OK and NO_RETURN give a summary of 3 flows,
// 1 failed, and Passed false.
func NewReport(results []FlowResult) Report {
	flows := slices.Clone(results)
	if flows == nil {
		flows = []FlowResult{}
	}
	slices.SortStableFunc(flows, func(first, second FlowResult) int {
		return strings.Compare(first.Flow.Label, second.Flow.Label)
	})
	summary := Summary{Total: len(flows), Verdicts: []VerdictCount{}, Problems: []ProblemCount{}}
	byVerdict := map[Verdict]int{}
	byProblem := map[ProblemKind]int{}
	for _, flow := range flows {
		byVerdict[flow.Verdict]++
		if flow.Verdict.FailsGate() {
			summary.Failed++
		}
		if flow.Verdict.Warning() {
			summary.Warnings++
		}
		for _, problem := range flow.Problems {
			byProblem[problem.Kind]++
		}
	}
	for _, verdict := range verdictOrder {
		if byVerdict[verdict] > 0 {
			summary.Verdicts = append(summary.Verdicts, VerdictCount{Verdict: verdict, Count: byVerdict[verdict]})
		}
	}
	kinds := make([]ProblemKind, 0, len(byProblem))
	for kind := range byProblem {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	for _, kind := range kinds {
		summary.Problems = append(summary.Problems, ProblemCount{Kind: kind, Count: byProblem[kind]})
	}
	return Report{Flows: flows, Summary: summary, Passed: summary.Failed == 0}
}
