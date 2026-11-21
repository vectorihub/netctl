package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// The simulation rules: whether packets get where they are sent, as planned and when
// something fails.
func init() {
	register("V-SIM-001", Error, "the baseline simulation passes", baselinePasses)
	register("V-SIM-002", Error, "every declared scenario's expectations hold", scenariosHold)
	registerSlow("V-SIM-003", Warning, "a single failure isolates a node", singleFailureSweep)
}

// baselinePasses sends every flow over the tables as they are, with the links health has
// marked down, and reports each flow that fails the gate: a black hole, a loop, a reply
// that does not come back, a route over a dead link, an unapproved asymmetry. The packet's
// journey is attached to the finding. A plan with no graph or no flows has nothing to send.
func baselinePasses(p *Plan) []Finding {
	if p.Graph == nil || len(p.Flows) == 0 {
		return nil
	}
	report := sim.Simulate(p.Graph, p.Tables, p.Input, p.Unreachable, p.Flows)
	var out []Finding
	for _, r := range report.Flows {
		if !r.Verdict.FailsGate() {
			continue
		}
		msg := fmt.Sprintf("%s is %s", r.Flow.Label, r.Verdict)
		if len(r.Problems) > 0 {
			msg += ": " + r.Problems[0].Detail
		}
		single := sim.NewReport([]sim.FlowResult{r})
		out = append(out, Finding{Subject: r.Flow.Label, Message: msg, Hint: "the routes do not deliver this flow; check the routes it passes",
			Detail: strings.TrimRight(single.Text(sim.TextOptions{Verbose: true}), "\n")})
	}
	return out
}

// scenariosHold runs each declared scenario and reports every expectation it does not
// meet, and every scenario that cannot run (a link or node it names that is not there).
func scenariosHold(p *Plan) []Finding {
	if p.Graph == nil || len(p.Scenarios) == 0 {
		return nil
	}
	in := sim.ScenarioInput{Graph: p.Graph, Input: p.Input, Flows: p.Flows}
	var out []Finding
	for _, sc := range p.Scenarios {
		result, err := sim.RunScenario(in, sc)
		if err != nil {
			out = append(out, Finding{Subject: sc.Name, Message: "the scenario cannot run: " + strings.ReplaceAll(err.Error(), "\n", "; "), Hint: "fix the scenario"})
			continue
		}
		for _, m := range result.Mismatches {
			out = append(out, Finding{Subject: sc.Name, Message: m.Detail, Hint: "the plan does not behave as the scenario says it should"})
		}
	}
	return out
}

// isolated returns the nodes that, in the computed routes, can reach none of the other
// live nodes, sorted: a node every pair from which is unreachable. Failed nodes are not
// counted, as live or as isolated.
func isolated(nodes []types.NodeID, unreachable []route.Pair, failed []types.NodeID) []types.NodeID {
	cut := map[[2]types.NodeID]bool{}
	for _, pair := range unreachable {
		cut[[2]types.NodeID{pair.From, pair.To}] = true
	}
	var live []types.NodeID
	for _, n := range nodes {
		if !slices.Contains(failed, n) {
			live = append(live, n)
		}
	}
	var out []types.NodeID
	for _, n := range live {
		others, cutOff := 0, 0
		for _, m := range live {
			if m == n {
				continue
			}
			others++
			if cut[[2]types.NodeID{n, m}] {
				cutOff++
			}
		}
		if others > 0 && cutOff == others {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// singleFailureSweep recomputes the routes once with each link down and once with each
// hub down, and warns about every node that the failure leaves with no way to any other
// node, that was not already cut off before. A node that a link failure isolates is
// reported against that link. A hub failure is reported against the hub, and only for
// nodes no link failure already isolates: losing the hub as well as losing its link is the
// same weakness once.
func singleFailureSweep(p *Plan) []Finding {
	if p.Graph == nil || p.Topology == nil {
		return nil
	}
	nodes := p.Graph.Nodes()
	base, err := route.Compute(p.Graph, p.Input)
	if err != nil {
		return nil
	}
	already := isolated(nodes, base.Unreachable, nil)
	var out []Finding
	byLink := map[types.NodeID]bool{}
	links := slices.Clone(p.Topology.Links)
	slices.SortFunc(links, func(a, b topology.Link) int {
		return strings.Compare(string(types.LinkIDOf(a.A, a.B)), string(types.LinkIDOf(b.A, b.B)))
	})
	for _, l := range links {
		id := types.LinkIDOf(l.A, l.B)
		res, err := route.Compute(sim.Degrade(p.Graph, []types.LinkID{id}, nil), p.Input)
		if err != nil {
			continue
		}
		for _, n := range isolated(nodes, res.Unreachable, nil) {
			if slices.Contains(already, n) {
				continue
			}
			byLink[n] = true
			out = append(out, Finding{Subject: string(id), Message: fmt.Sprintf("if the link fails, %s is isolated: it can reach no other node", n),
				Hint: "add a second path for the node, or suppress this warning with the reason"})
		}
	}
	for _, h := range hubs(p.Topology) {
		res, err := route.Compute(sim.Degrade(p.Graph, nil, []types.NodeID{h}), p.Input)
		if err != nil {
			continue
		}
		for _, n := range isolated(nodes, res.Unreachable, []types.NodeID{h}) {
			if slices.Contains(already, n) || byLink[n] {
				continue
			}
			out = append(out, Finding{Subject: string(h), Message: fmt.Sprintf("if the hub fails, %s is isolated: it can reach no other node", n),
				Hint: "give the node a path that does not depend on the hub"})
		}
	}
	return out
}
