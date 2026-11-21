package tests

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/config"
)

// TestRule_V_CFG_001 breaks a config three ways: a node with none, an edit after it was
// hashed, and a value the schema refuses.
func TestRule_V_CFG_001(t *testing.T) {
	quiet(t, "V-CFG-001")
	p := cleanPlan(t, true)
	p.Configs = p.Configs[:2]
	got := fires(t, p, "V-CFG-001")
	if len(got) != 1 || !strings.Contains(got[0].Message, "has no config") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	configOf(p, builder).Forwarding = true
	got = fires(t, p, "V-CFG-001")
	if got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "hash does not match") {
		t.Errorf("%+v", got)
	}
	// A schema fault, re-hashed so that only the schema objects.
	p = cleanPlan(t, true)
	c := configOf(p, builder)
	c.Role = "router"
	c.Overlay.ListenPort = 70000
	if _, err := c.Encode(); err != nil {
		t.Fatal(err)
	}
	got = findings(p, "V-CFG-001")
	joined := ""
	for _, f := range got {
		joined += f.Message + "\n"
	}
	if len(got) != 2 || !strings.Contains(joined, "$.role") || !strings.Contains(joined, "$.overlay.listen_port") {
		t.Errorf("%d findings: %s", len(got), joined)
	}
}

// TestRule_V_CFG_002 gives a node an active route through a node that is not its peer.
func TestRule_V_CFG_002(t *testing.T) {
	quiet(t, "V-CFG-002")
	p := cleanPlan(t, true)
	c := configOf(p, builder)
	c.Routes = append(c.Routes, config.Route{Prefix: pfx("10.99.0.0/16"), Via: "ghost", Metric: 5, Active: true})
	got := fires(t, p, "V-CFG-002")
	if len(got) != 1 || got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "ghost") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	c = configOf(p, builder)
	c.Routes = append(c.Routes, config.Route{Prefix: pfx("10.99.0.0/16"), Via: "ghost", Metric: 5, Backup: true})
	if got := findings(p, "V-CFG-002"); len(got) != 0 {
		t.Errorf("a backup was reported: %+v", got)
	}
}

// P1-VAL-030
//
// TestRule_V_CFG_003 takes a route's prefix out of its peer's allowed prefixes, which is
// what a missed failover looks like to WireGuard.
func TestRule_V_CFG_003(t *testing.T) {
	quiet(t, "V-CFG-003")
	p := cleanPlan(t, true)
	c := configOf(p, builder)
	peer := &c.Peers[peerIndex(c, hub)]
	peer.AllowedPrefixes = peer.AllowedPrefixes[1:] // drops 10.42.0.0/20
	got := fires(t, p, "V-CFG-003")
	if len(got) != 1 || got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "10.42.0.0/20") || !strings.Contains(got[0].Message, "aws-hub") {
		t.Errorf("%+v", got)
	}
	// After a failover the config must have moved the prefix; one that did not is caught.
	p = cleanPlan(t, false)
	c = configOf(p, builder)
	d := &c.Peers[peerIndex(c, dgx)]
	h := &c.Peers[peerIndex(c, hub)]
	h.AllowedPrefixes = append([]netip.Prefix(nil), d.AllowedPrefixes...) // a stale list
	fires(t, p, "V-CFG-003")
}

// P1-VAL-031
//
// TestRule_V_CFG_004 puts a prefix in the lists of two peers.
func TestRule_V_CFG_004(t *testing.T) {
	quiet(t, "V-CFG-004")
	p := cleanPlan(t, true)
	c := configOf(p, builder)
	h := &c.Peers[peerIndex(c, hub)]
	h.AllowedPrefixes = append(h.AllowedPrefixes, pfx("10.42.32.0/20")) // also in dgx-spark's
	got := fires(t, p, "V-CFG-004")
	if len(got) != 1 || got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "10.42.32.0/20 is allowed for both") {
		t.Errorf("%+v", got)
	}
}

// P1-VAL-032
//
// TestRule_V_CFG_005 plants a private key where a public key reference belongs, and a
// secret-named key in a policy entry's text.
func TestRule_V_CFG_005(t *testing.T) {
	quiet(t, "V-CFG-005")
	p := cleanPlan(t, true)
	c := configOf(p, builder)
	c.Peers[0].PublicKeyRef = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	got := fires(t, p, "V-CFG-005")
	if got[0].Subject != "builder-1" || !strings.Contains(got[0].Message, "wireguard-key") {
		t.Errorf("%+v", got)
	}
	for _, f := range got {
		if strings.Contains(f.Message, "yAnz5TF") {
			t.Errorf("the finding repeats the key: %+v", f)
		}
	}
	p = cleanPlan(t, true)
	configOf(p, dgx).Policy = []config.PolicyEntry{{Action: "allow", From: "ghp_abcdefghijklmnopqrstuvwxyz0123456789", To: "x"}}
	fires(t, p, "V-CFG-005")
}

// TestRule_V_CFG_005_TheTopologyLabels plants a private key in a node's label, the one place a
// person can type a secret into the input, and checks the rule names the label and never
// repeats the value, and that an ordinary label passes.
func TestRule_V_CFG_005_TheTopologyLabels(t *testing.T) {
	quiet(t, "V-CFG-005")
	p := cleanPlan(t, true)
	p.Topology.Nodes[1].Labels = map[string]string{"owner": "platform-team"}
	quiet(t, "V-CFG-005")
	p = cleanPlan(t, true)
	p.Topology.Nodes[1].Labels = map[string]string{"wg_private_key": "cGxhbnRlZC1zZWNyZXQtZm9yLXRlc3RzLW9ubHktMTIzNA=="}
	got := fires(t, p, "V-CFG-005")
	if want := "topology:nodes[" + string(p.Topology.Nodes[1].ID) + "].labels.wg_private_key"; got[0].Subject != want {
		t.Errorf("subject %q, want %q", got[0].Subject, want)
	}
	for _, f := range got {
		if strings.Contains(f.Message, "cGxh") || strings.Contains(f.Subject, "cGxh") {
			t.Errorf("the finding repeats the secret: %+v", f)
		}
	}
}

// P1-VAL-033
//
// TestRule_V_CFG_006 takes away dgx-spark's routes back to builder-1's overlay address
// while builder-1 still allows prefixes for it.
func TestRule_V_CFG_006(t *testing.T) {
	quiet(t, "V-CFG-006")
	p := cleanPlan(t, true)
	c := configOf(p, dgx)
	var kept []config.Route
	for _, r := range c.Routes {
		if !r.Prefix.Contains(configOf(p, builder).Overlay.Address.Addr()) {
			kept = append(kept, r)
		}
	}
	c.Routes = kept
	got := fires(t, p, "V-CFG-006")
	if len(got) == 0 || !strings.Contains(got[0].Message, "no active route back to builder-1") || !strings.Contains(got[0].Message, "10.42.240.2") {
		t.Errorf("%+v", got)
	}
	// An inactive route is not a way back.
	p = cleanPlan(t, true)
	c = configOf(p, dgx)
	for i, r := range c.Routes {
		if r.Prefix.Contains(configOf(p, builder).Overlay.Address.Addr()) {
			c.Routes[i].Active = false
		}
	}
	fires(t, p, "V-CFG-006")
	// A peer with nothing allowed needs no way back.
	p = cleanPlan(t, true)
	c = configOf(p, builder)
	c.Peers[peerIndex(c, dgx)].AllowedPrefixes = nil
	for _, f := range findings(p, "V-CFG-006") {
		if strings.Contains(f.Message, "builder-1 allows") && strings.Contains(f.Message, "for dgx-spark") {
			t.Errorf("%+v", f)
		}
	}
}

// TestRule_V_CFG_004_equivalentSpellingsAreOnePrefix checks two peers that carry the same
// block spelled differently (10.42.32.0/20 and 10.42.32.5/20) are caught, since encoding
// writes both as the one block, and that a route is allowed through a peer whose list
// spells its prefix with host bits.
func TestRule_V_CFG_004_equivalentSpellingsAreOnePrefix(t *testing.T) {
	p := cleanPlan(t, true)
	c := configOf(p, builder)
	h := &c.Peers[peerIndex(c, hub)]
	h.AllowedPrefixes = append(h.AllowedPrefixes, pfx("10.42.32.5/20")) // dgx-spark's list has 10.42.32.0/20
	got := fires(t, p, "V-CFG-004")
	if !strings.Contains(got[0].Message, "10.42.32.0/20 is allowed for both") {
		t.Errorf("%+v", got)
	}
	p = cleanPlan(t, true)
	c = configOf(p, builder)
	h = &c.Peers[peerIndex(c, hub)]
	h.AllowedPrefixes[0] = pfx("10.42.0.5/20") // the same block as the route's 10.42.0.0/20
	if got := findings(p, "V-CFG-003"); len(got) != 0 {
		t.Errorf("an equivalent spelling was refused: %+v", got)
	}
}
