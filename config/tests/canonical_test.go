package tests

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/types"
)

// sample returns a config that uses every field: a peer with a probe, endpoints and
// traversal, routes with a backup, a policy entry with ports, a site prefix.
func sample() *config.NodeConfig {
	site := netip.MustParsePrefix("10.42.16.0/20")
	return &config.NodeConfig{
		Schema: config.Schema, PlanID: "three-site@7f3c9e1a", Node: "builder-1", Role: "spoke", Forwarding: false,
		Overlay:    config.Overlay{Address: netip.MustParsePrefix("10.42.240.2/32"), ListenPort: 51820},
		SitePrefix: &site,
		Advertise:  prefixes("10.42.240.2/32", "10.42.16.0/20"),
		Peers: []config.Peer{
			{Node: "dgx-spark", Link: direct, Role: config.PeerDirect, PublicKeyRef: "node:dgx-spark",
				Endpoints:      []netip.AddrPort{netip.MustParseAddrPort("198.51.100.7:51820"), netip.MustParseAddrPort("198.51.100.6:51820")},
				NeedsTraversal: true, Keepalive: 25,
				Probe:           &config.Probe{Local: netip.MustParsePrefix("10.42.252.1/30"), Remote: netip.MustParseAddr("10.42.252.2")},
				AllowedPrefixes: prefixes("10.42.240.3/32", "10.42.32.0/20")},
			{Node: "aws-hub", Link: "aws-hub~builder-1", Role: config.PeerHub, PublicKeyRef: "node:aws-hub",
				Endpoints: []netip.AddrPort{netip.MustParseAddrPort("203.0.113.10:51820")}, Keepalive: 25,
				AllowedPrefixes: prefixes("10.42.240.1/32", "10.42.0.0/20")},
		},
		Routes: []config.Route{
			{Prefix: netip.MustParsePrefix("10.42.32.0/20"), Via: "aws-hub", Metric: 40, Backup: true},
			{Prefix: netip.MustParsePrefix("10.42.32.0/20"), Via: "dgx-spark", Metric: 10, Active: true},
		},
		Health: config.Health{
			Probes:        []config.ProbeTarget{{Link: direct, Target: netip.MustParseAddr("10.42.252.2"), Interval: 5}},
			FailThreshold: 3, RecoverThreshold: 5, HoldDownSeconds: 60,
		},
		Policy: []config.PolicyEntry{{Action: "deny", From: "site:builder", To: "site:aws", Ports: []uint16{5432}}},
	}
}

// encoded encodes a copy of the config and returns the bytes and the hash.
func encoded(t testing.TB, c *config.NodeConfig) ([]byte, string) {
	t.Helper()
	copied := c.Clone()
	data, err := copied.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return data, copied.Hash
}

// TestEncodeIsCanonical checks the same config written in different orders gives the
// same bytes: lists are sorted, prefixes masked and repeats dropped.
func TestEncodeIsCanonical(t *testing.T) {
	a, _ := encoded(t, sample())
	b := sample()
	slices.Reverse(b.Advertise)
	slices.Reverse(b.Peers)
	slices.Reverse(b.Routes)
	slices.Reverse(b.Peers[0].AllowedPrefixes)
	slices.Reverse(b.Peers[1].Endpoints)
	slices.Reverse(b.Peers[1].AllowedPrefixes)
	if got, _ := encoded(t, b); !bytes.Equal(a, got) {
		t.Errorf("a different order gave different bytes:\n%s\n%s", a, got)
	}
	c := sample()
	c.Advertise = append(c.Advertise, netip.MustParsePrefix("10.42.16.5/20"), netip.MustParsePrefix("10.42.16.0/20"))
	c.Peers[0].AllowedPrefixes[0] = netip.MustParsePrefix("10.42.240.3/32")
	c.Peers[0].Endpoints = append(c.Peers[0].Endpoints, c.Peers[0].Endpoints[0])
	if got, _ := encoded(t, c); !bytes.Equal(a, got) {
		t.Errorf("unmasked or repeated entries changed the bytes:\n%s", got)
	}
}

// TestNilAndEmptyListsAreTheSame checks an unset list and an empty one are written
// alike, as [] and never null, so equal configs cannot hash differently.
func TestNilAndEmptyListsAreTheSame(t *testing.T) {
	nilled := &config.NodeConfig{Schema: config.Schema, Node: "x", Role: "spoke"}
	empty := &config.NodeConfig{
		Schema: config.Schema, Node: "x", Role: "spoke", Advertise: []netip.Prefix{}, Peers: []config.Peer{},
		Routes: []config.Route{}, Policy: []config.PolicyEntry{}, Health: config.Health{Probes: []config.ProbeTarget{}},
	}
	a, _ := encoded(t, nilled)
	b, _ := encoded(t, empty)
	if !bytes.Equal(a, b) {
		t.Errorf("nil and empty differ:\n%s\n%s", a, b)
	}
	if bytes.Contains(a, []byte("null")) {
		t.Errorf("a null was written:\n%s", a)
	}
	withPeer := &config.NodeConfig{Schema: config.Schema, Node: "x", Role: "spoke", Peers: []config.Peer{{Node: "y"}}}
	if data, _ := encoded(t, withPeer); bytes.Contains(data, []byte("null")) {
		t.Errorf("a peer's empty lists were written as null:\n%s", data)
	}
}

// TestKeysAreInTheDocumentedOrder checks the JSON starts with the keys of the
// reference config in the order the guide shows, and ends with the hash.
func TestKeysAreInTheDocumentedOrder(t *testing.T) {
	data, _ := encoded(t, sample())
	text := string(data)
	last := -1
	for _, key := range []string{`"schema"`, `"plan_id"`, `"node"`, `"role"`, `"forwarding"`, `"overlay"`, `"site_prefix"`, `"advertise"`, `"peers"`, `"routes"`, `"health"`, `"policy"`, `"hash"`} {
		at := strings.Index(text, "\n  "+key)
		if at <= last {
			t.Errorf("%s is out of order", key)
		}
		last = at
	}
	if !strings.HasSuffix(text, "\n}\n") || strings.Contains(text, "\r") {
		t.Error("the output must end with one newline and use no carriage returns")
	}
	if strings.Contains(text, `<`) || strings.Contains(text, `&`) {
		t.Error("HTML escaping was applied")
	}
}

// leaves lists the paths of every settable leaf of the config, with a function that
// changes it. Lists get both an element change and a length change. The Hash is
// left out: it is the result, not an input.
func leaves(c *config.NodeConfig) map[string]func(*config.NodeConfig) {
	out := map[string]func(*config.NodeConfig){}
	var rec func(path string, get func(*config.NodeConfig) reflect.Value, v reflect.Value)
	rec = func(path string, get func(*config.NodeConfig) reflect.Value, v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			switch v.Type() {
			case reflect.TypeOf(netip.Prefix{}), reflect.TypeOf(netip.Addr{}), reflect.TypeOf(netip.AddrPort{}):
				out[path] = func(c *config.NodeConfig) { change(get(c)) }
				return
			}
			for i := 0; i < v.NumField(); i++ {
				i := i
				name := v.Type().Field(i).Name
				if path == "" && name == "Hash" {
					continue
				}
				rec(join(path, name), func(c *config.NodeConfig) reflect.Value { return get(c).Field(i) }, v.Field(i))
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			rec(path+"*", func(c *config.NodeConfig) reflect.Value { return get(c).Elem() }, v.Elem())
			out[path+" removed"] = func(c *config.NodeConfig) { get(c).Set(reflect.Zero(get(c).Type())) }
		case reflect.Slice:
			out[path+" longer"] = func(c *config.NodeConfig) { grow(get(c)) }
			out[path+" shorter"] = func(c *config.NodeConfig) { get(c).Set(get(c).Slice(0, get(c).Len()-1)) }
			for e := 0; e < v.Len(); e++ {
				e := e
				rec(fmt.Sprintf("%s[%d]", path, e), func(c *config.NodeConfig) reflect.Value { return get(c).Index(e) }, v.Index(e))
			}
		default:
			out[path] = func(c *config.NodeConfig) { change(get(c)) }
		}
	}
	rec("", func(c *config.NodeConfig) reflect.Value { return reflect.ValueOf(c).Elem() }, reflect.ValueOf(c).Elem())
	return out
}

// join joins a path and a field name.
func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// change makes a settable value different from what it was.
func change(v reflect.Value) {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint16:
		v.SetUint(v.Uint() + 1)
	case reflect.String:
		v.SetString(v.String() + "x")
	case reflect.Struct:
		switch x := v.Interface().(type) {
		case netip.Prefix:
			// A different length, not a different host: masking would undo that.
			bits := x.Bits() + 1
			if x.Bits() >= x.Addr().BitLen() {
				bits = x.Bits() - 1
			}
			v.Set(reflect.ValueOf(netip.PrefixFrom(x.Addr(), bits)))
		case netip.Addr:
			v.Set(reflect.ValueOf(x.Next()))
		case netip.AddrPort:
			v.Set(reflect.ValueOf(netip.AddrPortFrom(x.Addr(), x.Port()+1)))
		}
	}
}

// grow appends a copy of the first element of a list, changed so it is not a
// repeat, or a new one if the list is empty.
func grow(v reflect.Value) {
	elem := reflect.New(v.Type().Elem()).Elem()
	if v.Len() > 0 {
		elem.Set(v.Index(0))
	}
	grown := reflect.Append(v, elem)
	last := grown.Index(grown.Len() - 1)
	switch last.Kind() {
	case reflect.Struct:
		if last.Type().PkgPath() == "github.com/vectorihub/netctl/config" {
			for i := 0; i < last.NumField(); i++ {
				if last.Field(i).Kind() == reflect.String || last.Field(i).Kind() == reflect.Int {
					change(last.Field(i))
					break
				}
			}
		} else {
			change(last)
		}
	default:
		change(last)
	}
	v.Set(grown)
}

// P1-CFG-011
//
// TestHashChangesWhenAnyFieldDoes mutates every leaf of a config that uses all of
// them, one at a time, and checks the hash differs from the original every time:
// there is no field a change to which goes unnoticed. The leaves are found by
// reflection, so a field added to the types is covered the day it is added.
func TestHashChangesWhenAnyFieldDoes(t *testing.T) {
	_, base := encoded(t, sample())
	canonical := sample()
	canonical.Canonicalise()
	mutations := leaves(canonical)
	if len(mutations) < 70 {
		t.Fatalf("only %d mutations found; the sample is not using every field", len(mutations))
	}
	seen := map[string]string{}
	for path, mutate := range mutations {
		c := sample()
		c.Canonicalise()
		mutate(c)
		// Written, read back and verified, so the change is one a file could carry.
		_, hash := encoded(t, c)
		if hash == base {
			t.Errorf("changing %s did not change the hash", path)
		}
		if other, dup := seen[hash]; dup {
			t.Errorf("changing %s and %s gave the same hash", path, other)
		}
		seen[hash] = path
	}
	for _, field := range []string{"Schema", "PlanID", "Node", "Role", "Forwarding", "Overlay.ListenPort", "Peers[0].Keepalive", "Peers[1].Probe*.Remote", "Peers[1].Endpoints[1]", "Routes[0].Backup", "Health.HoldDownSeconds", "Policy[0].Ports[0]"} {
		if _, ok := mutations[field]; !ok {
			t.Errorf("no mutation for %s: the walk missed it (have %d)", field, len(mutations))
		}
	}
}

// TestEncodeFillsAHashThatVerifies checks the hash is "sha256:" and 64 hex digits,
// Verify accepts the config as written, and refuses it when any field is edited by
// hand afterwards.
func TestEncodeFillsAHashThatVerifies(t *testing.T) {
	c := sample()
	data, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Hash, "sha256:") || len(c.Hash) != len("sha256:")+64 {
		t.Fatalf("hash %q", c.Hash)
	}
	back, err := config.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := back.Verify(); err != nil {
		t.Errorf("the config as written does not verify: %v", err)
	}
	back.Peers[0].Keepalive = 0
	if err := back.Verify(); !types.HasCode(err, types.ErrInvalidValue) {
		t.Errorf("an edited config verified: %v", err)
	}
	back.Hash = ""
	if err := back.Verify(); err == nil {
		t.Error("a config with no hash verified")
	}
}

// TestVerifyAndPlanIDDoNotChangeTheirInput checks Verify and PlanID leave the config
// exactly as it was, down to the order of its lists.
func TestVerifyAndPlanIDDoNotChangeTheirInput(t *testing.T) {
	c := sample() // not canonical: its lists are in the wrong order
	before := c.Clone()
	_ = c.Verify()
	if _, err := config.PlanID("x", []*config.NodeConfig{c}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, before) {
		t.Error("Verify or PlanID changed the config")
	}
}

// TestCloneSharesNothing checks changing or sorting a clone's lists leaves the
// original alone.
func TestCloneSharesNothing(t *testing.T) {
	c := sample()
	before := sample()
	d := c.Clone()
	d.Canonicalise()
	direct := &d.Peers[1] // dgx-spark sorts after aws-hub
	direct.AllowedPrefixes[0] = netip.MustParsePrefix("1.1.1.1/32")
	direct.Probe.Remote = netip.MustParseAddr("1.1.1.1")
	d.Routes[0].Metric = 99
	d.Policy[0].Ports[0] = 1
	site := netip.MustParsePrefix("1.1.1.0/24")
	*d.SitePrefix = site
	d.Health.Probes[0].Interval = 99
	if !reflect.DeepEqual(c, before) {
		t.Error("changing the clone changed the original")
	}
}

// TestPlanIDDependsOnContentNotOnOrderOrHash checks the plan ID is the same whatever
// order the configs are given in and whether or not they have been hashed, and
// changes with content.
func TestPlanIDDependsOnContentNotOnOrderOrHash(t *testing.T) {
	a, b := sample(), sample()
	b.Node, b.Role = "aws-hub", "hub"
	one, err := config.PlanID("t", []*config.NodeConfig{a, b})
	if err != nil {
		t.Fatal(err)
	}
	two, _ := config.PlanID("t", []*config.NodeConfig{b, a})
	if one != two {
		t.Errorf("the order changed the plan ID: %q and %q", one, two)
	}
	if _, err := a.Encode(); err != nil {
		t.Fatal(err)
	}
	a.PlanID = "other@00000000"
	if three, _ := config.PlanID("t", []*config.NodeConfig{a, b}); three != one {
		t.Errorf("the hash or the old plan ID changed the plan ID: %q and %q", three, one)
	}
	b.Forwarding = true
	if four, _ := config.PlanID("t", []*config.NodeConfig{a, b}); four == one {
		t.Error("a change of content did not change the plan ID")
	}
	if named, _ := config.PlanID("u", []*config.NodeConfig{a, b}); !strings.HasPrefix(named, "u@") {
		t.Errorf("the plan ID does not carry the topology's name: %q", named)
	}
}

// TestDecodeIsStrict checks Decode refuses what it should, with the codes.
func TestDecodeIsStrict(t *testing.T) {
	good, _ := encoded(t, sample())
	for name, tc := range map[string]struct {
		text string
		code types.Code
	}{
		"not json":      {`{`, types.ErrInvalidValue},
		"unknown key":   {strings.Replace(string(good), `"role"`, `"interface": "wg0", "role"`, 1), types.ErrInvalidValue},
		"two documents": {string(good) + string(good), types.ErrInvalidValue},
		"stray bracket": {string(good) + `}`, types.ErrInvalidValue},
		"wrong type":    {strings.Replace(string(good), `"forwarding": false`, `"forwarding": "no"`, 1), types.ErrInvalidValue},
		"bad prefix":    {strings.Replace(string(good), `"10.42.16.0/20"`, `"banana"`, 1), types.ErrInvalidValue},
		"old schema":    {strings.Replace(string(good), "v1alpha1", "v0", 1), types.ErrUnsupportedSchema},
		"no schema":     {`{"node": "x"}`, types.ErrUnsupportedSchema},
	} {
		if _, err := config.Decode([]byte(tc.text)); !types.HasCode(err, tc.code) {
			t.Errorf("%s: %v, want %s", name, err, tc.code)
		}
	}
	back, err := config.Decode(good)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := encoded(t, back); !bytes.Equal(again, good) {
		t.Errorf("decoding and encoding changed the bytes:\n%s\n%s", good, again)
	}
}

// TestShuffledListsAlwaysHashTheSame checks, over many random shuffles of every list
// of a config, the hash never moves.
func TestShuffledListsAlwaysHashTheSame(t *testing.T) {
	_, want := encoded(t, sample())
	rng := rand.New(rand.NewPCG(57, 1))
	for i := 0; i < 200; i++ {
		c := sample()
		rng.Shuffle(len(c.Advertise), func(a, b int) { c.Advertise[a], c.Advertise[b] = c.Advertise[b], c.Advertise[a] })
		rng.Shuffle(len(c.Peers), func(a, b int) { c.Peers[a], c.Peers[b] = c.Peers[b], c.Peers[a] })
		rng.Shuffle(len(c.Routes), func(a, b int) { c.Routes[a], c.Routes[b] = c.Routes[b], c.Routes[a] })
		for p := range c.Peers {
			l := c.Peers[p].AllowedPrefixes
			rng.Shuffle(len(l), func(a, b int) { l[a], l[b] = l[b], l[a] })
			e := c.Peers[p].Endpoints
			rng.Shuffle(len(e), func(a, b int) { e[a], e[b] = e[b], e[a] })
		}
		if _, got := encoded(t, c); got != want {
			t.Fatalf("shuffle %d changed the hash", i)
		}
	}
}
