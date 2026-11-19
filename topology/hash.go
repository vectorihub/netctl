package topology

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"slices"
)

// hashVersion changes only if the canonical form below changes, which would
// change every hash. It is part of the hashed bytes.
const hashVersion = 1

// Hash returns a fingerprint of the topology: the SHA-256, as 64 lower-case hex
// characters, of its canonical JSON. It ties a plan to the input that produced it.
//
// Two topologies that mean the same thing hash the same, however they were
// written: the order of sites, nodes and links, which end of a link is named
// first, YAML comments and whitespace, links given as peers or as links, an
// omitted default or the same value written out, and the order of a node's
// endpoints, advertisements and peers, and of an intent's ports. What is not
// reordered is what has an order: intents, and address pools. Anything that
// changes the network changes the hash: a cost, a label, an intent's position.
//
// Hash works on a copy, so it does not change t, and it needs no Normalize
// first. Peers are imported on the copy when they can be. A nil Topology has a
// hash too. The result is stable across releases unless the canonical form
// changes, which would be called out in the changelog.
//
// Example: three-site.yaml and the same file with its nodes in another order
// have the same Hash.
func (t *Topology) Hash() string {
	sum := sha256.Sum256(t.canonicalJSON())
	return hex.EncodeToString(sum[:])
}

// canonical* are the hashed shapes. They are written out field by field, not
// taken from Topology, so adding a field to Topology cannot silently change a
// hash, and every enum is its text.
type (
	canonicalTopology struct {
		Version    int               `json:"hash_version"`
		Schema     string            `json:"schema"`
		Name       string            `json:"name"`
		Addressing canonicalAddress  `json:"addressing"`
		Sites      []canonicalSite   `json:"sites"`
		Nodes      []canonicalNode   `json:"nodes"`
		Links      []canonicalLink   `json:"links"`
		Intents    []canonicalIntent `json:"intents"`
	}
	canonicalAddress struct {
		Ref      string          `json:"ref"`
		Supernet string          `json:"supernet"`
		Pools    []canonicalPool `json:"pools"`
	}
	canonicalPool struct {
		Name      string `json:"name"`
		Prefix    string `json:"prefix"`
		ChildBits int    `json:"child_bits"`
		Reserved  bool   `json:"reserved"`
		SkipFirst int    `json:"skip_first"`
	}
	canonicalSite struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Region   string `json:"region"`
	}
	canonicalNode struct {
		ID         string            `json:"id"`
		Site       string            `json:"site"`
		Role       string            `json:"role"`
		Endpoints  []string          `json:"endpoints"`
		NAT        string            `json:"nat"`
		Advertises []string          `json:"advertises"`
		Labels     map[string]string `json:"labels"` // encoding/json writes map keys sorted
		Peers      []string          `json:"peers"`
	}
	canonicalLink struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		A      string `json:"a"`
		B      string `json:"b"`
		Cost   int    `json:"cost"`
		Health string `json:"health"`
	}
	canonicalIntent struct {
		Action string   `json:"action"`
		From   string   `json:"from"`
		To     string   `json:"to"`
		Ports  []uint16 `json:"ports"`
	}
)

// canonicalJSON returns the bytes that Hash digests: the topology in its one
// canonical form, as JSON. It works on a copy (imports peers, normalises), turns
// every enum into its text, sorts what is a set (endpoints, advertisements,
// peers, ports) and leaves what has an order (intents, pools) alone. Empty lists
// are written as [] and never null, so "none" has one spelling.
func (t *Topology) canonicalJSON() []byte {
	var work Topology
	if t != nil {
		work = t.clone()
		// A failed import leaves the peers in place, and they are hashed as they are.
		_ = work.ImportPeers()
		work.Normalize()
	}

	out := canonicalTopology{
		Version: hashVersion, Schema: work.Schema, Name: work.Name,
		Addressing: canonicalAddress{Ref: work.Addressing.Ref, Supernet: prefixText(work.Addressing.Supernet), Pools: []canonicalPool{}},
		Sites:      []canonicalSite{}, Nodes: []canonicalNode{}, Links: []canonicalLink{}, Intents: []canonicalIntent{},
	}
	for _, pool := range work.Addressing.Pools {
		out.Addressing.Pools = append(out.Addressing.Pools, canonicalPool{pool.Name, prefixText(pool.Prefix), pool.ChildBits, pool.Reserved, pool.SkipFirst})
	}
	for _, site := range work.Sites {
		out.Sites = append(out.Sites, canonicalSite{string(site.ID), site.Provider, site.Region})
	}
	for _, node := range work.Nodes {
		canon := canonicalNode{
			ID: string(node.ID), Site: string(node.Site), Role: node.Role.String(), NAT: node.NAT.String(),
			Endpoints: []string{}, Advertises: []string{}, Peers: []string{}, Labels: map[string]string{},
		}
		for _, endpoint := range node.Endpoints {
			canon.Endpoints = append(canon.Endpoints, endpoint.String())
		}
		for _, advert := range node.Advertises {
			canon.Advertises = append(canon.Advertises, advert.String())
		}
		for _, peer := range node.Peers {
			canon.Peers = append(canon.Peers, string(peer))
		}
		for key, value := range node.Labels {
			canon.Labels[key] = value
		}
		slices.Sort(canon.Endpoints)
		slices.Sort(canon.Advertises)
		slices.Sort(canon.Peers)
		out.Nodes = append(out.Nodes, canon)
	}
	for _, link := range work.Links {
		out.Links = append(out.Links, canonicalLink{string(link.ID), link.Kind.String(), string(link.A), string(link.B), link.Cost, link.Health.String()})
	}
	for _, intent := range work.Intents {
		ports := slices.Clone(intent.Ports)
		slices.Sort(ports)
		ports = slices.Compact(ports)
		if ports == nil {
			ports = []uint16{}
		}
		out.Intents = append(out.Intents, canonicalIntent{intent.Action.String(), intent.From.String(), intent.To.String(), ports})
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(out); err != nil {
		panic("topology: canonical form cannot be encoded: " + err.Error()) // plain strings and numbers cannot fail
	}
	return buffer.Bytes()
}

// prefixText is the prefix's text, or "" for the zero Prefix.
func prefixText(prefix netip.Prefix) string {
	if !prefix.IsValid() {
		return ""
	}
	return prefix.String()
}

// clone copies everything Normalize, ImportPeers or the caller could change in
// place: the slices of sites, nodes and links, and each node's own slices.
func (t *Topology) clone() Topology {
	out := *t
	out.Sites = slices.Clone(t.Sites)
	out.Links = slices.Clone(t.Links)
	out.Intents = slices.Clone(t.Intents)
	out.Addressing.Pools = slices.Clone(t.Addressing.Pools)
	out.Nodes = make([]Node, len(t.Nodes))
	for i, node := range t.Nodes {
		node.Endpoints = slices.Clone(node.Endpoints)
		node.Advertises = slices.Clone(node.Advertises)
		node.Peers = slices.Clone(node.Peers)
		out.Nodes[i] = node
	}
	return out
}
