package config

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/netip"
	"slices"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// hashPrefix starts every hash a config carries.
const hashPrefix = "sha256:"

// maskAll returns the prefixes with each one masked to the block it stands for,
// sorted, and without repeats. It never returns nil, so an empty list is written
// as [] and not as null.
func maskAll(prefixes []netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		masked, _ := cidr.Canonical(prefix)
		out = append(out, masked)
	}
	slices.SortFunc(out, cidr.Compare)
	return slices.Compact(out)
}

// Clone returns a copy of the config that shares nothing with it: changing, or
// sorting, a list of the copy leaves the original alone.
//
// Example:
//
//	copy := cfg.Clone(); copy.Peers[0].Keepalive = 0 does not change cfg.
func (c *NodeConfig) Clone() *NodeConfig {
	out := *c
	if c.SitePrefix != nil {
		site := *c.SitePrefix
		out.SitePrefix = &site
	}
	out.Advertise = slices.Clone(c.Advertise)
	if c.Peers != nil {
		out.Peers = make([]Peer, len(c.Peers))
		for i, p := range c.Peers {
			p.Endpoints = slices.Clone(p.Endpoints)
			p.AllowedPrefixes = slices.Clone(p.AllowedPrefixes)
			if p.Probe != nil {
				probe := *p.Probe
				p.Probe = &probe
			}
			out.Peers[i] = p
		}
	}
	out.Routes = slices.Clone(c.Routes)
	out.Health.Probes = slices.Clone(c.Health.Probes)
	if c.Policy != nil {
		out.Policy = make([]PolicyEntry, len(c.Policy))
		for i, entry := range c.Policy {
			entry.Ports = slices.Clone(entry.Ports)
			out.Policy[i] = entry
		}
	}
	return &out
}

// Canonicalise puts the config in its one canonical form, rule G8, so that two
// configs that mean the same thing are the same bytes.
//
// Every list is sorted: peers by node, routes by prefix, then metric, then next
// hop, endpoints and probes in order. Prefixes are masked to their blocks
// (10.42.16.5/20 becomes 10.42.16.0/20), and repeats are dropped from the lists
// of prefixes and endpoints. A list that is nil becomes an empty one, because the
// JSON of both must be the same: [] and never null. The probe's local address is
// the node's own address on the link, host bits and all, and is left as it is.
// Policy entries keep their order, which matters. It changes the config in place,
// and does not touch Hash.
//
// Example:
//
//	cfg.Advertise = []netip.Prefix{overlay, site}; cfg.Canonicalise() leaves the
//	site block first, because 10.42.16.0/20 sorts before 10.42.240.2/32.
func (c *NodeConfig) Canonicalise() {
	c.Advertise = maskAll(c.Advertise)
	if c.SitePrefix != nil {
		masked, _ := cidr.Canonical(*c.SitePrefix)
		c.SitePrefix = &masked
	}
	if c.Peers == nil {
		c.Peers = []Peer{}
	}
	for i := range c.Peers {
		p := &c.Peers[i]
		p.AllowedPrefixes = maskAll(p.AllowedPrefixes)
		if p.Endpoints == nil {
			p.Endpoints = []netip.AddrPort{}
		}
		slices.SortFunc(p.Endpoints, func(a, b netip.AddrPort) int { return a.Compare(b) })
		p.Endpoints = slices.Compact(p.Endpoints)
	}
	slices.SortFunc(c.Peers, func(a, b Peer) int { return cmp.Compare(a.Node, b.Node) })
	if c.Routes == nil {
		c.Routes = []Route{}
	}
	for i := range c.Routes {
		c.Routes[i].Prefix, _ = cidr.Canonical(c.Routes[i].Prefix)
	}
	slices.SortFunc(c.Routes, func(a, b Route) int {
		return cmp.Or(cidr.Compare(a.Prefix, b.Prefix), cmp.Compare(a.Metric, b.Metric), cmp.Compare(a.Via, b.Via),
			compareBool(b.Active, a.Active), compareBool(a.Backup, b.Backup))
	})
	if c.Health.Probes == nil {
		c.Health.Probes = []ProbeTarget{}
	}
	slices.SortFunc(c.Health.Probes, func(a, b ProbeTarget) int {
		return cmp.Or(cmp.Compare(a.Link, b.Link), a.Target.Compare(b.Target), cmp.Compare(a.Interval, b.Interval))
	})
	if c.Policy == nil {
		c.Policy = []PolicyEntry{}
	}
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

// marshal writes the value as two-space-indented JSON ending in a newline, without
// escaping <, > and & (nothing in a config needs it, and it would only make the
// text harder to read). The keys come out in the order of the struct fields.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, types.Errf(types.ErrInvalidValue, "node config", "writing it: %v", err)
	}
	return buf.Bytes(), nil
}

// hashOf returns the hash of the canonical JSON of the config as it stands, with
// the fields named by clear emptied first. The config itself is not changed.
func (c *NodeConfig) hashOf(clearPlan bool) (string, error) {
	copied := c.Clone()
	copied.Hash = ""
	if clearPlan {
		copied.PlanID = ""
	}
	body, err := marshal(copied)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hashPrefix + hex.EncodeToString(sum[:]), nil
}

// Encode canonicalises the config, fills in Hash, and returns the bytes to save:
// rules G8 and G9. The hash is the SHA-256 of the canonical JSON with the hash
// field empty, so any change to any other field changes it. The same config always
// gives the same bytes, whatever order its lists were built in.
//
// Encode returns INVALID_VALUE if the config cannot be written, which does not
// happen for one built by Generate.
//
// Example:
//
//	data, err := cfg.Encode() // cfg.Hash is now "sha256:" and 64 hex digits
func (c *NodeConfig) Encode() ([]byte, error) {
	c.Canonicalise()
	hash, err := c.hashOf(false)
	if err != nil {
		return nil, err
	}
	c.Hash = hash
	return marshal(c)
}

// Verify reports whether Hash is the hash of the rest of the config as written,
// so an agent can tell a config that was damaged or edited by hand. It does not
// change the config. It returns INVALID_VALUE, naming both hashes, if they differ.
//
// Example:
//
//	if err := cfg.Verify(); err != nil { /* refuse to apply it */ }
func (c *NodeConfig) Verify() error {
	copied := c.Clone()
	copied.Canonicalise()
	want, err := copied.hashOf(false)
	if err != nil {
		return err
	}
	if c.Hash != want {
		return types.Errf(types.ErrInvalidValue, "node config "+string(c.Node), "its hash is %q but its content hashes to %q", c.Hash, want)
	}
	return nil
}

// PlanID returns the plan ID of a set of configs, rule G10: the topology's name, an
// at sign, and the first eight hex digits of the SHA-256 of every node's content
// hash, in node order.
//
// A node's content hash here leaves out the plan ID and the hash, so the plan ID
// can be worked out again from finished configs, and does not depend on itself.
// It changes whenever the content of any node changes, and not otherwise. The
// configs are not changed.
//
// Example:
//
//	config.PlanID("three-site", configs) returns "three-site@" and eight hex digits.
func PlanID(topology string, configs []*NodeConfig) (string, error) {
	sorted := slices.Clone(configs)
	slices.SortFunc(sorted, func(a, b *NodeConfig) int { return cmp.Compare(a.Node, b.Node) })
	h := sha256.New()
	for _, c := range sorted {
		copied := c.Clone()
		copied.Canonicalise()
		content, err := copied.hashOf(true)
		if err != nil {
			return "", err
		}
		h.Write([]byte(string(c.Node) + "=" + content + "\n"))
	}
	return topology + "@" + hex.EncodeToString(h.Sum(nil))[:8], nil
}

// Decode reads a config written by Encode. It is strict: a key it does not know, a
// value of the wrong type and anything after the end of the config are refused
// (INVALID_VALUE), and so is a schema other than Schema (UNSUPPORTED_SCHEMA, with
// nothing else checked). It does not check the hash; call Verify for that.
//
// Example:
//
//	cfg, err := config.Decode(data)
//	if err != nil { return err }
//	if err := cfg.Verify(); err != nil { return err }
func Decode(data []byte) (*NodeConfig, error) {
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err == nil && probe.Schema != "" && probe.Schema != Schema {
		return nil, types.Errf(types.ErrUnsupportedSchema, "node config", "schema %q is not %q", probe.Schema, Schema)
	}
	var c NodeConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return nil, types.Errf(types.ErrInvalidValue, "node config", "reading it: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, types.Errf(types.ErrInvalidValue, "node config", "there is more than one value in it, or something after the end of it")
	}
	if c.Schema != Schema {
		return nil, types.Errf(types.ErrUnsupportedSchema, "node config", "schema %q is not %q", c.Schema, Schema)
	}
	return &c, nil
}
