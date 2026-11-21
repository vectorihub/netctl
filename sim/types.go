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
//
// Example:
//
//	sim.Packet{Src: src, Dst: dst, TTL: sim.DefaultTTL} // TTL 16
const DefaultTTL = 16

// HopResult says what happened to a packet at one node.
//
// The zero value is HopUnknown, so a hop that was never filled in cannot pass
// for a real result.
//
// Example:
//
//	if trace.Final == sim.Delivered { /* the packet arrived */ }
type HopResult int

const (
	// HopUnknown is the zero value: no result was set.
	//
	// Example:
	//
	//	sim.HopUnknown.String() // "UNKNOWN"
	HopUnknown HopResult = iota
	// Forwarded means the node found a route and passed the packet on.
	//
	// Example:
	//
	//	sim.Forwarded.String() // "FORWARDED"
	Forwarded
	// Delivered means the destination address is the node's own.
	//
	// Example:
	//
	//	sim.Delivered.String() // "DELIVERED"
	Delivered
	// BlackHole means no route matched, so the packet was dropped.
	//
	// Example:
	//
	//	sim.BlackHole.String() // "BLACK_HOLE"
	BlackHole
	// LinkDown means the route leaves over a link that is down.
	//
	// Example:
	//
	//	sim.LinkDown.String() // "LINK_DOWN"
	LinkDown
	// TTLExceeded means the packet ran out of hops.
	//
	// Example:
	//
	//	sim.TTLExceeded.String() // "TTL_EXCEEDED"
	TTLExceeded
	// LoopDetected means the packet came back to a node it had already visited.
	//
	// Example:
	//
	//	sim.LoopDetected.String() // "LOOP"
	LoopDetected
	// WrongNeighbour means the route's next hop is not joined to the node by the
	// route's link: the tables and the graph disagree.
	//
	// Example:
	//
	//	sim.WrongNeighbour.String() // "WRONG_NEIGHBOUR"
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
//
// Example:
//
//	BlackHole.String() returns "BLACK_HOLE".
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
// Example:
//
//	ParseHopResult("LOOP") returns LoopDetected, true.
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
//
// Example:
//
//	LoopDetected.MarshalText() returns "LOOP"; the zero HopResult returns an INVALID_VALUE error.
func (result HopResult) MarshalText() ([]byte, error) {
	if _, ok := hopNames[result]; !ok {
		return nil, types.Errf(types.ErrInvalidValue, "hop result", "cannot write unknown hop result %d", int(result))
	}
	return []byte(result.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
//
// Example:
//
//	UnmarshalText("LINK_DOWN") sets the receiver to LinkDown; "link down" returns an
//	INVALID_VALUE error.
func (result *HopResult) UnmarshalText(text []byte) error {
	parsed, ok := ParseHopResult(string(text))
	if !ok {
		return types.Errf(types.ErrInvalidValue, "hop result", "unknown hop result %q", text)
	}
	*result = parsed
	return nil
}

// Verdict is the judgement on one flow, forward and return together.
//
// The zero value is VerdictUnknown. It fails the gate, so a flow that was never
// judged cannot pass.
//
// Example:
//
//	if result.Verdict.FailsGate() { /* the flow fails the check */ }
type Verdict int

const (
	// VerdictUnknown is the zero value: the flow was not judged.
	//
	// Example:
	//
	//	sim.VerdictUnknown.String() // "UNKNOWN"; FailsGate() is true
	VerdictUnknown Verdict = iota
	// VerdictOK means delivered both ways, along the same path reversed.
	//
	// Example:
	//
	//	sim.VerdictOK.String() // "OK"; FailsGate() is false
	VerdictOK
	// VerdictOKAsymmetricAllowed means delivered both ways along different paths,
	// and the pair is explicitly allowed to be asymmetric. It is a warning.
	//
	// Example:
	//
	//	sim.VerdictOKAsymmetricAllowed.String() // "OK_ASYMMETRIC_ALLOWED"; FailsGate() is false
	VerdictOKAsymmetricAllowed
	// VerdictAsymmetric means delivered both ways along different paths, and
	// the pair is not allowed to be. WireGuard's cryptokey routing and stateful
	// firewalls both break on this.
	//
	// Example:
	//
	//	sim.VerdictAsymmetric.String() // "ASYMMETRIC"; FailsGate() is true
	VerdictAsymmetric
	// VerdictNoReturn means the forward packet arrived but the reply could not
	// get back.
	//
	// Example:
	//
	//	sim.VerdictNoReturn.String() // "NO_RETURN"; FailsGate() is true
	VerdictNoReturn
	// VerdictBlackHole means the forward packet reached a node with no route.
	//
	// Example:
	//
	//	sim.VerdictBlackHole.String() // "BLACK_HOLE"; FailsGate() is true
	VerdictBlackHole
	// VerdictLoop means a packet looped, forward or on the way back.
	//
	// Example:
	//
	//	sim.VerdictLoop.String() // "LOOP"; FailsGate() is true
	VerdictLoop
	// VerdictLinkDown means an active route uses a link that is down: failover
	// did not happen.
	//
	// Example:
	//
	//	sim.VerdictLinkDown.String() // "LINK_DOWN"; FailsGate() is true
	VerdictLinkDown
	// VerdictWrongNeighbour means a route points at a node that is not adjacent:
	// the tables and the graph disagree.
	//
	// Example:
	//
	//	sim.VerdictWrongNeighbour.String() // "WRONG_NEIGHBOUR"; FailsGate() is true
	VerdictWrongNeighbour
	// VerdictTTLExceeded means a packet ran out of hops without looping, because
	// its path is longer than the limit.
	//
	// Example:
	//
	//	sim.VerdictTTLExceeded.String() // "TTL_EXCEEDED"; FailsGate() is true
	VerdictTTLExceeded
	// VerdictUnexpectedDelivery means a negative flow, one that must not be
	// delivered anywhere, was delivered.
	//
	// Example:
	//
	//	sim.VerdictUnexpectedDelivery.String() // "UNEXPECTED_DELIVERY"; FailsGate() is true
	VerdictUnexpectedDelivery
	// VerdictNoHealthyPath means the routing engine found no healthy path
	// between the two nodes, so failing to deliver is a consequence of the
	// current link health and not a planning error.
	//
	// Example:
	//
	//	sim.VerdictNoHealthyPath.String() // "NO_HEALTHY_PATH"; FailsGate() is false
	VerdictNoHealthyPath
	// VerdictUnreachableExpected means a negative flow was correctly not
	// delivered.
	//
	// Example:
	//
	//	sim.VerdictUnreachableExpected.String() // "UNREACHABLE_EXPECTED"; FailsGate() is false
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
//
// Example:
//
//	VerdictNoReturn.String() returns "NO_RETURN".
func (verdict Verdict) String() string {
	if name, ok := verdictNames[verdict]; ok {
		return name
	}
	return "UNKNOWN"
}

// ParseVerdict converts the text String returns back into a Verdict. Anything
// else returns VerdictUnknown and false.
//
// Example:
//
//	ParseVerdict("NO_RETURN") returns VerdictNoReturn, true.
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
//
// Example:
//
//	VerdictOK.MarshalText() returns "OK"; the zero Verdict returns an INVALID_VALUE error.
func (verdict Verdict) MarshalText() ([]byte, error) {
	if _, ok := verdictNames[verdict]; !ok {
		return nil, types.Errf(types.ErrInvalidValue, "verdict", "cannot write unknown verdict %d", int(verdict))
	}
	return []byte(verdict.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
//
// Example:
//
//	UnmarshalText("LOOP") sets the receiver to VerdictLoop; "loop" returns an INVALID_VALUE error.
func (verdict *Verdict) UnmarshalText(text []byte) error {
	parsed, ok := ParseVerdict(string(text))
	if !ok {
		return types.Errf(types.ErrInvalidValue, "verdict", "unknown verdict %q", text)
	}
	*verdict = parsed
	return nil
}

// FailsGate reports whether the verdict stops a plan from being applied.
//
// These pass: OK, OK_ASYMMETRIC_ALLOWED (with a warning), UNREACHABLE_EXPECTED,
// and NO_HEALTHY_PATH, which is the honest result when links have failed and no
// path is left. Everything else fails, including an unknown verdict.
//
// Example:
//
//	VerdictBlackHole.FailsGate() is true; VerdictOK.FailsGate() is false.
func (verdict Verdict) FailsGate() bool {
	switch verdict {
	case VerdictOK, VerdictOKAsymmetricAllowed, VerdictUnreachableExpected, VerdictNoHealthyPath:
		return false
	}
	return true
}

// Warning reports whether the verdict passes the gate but deserves attention:
// delivered, but along different paths that were explicitly allowed to differ.
//
// Example:
//
//	VerdictOKAsymmetricAllowed.Warning() is true; VerdictOK.Warning() is false.
func (verdict Verdict) Warning() bool { return verdict == VerdictOKAsymmetricAllowed }

// Packet is one simulated packet.
//
// Example:
//
//	packet := sim.Packet{Src: netip.MustParseAddr("10.42.16.1"), Dst: netip.MustParseAddr("10.42.33.7"), TTL: sim.DefaultTTL}
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
//
// Example:
//
//	for _, hop := range trace.Hops {
//		fmt.Println(hop.Node, hop.Result)
//	}
type Hop struct {
	// Node is where the packet was.
	Node types.NodeID `json:"node"`
	// Route is the route that matched, a copy of it, or nil if none did.
	Route *route.Route `json:"route,omitempty"`
	// Result is what the node did with the packet.
	Result HopResult `json:"result"`
}

// Trace is the journey of one packet.
//
// Example:
//
//	trace := net.Send(packet, "builder-1")
//	fmt.Println(trace.Delivered(), trace.Nodes()) // true [builder-1 dgx-spark]
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
//
// Example:
//
//	a trace that ends with Delivered is Delivered.
func (trace Trace) Delivered() bool { return trace.Final == Delivered }

// Nodes returns the nodes the packet visited, in order.
//
// Example:
//
//	a trace through builder-1, aws-hub and dgx-spark returns those three, in order.
func (trace Trace) Nodes() []types.NodeID {
	nodes := make([]types.NodeID, len(trace.Hops))
	for i, hop := range trace.Hops {
		nodes[i] = hop.Node
	}
	return nodes
}

// Links returns the links the packet crossed, in order: the link of every hop
// that forwarded it.
//
// Example:
//
//	the same trace returns aws-hub~builder-1 and aws-hub~dgx-spark.
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
//
// Example:
//
//	the same trace has 2 crossings.
func (trace Trace) Crossings() int { return len(trace.Links()) }

// ProblemKind names a kind of problem a flow can have.
//
// Example:
//
//	for _, problem := range result.Problems {
//		if problem.Kind == sim.ProblemLoop { /* routes point at each other */ }
//	}
type ProblemKind string

const (
	// ProblemBlackHole is a packet dropped for want of a route.
	//
	// Example:
	//
	//	problem.Kind == sim.ProblemBlackHole // "black_hole"
	ProblemBlackHole ProblemKind = "black_hole"
	// ProblemLoop is a packet that came back to a node it had visited.
	//
	// Example:
	//
	//	problem.Kind == sim.ProblemLoop // "loop"
	ProblemLoop ProblemKind = "loop"
	// ProblemLinkDown is a route that uses a link that is down.
	//
	// Example:
	//
	//	problem.Kind == sim.ProblemLinkDown // "link_down"
	ProblemLinkDown ProblemKind = "link_down"
	// ProblemWrongNeighbour is a route to a node that is not adjacent.
	//
	// Example:
	//
	//	problem.Kind == sim.ProblemWrongNeighbour // "wrong_neighbour"
	ProblemWrongNeighbour ProblemKind = "wrong_neighbour"
	// ProblemTTLExceeded is a packet that ran out of hops.
	//
	// Example:
	//
	//	problem.Kind == sim.ProblemTTLExceeded // "ttl_exceeded"
	ProblemTTLExceeded ProblemKind = "ttl_exceeded"
	// ProblemAsymmetric is a return path that is not the forward path reversed.
	//
	// Example:
	//
	//	problem.Kind == sim.ProblemAsymmetric // "asymmetric"
	ProblemAsymmetric ProblemKind = "asymmetric"
	// ProblemUnexpectedDelivery is a negative flow that was delivered.
	//
	// Example:
	//
	//	problem.Kind == sim.ProblemUnexpectedDelivery // "unexpected_delivery"
	ProblemUnexpectedDelivery ProblemKind = "unexpected_delivery"
)

// Problem is one thing wrong with a flow, with where and which way.
//
// Example:
//
//	for _, problem := range result.Problems {
//		fmt.Println(problem.Kind, problem.Direction, problem.Node, problem.Detail)
//	}
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
//
// Example:
//
//	if flow.Family == sim.FamilyNegative { /* delivery would be a failure */ }
type Family int

const (
	// FamilyUnknown is the zero value: no family was set.
	//
	// Example:
	//
	//	sim.FamilyUnknown.String() // "unknown"
	FamilyUnknown Family = iota
	// FamilyOverlay is one node's overlay address to another's.
	//
	// Example:
	//
	//	sim.FamilyOverlay.String() // "overlay"
	FamilyOverlay
	// FamilySite is one site's representative address to another site's.
	//
	// Example:
	//
	//	sim.FamilySite.String() // "site"
	FamilySite
	// FamilyDeny is a deny intent. Phase 1 reports it as reachable, because
	// policy is not enforced yet, so that Phase 3 has the list.
	//
	// Example:
	//
	//	sim.FamilyDeny.String() // "deny"
	FamilyDeny
	// FamilyNegative is a packet to an address outside the supernet, which must
	// not be delivered anywhere.
	//
	// Example:
	//
	//	sim.FamilyNegative.String() // "negative"
	FamilyNegative
)

// familyNames holds the text of each family.
var familyNames = map[Family]string{
	FamilyOverlay: "overlay", FamilySite: "site", FamilyDeny: "deny", FamilyNegative: "negative",
}

// String returns "overlay", "site", "deny", "negative" or "unknown".
//
// Example:
//
//	FamilySite.String() returns "site".
func (family Family) String() string {
	if name, ok := familyNames[family]; ok {
		return name
	}
	return "unknown"
}

// MarshalText writes the family as its name. An unknown family returns an error.
//
// Example:
//
//	FamilyDeny.MarshalText() returns "deny"; the zero Family returns an INVALID_VALUE error.
func (family Family) MarshalText() ([]byte, error) {
	if _, ok := familyNames[family]; !ok {
		return nil, types.Errf(types.ErrInvalidValue, "flow family", "cannot write unknown flow family %d", int(family))
	}
	return []byte(family.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
//
// Example:
//
//	UnmarshalText("negative") sets the receiver to FamilyNegative; "odd" returns an INVALID_VALUE error.
func (family *Family) UnmarshalText(text []byte) error {
	for candidate, name := range familyNames {
		if name == string(text) {
			*family = candidate
			return nil
		}
	}
	return types.Errf(types.ErrInvalidValue, "flow family", "unknown flow family %q", text)
}

// Flow is one thing to test: a packet from one address to another, injected at a
// node, with the reply expected back.
//
// Example:
//
//	flow := sim.Flow{Label: "builder-1 -> dgx-spark", Family: sim.FamilyOverlay,
//		Src: netip.MustParseAddr("10.42.240.2"), Dst: netip.MustParseAddr("10.42.240.3"),
//		From: "builder-1", To: "dgx-spark"}
//	result := net.Run(flow, false)
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
//
// Example:
//
//	a flow of FamilyNegative is Negative; an overlay flow is not.
func (flow Flow) Negative() bool { return flow.Family == FamilyNegative }

// FlowResult is the outcome of one flow.
//
// Example:
//
//	result := net.Run(flow, false)
//	fmt.Println(result.Verdict, result.Symmetric) // OK true
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
//
// Example:
//
//	for _, entry := range report.Summary.Verdicts {
//		fmt.Println(entry.Verdict, entry.Count) // OK 13
//	}
type VerdictCount struct {
	// Verdict is the verdict.
	Verdict Verdict `json:"verdict"`
	// Count is how many flows have it.
	Count int `json:"count"`
}

// ProblemCount is how many problems of a kind there were.
//
// Example:
//
//	for _, entry := range report.Summary.Problems {
//		fmt.Println(entry.Kind, entry.Count) // asymmetric 2
//	}
type ProblemCount struct {
	// Kind is the kind of problem.
	Kind ProblemKind `json:"kind"`
	// Count is how many there were.
	Count int `json:"count"`
}

// Summary counts a report's flows.
//
// Example:
//
//	s := report.Summary
//	fmt.Println(s.Total, s.Failed, s.Warnings, s.Count(sim.VerdictOK))
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
//
// Example:
//
//	for a summary of 13 OK and 3 UNREACHABLE_EXPECTED, Count(VerdictOK) is 13 and
//	Count(VerdictLoop) is 0.
func (summary Summary) Count(verdict Verdict) int {
	for _, entry := range summary.Verdicts {
		if entry.Verdict == verdict {
			return entry.Count
		}
	}
	return 0
}

// Report is the outcome of simulating a set of flows.
//
// Example:
//
//	report := sim.NewReport(results)
//	if !report.Passed { /* at least one flow fails the gate */ }
type Report struct {
	// Flows holds every flow's result, sorted by label.
	Flows []FlowResult `json:"flows"`
	// Summary counts the flows.
	Summary Summary `json:"summary"`
	// Passed is true if no flow fails the gate.
	Passed bool `json:"passed"`
}

// fingerprint writes every field of a flow result as text, following pointers,
// so two results that share a label still sort the same way whatever order they
// came in. Every string is quoted, so no text inside a field (a note holding
// "|", say) can make two different results write the same fingerprint, and
// every slice says whether it is nil, because a nil slice and an empty one are
// different values that Report.JSON writes differently (null and []). Equal
// fingerprints therefore mean equal results. It is only a sort key.
func fingerprint(result FlowResult) string {
	var out strings.Builder
	flow := result.Flow
	fmt.Fprintf(&out, "%q|%d|%s|%s|%q|%q|%v|%v|ports %s %v|%q|", flow.Label, flow.Family, flow.Src, flow.Dst, flow.From, flow.To,
		flow.AllowAsymmetric, flow.Intent, nilness(flow.Ports == nil), flow.Ports, flow.Note)
	fmt.Fprintf(&out, "%d|%v|", result.Verdict, result.Symmetric)
	for _, trace := range []*Trace{&result.Forward, result.Return} {
		if trace == nil {
			out.WriteString("no trace|")
			continue
		}
		fmt.Fprintf(&out, "%s>%s ttl %d flow %q final %d hops %s %d:", trace.Packet.Src, trace.Packet.Dst,
			trace.Packet.TTL, trace.Packet.Flow, trace.Final, nilness(trace.Hops == nil), len(trace.Hops))
		for _, hop := range trace.Hops {
			fmt.Fprintf(&out, "%q %d", hop.Node, hop.Result)
			if r := hop.Route; r != nil {
				fmt.Fprintf(&out, " route %s %q %q %d %d path %s %q %v", r.Prefix, r.NextHop, r.Via, r.Metric, r.Source,
					nilness(r.Path == nil), r.Path, r.Active)
			}
			out.WriteString(";")
		}
		out.WriteString("|")
	}
	fmt.Fprintf(&out, "problems %s %d:", nilness(result.Problems == nil), len(result.Problems))
	for _, problem := range result.Problems {
		fmt.Fprintf(&out, "%q %q %q %q;", problem.Kind, problem.Direction, problem.Node, problem.Detail)
	}
	return out.String()
}

// nilness writes "nil" for a nil slice and "set" for any other, so a fingerprint
// tells a nil slice from an empty one.
func nilness(isNil bool) string {
	if isNil {
		return "nil"
	}
	return "set"
}

// sortResults sorts flow results in place by label, and results that share a
// label by their fingerprint, so the order is total and never depends on the
// input's. Fingerprints are built only for results whose label ties with
// another's, and at most once each, so a report of uniquely labelled flows
// formats nothing.
func sortResults(flows []FlowResult) {
	slices.SortStableFunc(flows, func(first, second FlowResult) int {
		return strings.Compare(first.Flow.Label, second.Flow.Label)
	})
	for start := 0; start < len(flows); {
		end := start + 1
		for end < len(flows) && flows[end].Flow.Label == flows[start].Flow.Label {
			end++
		}
		if end-start > 1 {
			sortByFingerprint(flows[start:end])
		}
		start = end
	}
}

// sortByFingerprint sorts a run of results that share a label by their
// fingerprints, building each fingerprint once.
func sortByFingerprint(run []FlowResult) {
	type keyed struct {
		// key is the result's fingerprint.
		key string
		// result is the flow result itself.
		result FlowResult
	}
	pairs := make([]keyed, len(run))
	for i, result := range run {
		pairs[i] = keyed{key: fingerprint(result), result: result}
	}
	slices.SortFunc(pairs, func(first, second keyed) int { return strings.Compare(first.key, second.key) })
	for i, pair := range pairs {
		run[i] = pair.result
	}
}

// NewReport gathers flow results into a report: sorted by label (results that
// share a label by everything else in them, so the order never depends on the
// input's), counted, and marked passed if no verdict fails the gate. The input is not changed. A report
// of no flows has passed vacuously; its Summary.Total says it tested nothing.
//
// Example:
//
//	results with verdicts OK, OK and NO_RETURN give a summary of 3 flows,
//	1 failed, and Passed false.
func NewReport(results []FlowResult) Report {
	flows := slices.Clone(results)
	if flows == nil {
		flows = []FlowResult{}
	}
	sortResults(flows)
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
