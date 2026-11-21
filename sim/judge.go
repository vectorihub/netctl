package sim

import (
	"fmt"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// Symmetric reports whether the return trace is exactly the forward trace
// reversed: the same nodes in the opposite order, and the same links in the
// opposite order. Both packets must have been delivered; a trace that failed is
// not symmetric with anything.
//
// Matching nodes alone is not enough. Two links between the same pair of nodes
// are different paths to WireGuard and to a stateful firewall.
//
// Example:
//
//	forward builder-1, aws-hub, dgx-spark over aws-hub~builder-1 and
//	aws-hub~dgx-spark is symmetric with a return of dgx-spark, aws-hub, builder-1
//	over aws-hub~dgx-spark and aws-hub~builder-1.
func Symmetric(forward, ret Trace) bool {
	if !forward.Delivered() || !ret.Delivered() {
		return false
	}
	nodes, links := ret.Nodes(), ret.Links()
	slices.Reverse(nodes)
	slices.Reverse(links)
	return slices.Equal(forward.Nodes(), nodes) && slices.Equal(forward.Links(), links)
}

// Case is what Judge needs to know about a flow besides its two traces.
//
// Example:
//
//	verdict := sim.Judge(forward, reply, sim.Case{AllowAsymmetric: true})
type Case struct {
	// Negative says the flow must not be delivered anywhere.
	Negative bool
	// Partitioned says the routing engine found no healthy path between the two
	// nodes, so failing to deliver follows from link health and is not a mistake
	// in the plan.
	Partitioned bool
	// AllowAsymmetric says the forward and return paths may differ.
	AllowAsymmetric bool
}

// Judge turns a forward and a return trace into a verdict. The checks run in a
// fixed order, and the order matters: the first that applies wins.
//
//  1. A negative flow that was delivered is UNEXPECTED_DELIVERY, and one that
//     was dropped for want of a route is UNREACHABLE_EXPECTED. Any other
//     failure of a negative flow (a loop, say) is judged below, because it is a
//     real fault and not "correctly not delivered".
//  2. A partitioned pair that did not get through both ways is NO_HEALTHY_PATH,
//     so a node cut off by real failures is a consequence of health, not a
//     planning bug.
//  3. A loop, forward or on the way back, is LOOP.
//  4. A link that is down is LINK_DOWN, a next hop that is not a neighbour is
//     WRONG_NEIGHBOUR, and a packet that ran out of hops is TTL_EXCEEDED. These
//     come before BLACK_HOLE because each explains the failure more precisely.
//  5. A forward packet that was dropped is BLACK_HOLE.
//  6. A forward packet that arrived but whose reply did not is NO_RETURN.
//  7. Delivered both ways along different paths is OK_ASYMMETRIC_ALLOWED if the
//     case allows it, and ASYMMETRIC if not.
//  8. Otherwise OK.
//
// Example:
//
//	forward delivered, return dropped at the hub for want of a route, is
//	NO_RETURN.
func Judge(forward, ret Trace, c Case) Verdict {
	switch {
	case c.Negative && forward.Delivered():
		return VerdictUnexpectedDelivery
	case c.Negative && forward.Final == BlackHole:
		return VerdictUnreachableExpected
	case c.Partitioned && !(forward.Delivered() && ret.Delivered()):
		return VerdictNoHealthyPath
	case forward.Final == LoopDetected || ret.Final == LoopDetected:
		return VerdictLoop
	case forward.Final == LinkDown || ret.Final == LinkDown:
		return VerdictLinkDown
	case forward.Final == WrongNeighbour || ret.Final == WrongNeighbour:
		return VerdictWrongNeighbour
	case forward.Final == TTLExceeded || ret.Final == TTLExceeded:
		return VerdictTTLExceeded
	case !forward.Delivered():
		return VerdictBlackHole
	case !ret.Delivered():
		return VerdictNoReturn
	case !Symmetric(forward, ret) && c.AllowAsymmetric:
		return VerdictOKAsymmetricAllowed
	case !Symmetric(forward, ret):
		return VerdictAsymmetric
	}
	return VerdictOK
}

// kindOf maps a failed hop result to the kind of problem it is.
func kindOf(result HopResult) (ProblemKind, bool) {
	switch result {
	case BlackHole:
		return ProblemBlackHole, true
	case LoopDetected:
		return ProblemLoop, true
	case LinkDown:
		return ProblemLinkDown, true
	case WrongNeighbour:
		return ProblemWrongNeighbour, true
	case TTLExceeded:
		return ProblemTTLExceeded, true
	}
	return "", false
}

// describeFailure explains where and why a trace stopped.
func describeFailure(trace Trace) (types.NodeID, string) {
	last := trace.Hops[len(trace.Hops)-1]
	switch trace.Final {
	case BlackHole:
		return last.Node, fmt.Sprintf("%s has no route for %s", last.Node, trace.Packet.Dst)
	case LoopDetected:
		return last.Node, fmt.Sprintf("the packet came back to %s: %s", last.Node, pathOf(trace))
	case LinkDown:
		if last.Route != nil {
			return last.Node, fmt.Sprintf("%s routes %s over %s, which is down", last.Node, trace.Packet.Dst, last.Route.Via)
		}
		return last.Node, fmt.Sprintf("%s routes %s over a link that is down", last.Node, trace.Packet.Dst)
	case WrongNeighbour:
		if last.Route != nil {
			return last.Node, fmt.Sprintf("%s routes %s to %s over %s, but they are not joined by that link", last.Node, trace.Packet.Dst, last.Route.NextHop, last.Route.Via)
		}
		return last.Node, fmt.Sprintf("%s routes %s to a node it is not joined to", last.Node, trace.Packet.Dst)
	}
	return last.Node, fmt.Sprintf("the packet ran out of hops at %s after %d: %s", last.Node, trace.Packet.TTL, pathOf(trace))
}

// pathOf writes the nodes a trace visited as "a > b > c".
func pathOf(trace Trace) string {
	text := ""
	for i, node := range trace.Nodes() {
		if i > 0 {
			text += " > "
		}
		text += string(node)
	}
	return text
}

// ProblemsOf lists what is wrong with a flow, given its traces and its verdict:
// one problem for each trace that failed, naming the node and the reason, and
// one for a path that is not symmetric. A flow with nothing wrong has none.
//
// The forward trace of a negative flow that was correctly dropped is not a
// problem, and neither is a trace that failed only because the pair is
// partitioned.
//
// Example:
//
//	a forward trace that ended in a black hole at aws-hub gives one problem, of kind
//	black_hole, direction forward, node aws-hub.
func ProblemsOf(forward Trace, ret *Trace, verdict Verdict) []Problem {
	if !verdict.FailsGate() && verdict != VerdictOKAsymmetricAllowed {
		return nil
	}
	var problems []Problem
	add := func(direction string, trace Trace) {
		if trace.Delivered() || len(trace.Hops) == 0 {
			return
		}
		kind, ok := kindOf(trace.Final)
		if !ok {
			return
		}
		node, detail := describeFailure(trace)
		problems = append(problems, Problem{Kind: kind, Direction: direction, Node: node, Detail: detail})
	}
	add("forward", forward)
	if ret != nil {
		add("return", *ret)
	}
	switch verdict {
	case VerdictUnexpectedDelivery:
		var node types.NodeID
		if nodes := forward.Nodes(); len(nodes) > 0 {
			node = nodes[len(nodes)-1]
		}
		problems = append(problems, Problem{Kind: ProblemUnexpectedDelivery, Direction: "forward",
			Node: node, Detail: fmt.Sprintf("%s was delivered, but it must not be delivered anywhere", forward.Packet.Dst)})
	case VerdictAsymmetric, VerdictOKAsymmetricAllowed:
		if ret != nil {
			problems = append(problems, Problem{Kind: ProblemAsymmetric,
				Detail: fmt.Sprintf("forward %s, return %s", pathOf(forward), pathOf(*ret))})
		}
	}
	return problems
}

// Run sends one flow through the network and judges it. The forward packet goes
// from the flow's source, injected at flow.From. If it is delivered, the
// destination replies: a packet from Dst back to Src, injected at flow.To. A
// forward packet that is not delivered gets no reply, because there is nothing
// to reply to, so the result has no return trace.
//
// partitioned says the routing engine found no healthy path between the two
// nodes (see Case). The flow's own Negative and AllowAsymmetric settings are
// used as they are.
//
// Example:
//
//	the overlay flow builder-1 to dgx-spark with the direct link up goes
//	builder-1, dgx-spark and comes back dgx-spark, builder-1: verdict OK.
func (n Net) Run(flow Flow, partitioned bool) FlowResult {
	return n.runCase(flow, Case{
		Negative: flow.Negative(), Partitioned: partitioned, AllowAsymmetric: flow.AllowAsymmetric,
	})
}

// runCase is Run with the judging case given by the caller, so a scenario can
// treat a flow to a failed node as one that must not be delivered without
// changing the flow itself.
func (n Net) runCase(flow Flow, c Case) FlowResult {
	result := FlowResult{Flow: flow}
	result.Forward = n.Send(Packet{Src: flow.Src, Dst: flow.Dst, Flow: flow.Label}, flow.From)
	if result.Forward.Delivered() && !c.Negative {
		reply := n.Send(Packet{Src: flow.Dst, Dst: flow.Src, Flow: flow.Label + " (return)"}, flow.To)
		result.Return = &reply
		result.Symmetric = Symmetric(result.Forward, reply)
	}
	var ret Trace
	if result.Return != nil {
		ret = *result.Return
	}
	result.Verdict = Judge(result.Forward, ret, c)
	result.Problems = ProblemsOf(result.Forward, result.Return, result.Verdict)
	return result
}
