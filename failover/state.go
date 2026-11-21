package failover

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"slices"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// Schema is the format version of a saved State. ParseState refuses any other
// with UNSUPPORTED_SCHEMA.
//
// Example:
//
//	failover.Schema is "beaver.failover/v1", the "schema" of every saved state.
const Schema = "beaver.failover/v1"

// State is everything a restart must not forget: how far each link can be
// trusted, the hold-down, pin and flap timers that go with it, and the routing
// tables that are installed. Evaluate takes the last State and returns the next;
// JSON and ParseState write it out and read it back, so the control plane, or an
// agent, can stop at any point and carry on later with the same answer.
//
// The zero State is a valid start: nothing is known about any link, and Evaluate
// fills it in from the network.
//
// Example:
//
//	state := failover.State{}
//	result, _ := failover.Evaluate(network, state, signals, now)
//	saved, _ := result.State.JSON()
type State struct {
	// Schema is Schema. It is empty on a State that was never saved or returned
	// by Evaluate.
	//
	// Example:
	//
	//	"beaver.failover/v1".
	Schema string `json:"schema"`
	// EvaluatedAt is the time of the last evaluation.
	//
	// Example:
	//
	//	10:00:50 when the last Evaluate was given that time.
	EvaluatedAt time.Time `json:"evaluated_at,omitzero"`
	// Links holds the health state of every link, sorted by link ID.
	//
	// Example:
	//
	//	one health.Link for each of the three links of the three-site network.
	Links []*health.Link `json:"links"`
	// Tables are the routing tables in force, which after a rejected failover
	// are the ones from before it.
	//
	// Example:
	//
	//	state.Tables["aws-hub"] is the hub's installed table.
	Tables route.Tables `json:"tables,omitempty"`
	// TablesDown lists the links that were marked down when Tables were made,
	// sorted. The guard judges a new set of tables against these tables on that
	// network, so after a rejected failover the baseline stays the one the
	// kept tables were built for and not the health that has since moved on.
	//
	// Example:
	//
	//	["builder-1~dgx-spark"] when the installed tables were made with the direct link down.
	TablesDown []types.LinkID `json:"tables_down,omitempty"`
	// Unreachable lists the pairs of nodes that had no path when the tables
	// were made, so a later simulation of them judges a cut-off flow as cut off
	// and not as a black hole.
	//
	// Example:
	//
	//	builder-1 to aws-hub when builder-1 was cut off.
	Unreachable []route.Pair `json:"unreachable,omitempty"`
}

// links returns copies of the state's links, so a caller can fold signals into
// them without touching the State it was given.
func (s State) links() []*health.Link {
	out := make([]*health.Link, len(s.Links))
	for i, l := range s.Links {
		out[i] = l.Clone()
	}
	return out
}

// Link returns the health of the link with the given ID, and false if the state
// holds none. The link is the State's own: do not change it.
//
// Example:
//
//	l, ok := state.Link("builder-1~dgx-spark")
//	if ok && l.State == health.Down { ... }
func (s State) Link(id types.LinkID) (*health.Link, bool) {
	for _, l := range s.Links {
		if l.ID == id {
			return l, true
		}
	}
	return nil, false
}

// JSON writes the state as indented JSON with a final newline. Links are sorted
// by ID and routes by their order, and every time is written in UTC, so the same
// state always gives the same bytes. It does not change the State. It returns
// UNSUPPORTED_SCHEMA for a state whose Schema is neither empty nor Schema, and
// INVALID_TOPOLOGY for one that ParseState would refuse.
//
// Example:
//
//	data, err := result.State.JSON()
//	// the file the control plane writes before it stops
func (s State) JSON() ([]byte, error) {
	if s.Schema != "" && s.Schema != Schema {
		return nil, types.Errf(types.ErrUnsupportedSchema, "failover state", "schema %q is not %q", s.Schema, Schema)
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	out := State{Schema: Schema, EvaluatedAt: s.EvaluatedAt.UTC(), Tables: s.Tables, TablesDown: s.TablesDown, Unreachable: s.Unreachable}
	if out.Links = s.links(); out.Links == nil {
		out.Links = []*health.Link{}
	}
	slices.SortFunc(out.Links, func(a, b *health.Link) int { return cmp.Compare(a.ID, b.ID) })
	for _, l := range out.Links {
		utc(l)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, types.Errf(types.ErrInvalidValue, "failover state", "writing it: %v", err)
	}
	return append(data, '\n'), nil
}

// utc rewrites every time in the link in UTC, so its JSON does not depend on the
// time zone the times were made in.
func utc(l *health.Link) {
	l.PinnedUntil, l.LastAdmin = l.PinnedUntil.UTC(), l.LastAdmin.UTC()
	for i := range l.Flaps {
		l.Flaps[i] = l.Flaps[i].UTC()
	}
	for _, d := range l.Dirs {
		d.HoldDownUntil, d.LastEvidence, d.LastSignal = d.HoldDownUntil.UTC(), d.LastEvidence.UTC(), d.LastSignal.UTC()
	}
}

// check reports every way a state cannot be right at once: a link with no ID or
// that is not between exactly two nodes, a link listed twice, and a table filed
// under the wrong node (INVALID_TOPOLOGY).
func (s State) check() error {
	var problems types.List
	seen := map[types.LinkID]bool{}
	for _, l := range s.Links {
		switch {
		case l == nil || l.ID == "":
			problems.Add(types.Errf(types.ErrInvalidTopology, "failover state", "a link has no ID"))
			continue
		case seen[l.ID]:
			problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(l.ID), "it is listed more than once"))
		case len(l.Dirs) != 2:
			problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(l.ID), "it has %d directions, want 2", len(l.Dirs)))
		}
		seen[l.ID] = true
		for node, d := range l.Dirs {
			if node == "" || d == nil {
				problems.Add(types.Errf(types.ErrInvalidTopology, "link "+string(l.ID), "a direction has no node or no state"))
			}
		}
	}
	for _, node := range s.Tables.Nodes() {
		if table := s.Tables[node]; table == nil || table.Node != node {
			problems.Add(types.Errf(types.ErrInvalidTopology, "tables", "the table filed under %q is not that node's", node))
		}
	}
	return problems.Err()
}

// ParseState reads a state written by State.JSON. It refuses text that is not
// JSON, has a field it does not know, or follows more than one value
// (INVALID_VALUE); a schema other than Schema (UNSUPPORTED_SCHEMA); and a state
// that is not coherent (INVALID_TOPOLOGY, every problem at once). Times, hold-down
// timers, pins and flap records come back exactly as they were written, which is
// what lets a restart carry on a hold-down instead of starting it again.
//
// Example:
//
//	state, err := failover.ParseState(saved)
//	if err != nil { return err }
//	result, err := failover.Evaluate(network, state, signals, now)
func ParseState(data []byte) (State, error) {
	var s State
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return State{}, types.Errf(types.ErrInvalidValue, "failover state", "reading it: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return State{}, types.Errf(types.ErrInvalidValue, "failover state", "there is more than one value in it, or something after the end of it")
	}
	if s.Schema != Schema {
		return State{}, types.Errf(types.ErrUnsupportedSchema, "failover state", "schema %q is not %q", s.Schema, Schema)
	}
	if err := s.check(); err != nil {
		return State{}, err
	}
	for _, table := range s.Tables {
		table.Sort()
	}
	return s, nil
}
