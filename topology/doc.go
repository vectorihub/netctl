// Package topology models what a network is made of: sites, nodes, links and
// intents. It reads that description from YAML bytes and, later, checks it and
// exposes the graph that routing walks.
//
// # The model
//
// A Topology is plain data (types.go). Build one in Go code or read one with
// Decode; the rest of netctl cannot tell the difference (D7). Roles and link
// kinds come from package types. A Link is one object with two ends, so it
// cannot be declared on one side only.
//
// # Loading
//
// Decode is strict. It refuses input over a size limit, a document whose
// aliases expand without bound, unknown fields, repeated fields, wrong kinds of
// value and unknown names, and it reports every problem at once, each with the
// file name and line. An unknown field that is close to a real one says which:
// "rol" suggests "role". Decode then normalises the result (see Normalize), so
// the same network always decodes to the same Topology. Decode checks the file's
// shape, not the network's meaning; that is Check.
//
// # Rules
//
// Check tests a Topology against rules T1 to T10 (schema, IDs, sites, link ends,
// duplicate links, hub-spoke ends, a hub exists, NAT traversal, spokes reach a
// hub, selectors match) and reports every violation at once as a Report of
// Findings, each naming its rule. T8 is a warning; the rest are errors. T11,
// unknown fields, is Decode's. Load is Decode followed by Check.
//
// # Normal form
//
// Normalize fills in defaults in one place (link cost and health), stores each
// link with A before B and gives it its derived ID, and sorts sites, nodes and
// links by ID. Intents keep their order, because order matters for policy. Call
// it on a Topology built in code; Decode has already called it.
//
// # Selectors
//
// A Selector picks nodes: "*", "node:ID" (or a bare ID), "site:ID" and
// "label:key=value". ParseSelector reads the text; Matches and Resolve say which
// nodes it picks, in sorted order.
//
// # Per-node adjacency
//
// Some sources list each node's peers instead of links. A node may give "peers"
// in YAML (Node.Peers in code), and ImportPeers, which Decode calls, turns the
// lists into links, deriving the kind from the roles. It refuses a pair listed
// by one side only, so a one-sided link cannot get in.
//
// # Graph
//
// BuildGraph turns a Topology and a view of which links are usable into the
// adjacency view that routing and the simulator walk: two directed edges per
// link, each node's edges sorted by (To, Link). It is a snapshot.
//
// # Hash
//
// Hash fingerprints the canonical form of a Topology, so a plan can name the
// exact input it came from. Reordering, comments, whitespace and the links-or-
// peers choice do not change it; any change to the network does.
package topology
