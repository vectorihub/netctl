package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// Names of the files Render produces, relative to the output directory.
const (
	// FilePlan is the summary of the plan: its ID, verdict, changes and decisions.
	//
	// Example:
	//
	//	result.Render()[plan.FilePlan].
	FilePlan = "plan.json"
	// FileIPAM is the address plan to save for the next plan.
	//
	// Example:
	//
	//	result.Render()[plan.FileIPAM].
	FileIPAM = "ipam.json"
	// FileRoutes is every node's routing table as text.
	//
	// Example:
	//
	//	result.Render()[plan.FileRoutes].
	FileRoutes = "routes.txt"
	// FileValidation is the validator's report.
	//
	// Example:
	//
	//	result.Render()[plan.FileValidation].
	FileValidation = "validation.json"
	// FileSimulation is the simulation report.
	//
	// Example:
	//
	//	result.Render()[plan.FileSimulation].
	FileSimulation = "simulation.json"
	// DirNodes is the directory of node configs, one file named after each node.
	//
	// Example:
	//
	//	result.Render()["nodes/builder-1.json"].
	DirNodes = "nodes"
)

// summary is the content of plan.json: what a person or a script wants to know about a
// plan before opening anything else.
type summary struct {
	// PlanID names the plan; it is empty when there are no configs.
	PlanID string `json:"plan_id"`
	// OK says the validator found no errors.
	OK bool `json:"ok"`
	// Nodes lists the nodes that have a config, sorted.
	Nodes []types.NodeID `json:"nodes"`
	// Unusable lists the links not in use.
	Unusable []types.LinkID `json:"unusable"`
	// Unreachable lists the pairs with no healthy path.
	Unreachable []route.Pair `json:"unreachable"`
	// Changes are the changes to the address plan.
	Changes []Change `json:"changes"`
	// Decisions are what the planner decided that the caller must know.
	Decisions []Decision `json:"decisions"`
	// Files lists every file of the rendering, sorted.
	Files []string `json:"files"`
}

// indented marshals a value as indented JSON with a final newline, the one layout every
// JSON file of a rendering has.
func indented(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Render returns the bytes of every output file of the plan, keyed by the path relative to
// the output directory: plan.json, ipam.json, routes.txt, validation.json, simulation.json
// and nodes/<node>.json. It does no I/O: the caller decides where to write, and every
// consumer writes the same bytes. The same Result always renders the same bytes. A result
// with no configs (a plan that was refused and had nothing to keep) renders no node files,
// and a caller that writes files should not write any for a plan that returned an error.
//
// Example:
//
//	files, err := result.Render()
//	for path, data := range files { write(filepath.Join(dir, path), data) }
func (r Result) Render() (map[string][]byte, error) {
	files := map[string][]byte{}
	var err error
	if files[FileIPAM], err = ipam.Marshal(r.IPAM); err != nil {
		return nil, fmt.Errorf("render %s: %w", FileIPAM, err)
	}
	if files[FileValidation], err = r.Validation.JSON(); err != nil {
		return nil, fmt.Errorf("render %s: %w", FileValidation, err)
	}
	if files[FileSimulation], err = r.Simulation.JSON(); err != nil {
		return nil, fmt.Errorf("render %s: %w", FileSimulation, err)
	}
	files[FileRoutes] = []byte(r.RoutesText())
	for _, c := range r.Configs {
		data, err := c.Encode()
		if err != nil {
			return nil, fmt.Errorf("render config of %s: %w", c.Node, err)
		}
		files[DirNodes+"/"+string(c.Node)+".json"] = data
	}
	sum := summary{PlanID: r.PlanID, OK: r.Validation.OK, Nodes: []types.NodeID{}, Unusable: nonNil(r.Unusable), Unreachable: nonNil(r.Unreachable),
		Changes: nonNil(r.Changes), Decisions: nonNil(r.Decisions)}
	for _, c := range r.Configs {
		sum.Nodes = append(sum.Nodes, c.Node)
	}
	for path := range files {
		sum.Files = append(sum.Files, path)
	}
	sum.Files = append(sum.Files, FilePlan)
	slices.Sort(sum.Files)
	if files[FilePlan], err = indented(sum); err != nil {
		return nil, fmt.Errorf("render %s: %w", FilePlan, err)
	}
	return files, nil
}

// nonNil returns the slice, or an empty one if it is nil, so that JSON shows [] and not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// RoutesText writes every node's routing table, nodes in order and each node's routes in the
// table's order, one route to a line, with the backups marked. It is the same text however
// the plan was made.
//
// Example:
//
//	node builder-1
//	  10.42.32.0/20  via builder-1~dgx-spark  next-hop dgx-spark  metric 10  active
func (r Result) RoutesText() string {
	var b strings.Builder
	for _, node := range r.Tables.Nodes() {
		fmt.Fprintf(&b, "node %s\n", node)
		table := r.Tables[node]
		for _, rt := range table.Routes {
			state := "backup"
			if rt.Active {
				state = "active"
			}
			switch {
			case rt.Via == "":
				fmt.Fprintf(&b, "  %s  connected  %s\n", rt.Prefix, state)
			default:
				fmt.Fprintf(&b, "  %s  via %s  next-hop %s  metric %d  %s  %s\n", rt.Prefix, rt.Via, rt.NextHop, rt.Metric, rt.Source, state)
			}
		}
	}
	return b.String()
}

// SimulationText writes the simulation report as text, listing the flows that fail the gate
// or warn, and every flow with verbose.
//
// Example:
//
//	fmt.Print(result.SimulationText(false)) ends with PASS on a good plan.
func (r Result) SimulationText(verbose bool) string {
	return r.Simulation.Text(sim.TextOptions{Verbose: verbose})
}

// SimulationJSON writes the simulation report as stable JSON.
//
// Example:
//
//	data, err := result.SimulationJSON()
func (r Result) SimulationJSON() ([]byte, error) { return r.Simulation.JSON() }

// owner says who holds an address in words, from the narrowest allocation that covers it:
// "node builder-1" or "site dgx". It returns "" when nothing in the address plan covers it.
func (r Result) owner(dst netip.Addr) string {
	best := ""
	bits := -1
	for _, a := range r.IPAM.Allocations {
		if a.State == ipam.Allocated && a.Prefix.Contains(dst) && a.Prefix.Bits() > bits {
			best, bits = a.Owner.Kind+" "+a.Owner.ID, a.Prefix.Bits()
		}
	}
	return best
}

// Explain says which route a node uses for an address and why. The destination's holder is
// named from the address plan, and a link's health is shown when the plan was made with the
// failover guard. It fails when the plan has no table for the node.
//
// Example:
//
//	e, err := result.Explain("builder-1", netip.MustParseAddr("10.42.33.7"))
//	fmt.Print(e.String())
func (r Result) Explain(from types.NodeID, dst netip.Addr) (route.Explanation, error) {
	table, ok := r.Tables[from]
	if !ok {
		return route.Explanation{}, types.Errf(types.ErrNotFound, "explain", "the plan has no routing table for node %q", from)
	}
	notes := route.Notes{Owner: r.owner(dst)}
	if r.Failover != nil {
		notes.Health = func(link types.LinkID) (bool, string) {
			l, ok := r.Failover.State.Link(link)
			if !ok {
				return false, ""
			}
			return l.Usable(), l.State.String()
		}
	}
	return route.Explain(table, dst, notes), nil
}

// ExplainText is Explain written as text, the same bytes whoever prints it.
//
// Example:
//
//	text, err := result.ExplainText("builder-1", netip.MustParseAddr("10.42.33.7"))
func (r Result) ExplainText(from types.NodeID, dst netip.Addr) (string, error) {
	e, err := r.Explain(from, dst)
	if err != nil {
		return "", err
	}
	return e.String(), nil
}

// ExplainJSON is Explain written as indented JSON.
//
// Example:
//
//	data, err := result.ExplainJSON("builder-1", netip.MustParseAddr("10.42.33.7"))
func (r Result) ExplainJSON(from types.NodeID, dst netip.Addr) ([]byte, error) {
	e, err := r.Explain(from, dst)
	if err != nil {
		return nil, err
	}
	return indented(e)
}

// Diff compares what two plans would put on the machines, node by node. Neither is changed.
// An empty diff (Empty) means the machines would see no change.
//
// Example:
//
//	d := plan.Diff(before, after)
//	if !d.Empty() { fmt.Print(d.Text()) }
func Diff(old, updated Result) config.PlanDiff { return config.DiffPlans(old.Configs, updated.Configs) }

// DiffJSON is Diff written as indented JSON.
//
// Example:
//
//	data, err := plan.DiffJSON(before, after)
func DiffJSON(old, updated Result) ([]byte, error) {
	var b bytes.Buffer
	data, err := indented(Diff(old, updated))
	if err != nil {
		return nil, err
	}
	b.Write(data)
	return b.Bytes(), nil
}
