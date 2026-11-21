package config

import (
	"net/netip"

	"github.com/vectorihub/netctl/types"
)

// Schema is the format version of a NodeConfig. Decode refuses any other with
// UNSUPPORTED_SCHEMA.
//
// Example:
//
//	config.Schema is "beaver.netctl/v1alpha1", the "schema" of every config.
const Schema = "beaver.netctl/v1alpha1"

// NodeConfig is the desired network state of one node: what its network should
// look like, written in Beaver's own words and independent of the machine that
// will apply it. It is the contract between the control plane and the data plane.
//
// It says what, never how. There are no interface names, no ip, wg or nft syntax,
// no sysctls, no routing-table numbers, no file paths, and no secrets: a peer's
// public key is named by a reference (public_key_ref), and private keys are made
// on the node and never leave it.
//
// The order of the fields is the order of the keys in the JSON, which fixes the
// bytes and so the hash. Do not reorder them casually: it changes every hash.
// Call Encode to write a config; it sorts every list, fills in Hash and gives the
// bytes to save.
//
// Example:
//
//	cfg.Node is "builder-1", cfg.Role is "spoke", cfg.Forwarding is false, and
//	cfg.Peers has one Peer for the hub and one for dgx-spark.
type NodeConfig struct {
	// Schema is Schema.
	//
	// Example:
	//
	//	"beaver.netctl/v1alpha1".
	Schema string `json:"schema"`
	// PlanID names the plan this config belongs to: the topology's name, an
	// at sign, and the first eight hex digits of a hash of every node's content.
	// It changes whenever any node's config does.
	//
	// Example:
	//
	//	"three-site@7f3c9e1a".
	PlanID string `json:"plan_id"`
	// Node is the node this config is for.
	//
	// Example:
	//
	//	"builder-1".
	Node types.NodeID `json:"node"`
	// Role is "hub" or "spoke".
	//
	// Example:
	//
	//	"spoke".
	Role string `json:"role"`
	// Forwarding says the node passes traffic between other nodes. Only a hub
	// does. The agent derives whatever the machine needs from this; the config
	// does not name a sysctl.
	//
	// Example:
	//
	//	true for aws-hub, false for builder-1.
	Forwarding bool `json:"forwarding"`
	// Overlay is the node's own address on the overlay and its listen port.
	//
	// Example:
	//
	//	Overlay{Address: 10.42.240.2/32, ListenPort: 51820}.
	Overlay Overlay `json:"overlay"`
	// SitePrefix is the address block of the node's site, when the node
	// advertises its site. It is left out of the JSON otherwise.
	//
	// Example:
	//
	//	10.42.16.0/20 for builder-1.
	SitePrefix *netip.Prefix `json:"site_prefix,omitempty"`
	// Advertise lists what the node announces to the overlay, sorted: its site
	// block and its overlay address.
	//
	// Example:
	//
	//	[10.42.16.0/20 10.42.240.2/32] for builder-1.
	Advertise []netip.Prefix `json:"advertise"`
	// Peers lists the nodes this one is directly linked to, sorted by node ID:
	// one for every link the node is an end of.
	//
	// Example:
	//
	//	builder-1 has a peer for aws-hub and one for dgx-spark.
	Peers []Peer `json:"peers"`
	// Routes lists the node's routes, active and backup, sorted. Connected
	// routes are not listed: they are the node's own addresses.
	//
	// Example:
	//
	//	10.42.32.0/20 via dgx-spark, metric 10, active; the same via aws-hub,
	//	metric 40, backup.
	Routes []Route `json:"routes"`
	// Health says what to probe and when to call a link down or back.
	//
	// Example:
	//
	//	Health{FailThreshold: 3, RecoverThreshold: 5, HoldDownSeconds: 60, Probes: ...}.
	Health Health `json:"health"`
	// Policy holds the policy entries that concern this node. It is empty until
	// the policy engine exists, and is written as an empty list.
	//
	// Example:
	//
	//	[] in every config today.
	Policy []PolicyEntry `json:"policy"`
	// Hash is "sha256:" and the hex SHA-256 of the canonical JSON of the config
	// with this field empty. Encode fills it in.
	//
	// Example:
	//
	//	"sha256:3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b".
	Hash string `json:"hash"`
}

// Overlay is a node's own address on the overlay network.
//
// Example:
//
//	Overlay{Address: netip.MustParsePrefix("10.42.240.2/32"), ListenPort: 51820}.
type Overlay struct {
	// Address is the node's overlay address, with its prefix length.
	//
	// Example:
	//
	//	10.42.240.2/32.
	Address netip.Prefix `json:"address"`
	// ListenPort is the UDP port the node listens on.
	//
	// Example:
	//
	//	51820.
	ListenPort int `json:"listen_port"`
}

// PeerRole says what kind of link a peer is on, from the point of view of the node
// the config is for.
//
// Example:
//
//	PeerHub for the hub of a spoke, PeerSpoke for a spoke of a hub, PeerDirect for
//	the other end of a direct link between two spokes.
type PeerRole string

const (
	// PeerHub is a hub reached over a hub-spoke link.
	//
	// Example:
	//
	//	config.PeerHub is "hub".
	PeerHub PeerRole = "hub"
	// PeerSpoke is a spoke reached over a hub-spoke link.
	//
	// Example:
	//
	//	config.PeerSpoke is "spoke".
	PeerSpoke PeerRole = "spoke"
	// PeerDirect is a node reached over a direct link.
	//
	// Example:
	//
	//	config.PeerDirect is "direct".
	PeerDirect PeerRole = "direct"
)

// Peer is a node this one is directly linked to.
//
// Example:
//
//	builder-1's direct peer is dgx-spark: link "builder-1~dgx-spark", no endpoints,
//	needs_traversal, keepalive 25, a probe, and the prefixes that are currently
//	routed through it.
type Peer struct {
	// Node is the peer's node ID.
	//
	// Example:
	//
	//	"aws-hub".
	Node types.NodeID `json:"node"`
	// Link is the link between the two nodes.
	//
	// Example:
	//
	//	"aws-hub~builder-1".
	Link types.LinkID `json:"link"`
	// Role is what kind of peer it is.
	//
	// Example:
	//
	//	PeerHub.
	Role PeerRole `json:"role"`
	// PublicKeyRef names the peer's public key by identity, "node:" and the
	// node ID. The key itself is not in the config.
	//
	// Example:
	//
	//	"node:aws-hub".
	PublicKeyRef string `json:"public_key_ref"`
	// Endpoints are the public addresses to reach the peer on, sorted. It is
	// empty for a peer that is behind NAT.
	//
	// Example:
	//
	//	[203.0.113.10:51820] for aws-hub; [] for dgx-spark.
	Endpoints []netip.AddrPort `json:"endpoints"`
	// NeedsTraversal says neither end can be dialled from outside, so the two
	// have to open the path together. It is left out of the JSON when false.
	//
	// Example:
	//
	//	true for builder-1's peer dgx-spark, both being behind NAT.
	NeedsTraversal bool `json:"needs_traversal,omitempty"`
	// Keepalive is the interval in seconds at which to keep the tunnel open, or 0
	// for none. It is 25 when the node is behind NAT or the peer needs traversal.
	//
	// Example:
	//
	//	25 on a spoke's peers; 0 on the hub's.
	Keepalive int `json:"keepalive_seconds"`
	// Probe is the pair of addresses used to test a direct link. It is left out
	// of the JSON for a link that is not probed.
	//
	// Example:
	//
	//	Probe{Local: 10.42.252.1/30, Remote: 10.42.252.2} for builder-1.
	Probe *Probe `json:"probe,omitempty"`
	// AllowedPrefixes lists, sorted, what may be sent to and received from the
	// peer: the peer's probe address, and every prefix whose active route on this
	// node has the peer as next hop. A prefix is in at most one peer's list,
	// because WireGuard keeps a prefix with one peer; failover moves it.
	//
	// Example:
	//
	//	[10.42.0.0/20 10.42.240.1/32] for builder-1's hub peer.
	AllowedPrefixes []netip.Prefix `json:"allowed_prefixes"`
}

// Probe is the pair of addresses on the transit network of a direct link that its
// two ends probe each other on. They are not moved by failover, so probes keep
// crossing the direct link after its traffic has gone to the hub, and recovery
// can be seen.
//
// Example:
//
//	Probe{Local: netip.MustParsePrefix("10.42.252.1/30"), Remote: netip.MustParseAddr("10.42.252.2")}.
type Probe struct {
	// Local is this node's address on the link, with the link's prefix length.
	//
	// Example:
	//
	//	10.42.252.1/30.
	Local netip.Prefix `json:"local"`
	// Remote is the other end's address on the link.
	//
	// Example:
	//
	//	10.42.252.2.
	Remote netip.Addr `json:"remote"`
}

// Route is one route of the node.
//
// Example:
//
//	Route{Prefix: 10.42.32.0/20, Via: "dgx-spark", Metric: 10, Active: true}.
type Route struct {
	// Prefix is the block of destinations the route covers.
	//
	// Example:
	//
	//	10.42.32.0/20.
	Prefix netip.Prefix `json:"prefix"`
	// Via is the neighbour the traffic goes to.
	//
	// Example:
	//
	//	"dgx-spark".
	Via types.NodeID `json:"via"`
	// Metric is the cost of the route. Lower is better.
	//
	// Example:
	//
	//	10.
	Metric int `json:"metric"`
	// Active says the route is in use.
	//
	// Example:
	//
	//	true for a primary, false for a backup that is waiting.
	Active bool `json:"active"`
	// Backup says the route is a backup for another. It is left out of the JSON
	// when false.
	//
	// Example:
	//
	//	true for the route via aws-hub that backs up the direct one.
	Backup bool `json:"backup,omitempty"`
}

// Health says what to probe and when to call a link down or back up. The agent
// feeds the results, unchanged, to the health package.
//
// Example:
//
//	Health{FailThreshold: 3, RecoverThreshold: 5, HoldDownSeconds: 60,
//	Probes: []ProbeTarget{{Link: "builder-1~dgx-spark", Target: 10.42.252.2, Interval: 5}}}.
type Health struct {
	// Probes lists what to probe, sorted by link.
	//
	// Example:
	//
	//	one entry for each direct link the node is on.
	Probes []ProbeTarget `json:"probes"`
	// FailThreshold is how many failures in a row take a link down.
	//
	// Example:
	//
	//	3.
	FailThreshold int `json:"fail_threshold"`
	// RecoverThreshold is how many good signals in a row bring a link back.
	//
	// Example:
	//
	//	5.
	RecoverThreshold int `json:"recover_threshold"`
	// HoldDownSeconds is how long a link that went down stays down.
	//
	// Example:
	//
	//	60.
	HoldDownSeconds int `json:"hold_down_seconds"`
}

// ProbeTarget is one link to probe, and where.
//
// Example:
//
//	ProbeTarget{Link: "builder-1~dgx-spark", Target: 10.42.252.2, Interval: 5}.
type ProbeTarget struct {
	// Link is the link being tested.
	//
	// Example:
	//
	//	"builder-1~dgx-spark".
	Link types.LinkID `json:"link"`
	// Target is the address to probe, the far end's probe address.
	//
	// Example:
	//
	//	10.42.252.2.
	Target netip.Addr `json:"target"`
	// Interval is the time between probes in seconds.
	//
	// Example:
	//
	//	5.
	Interval int `json:"interval_seconds"`
}

// PolicyEntry is one policy rule that concerns the node. Phase 1 writes none; the
// shape is here so that the schema does not have to change when policy arrives.
//
// Example:
//
//	PolicyEntry{Action: "allow", From: "builder-1", To: "dgx-spark"}.
type PolicyEntry struct {
	// Action is "allow" or "deny".
	//
	// Example:
	//
	//	"deny".
	Action string `json:"action"`
	// From is the selector of the sources.
	//
	// Example:
	//
	//	"site:builder".
	From string `json:"from"`
	// To is the selector of the destinations.
	//
	// Example:
	//
	//	"site:aws".
	To string `json:"to"`
	// Ports lists the destination ports the rule is about. It is left out of
	// the JSON when the rule covers every port.
	//
	// Example:
	//
	//	[5432].
	Ports []uint16 `json:"ports,omitempty"`
}
