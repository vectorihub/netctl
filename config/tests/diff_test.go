package tests

import (
	"net/netip"
	"testing"

	"github.com/vectorihub/netctl/config"
)

// TestDiffOfAConfigWithItselfIsEmpty checks nothing is reported when nothing changed,
// and that the plan ID and hash alone are not a change.
func TestDiffOfAConfigWithItselfIsEmpty(t *testing.T) {
	c := sample()
	if d := config.Diff(c, c.Clone()); !d.Empty() || d.Text() != "" {
		t.Errorf("a config differs from itself: %+v", d)
	}
	other := c.Clone()
	other.PlanID, other.Hash = "other@12345678", "sha256:"+"00"
	if d := config.Diff(c, other); !d.Empty() {
		t.Errorf("the plan ID or hash was reported as a change: %+v", d)
	}
	shuffled := sample()
	shuffled.Peers[0], shuffled.Peers[1] = shuffled.Peers[1], shuffled.Peers[0]
	if d := config.Diff(c, shuffled); !d.Empty() {
		t.Errorf("a different order was reported as a change: %+v", d)
	}
}

// TestDiffFindsMovedPrefixes checks a prefix that left one peer's list and joined
// another's is one move, and one that only appears or disappears is not.
func TestDiffFindsMovedPrefixes(t *testing.T) {
	old := sample()
	updated := sample()
	// 10.42.32.0/20 moves from the direct peer to the hub; 10.42.99.0/24 appears on
	// the hub; 10.42.240.3/32 disappears from the direct peer, which keeps nothing.
	direct, hub := &updated.Peers[0], &updated.Peers[1]
	direct.AllowedPrefixes = nil
	hub.AllowedPrefixes = append(hub.AllowedPrefixes, netip.MustParsePrefix("10.42.32.0/20"), netip.MustParsePrefix("10.42.99.0/24"))
	d := config.Diff(old, updated)
	if len(d.Moves) != 1 || d.Moves[0] != (config.PrefixMove{Prefix: netip.MustParsePrefix("10.42.32.0/20"), From: "dgx-spark", To: "aws-hub"}) {
		t.Fatalf("moves %+v", d.Moves)
	}
	var onHub, onDirect *config.PeerChange
	for i := range d.PeersChanged {
		switch d.PeersChanged[i].Node {
		case "aws-hub":
			onHub = &d.PeersChanged[i]
		case "dgx-spark":
			onDirect = &d.PeersChanged[i]
		}
	}
	if onHub == nil || !equalPrefixes(onHub.Added, prefixes("10.42.99.0/24")) || len(onHub.Removed) != 0 {
		t.Errorf("hub change %+v", onHub)
	}
	if onDirect == nil || !equalPrefixes(onDirect.Removed, prefixes("10.42.240.3/32")) || len(onDirect.Added) != 0 {
		t.Errorf("direct change %+v", onDirect)
	}
}

// TestDiffFindsPeerChanges checks peers added and removed, and the fields of a peer
// that changed.
func TestDiffFindsPeerChanges(t *testing.T) {
	old := sample()
	updated := sample()
	updated.Peers = updated.Peers[:1] // dgx-spark stays, aws-hub goes
	updated.Peers[0].Keepalive = 0
	updated.Peers[0].Endpoints = nil
	updated.Peers[0].Probe = nil
	updated.Peers = append(updated.Peers, config.Peer{Node: "mac-studio", Link: "builder-1~mac-studio", Role: config.PeerDirect, PublicKeyRef: "node:mac-studio"})
	d := config.Diff(old, updated)
	if len(d.PeersRemoved) != 1 || d.PeersRemoved[0].Node != "aws-hub" || len(d.PeersAdded) != 1 || d.PeersAdded[0].Node != "mac-studio" {
		t.Errorf("added %+v removed %+v", d.PeersAdded, d.PeersRemoved)
	}
	if len(d.PeersChanged) != 1 {
		t.Fatalf("changed %+v", d.PeersChanged)
	}
	got := d.PeersChanged[0]
	if got.Node != "dgx-spark" || len(got.Fields) != 3 || got.Fields[0] != "endpoints" || got.Fields[1] != "keepalive_seconds" || got.Fields[2] != "probe" {
		t.Errorf("changed fields %+v, want endpoints, keepalive_seconds and probe", got)
	}
}

// TestDiffFindsRouteChanges checks routes added, removed and changed, matched by
// prefix and next hop.
func TestDiffFindsRouteChanges(t *testing.T) {
	old := sample()
	updated := sample()
	updated.Routes[0].Active, updated.Routes[0].Backup = true, false // the backup via aws-hub becomes active
	updated.Routes = updated.Routes[:1]                              // the direct route goes
	updated.Routes = append(updated.Routes, config.Route{Prefix: netip.MustParsePrefix("10.42.77.0/24"), Via: "aws-hub", Metric: 20, Active: true})
	d := config.Diff(old, updated)
	if len(d.RoutesRemoved) != 1 || d.RoutesRemoved[0].Via != "dgx-spark" {
		t.Errorf("removed %+v", d.RoutesRemoved)
	}
	if len(d.RoutesAdded) != 1 || d.RoutesAdded[0].Prefix != netip.MustParsePrefix("10.42.77.0/24") {
		t.Errorf("added %+v", d.RoutesAdded)
	}
	if len(d.RoutesChanged) != 1 || d.RoutesChanged[0].Old.Active || !d.RoutesChanged[0].New.Active || d.RoutesChanged[0].New.Backup {
		t.Errorf("changed %+v", d.RoutesChanged)
	}
}

// TestDiffNamesTheOtherFieldsThatChanged checks the scalar fields are reported by
// name.
func TestDiffNamesTheOtherFieldsThatChanged(t *testing.T) {
	old := sample()
	updated := sample()
	updated.Role, updated.Forwarding = "hub", true
	updated.Overlay.ListenPort = 51821
	updated.SitePrefix = nil
	updated.Advertise = updated.Advertise[:1]
	updated.Health.FailThreshold = 4
	updated.Policy = nil
	d := config.Diff(old, updated)
	want := []string{"role", "forwarding", "overlay", "site_prefix", "advertise", "health", "policy"}
	if len(d.Other) != len(want) {
		t.Fatalf("other %v, want %v", d.Other, want)
	}
	for i, w := range want {
		if d.Other[i] != w {
			t.Errorf("other[%d] = %s, want %s", i, d.Other[i], w)
		}
	}
}

// TestDiffDoesNotChangeItsInputs checks neither config is touched, down to the order
// of its lists.
func TestDiffDoesNotChangeItsInputs(t *testing.T) {
	a, b := sample(), sample()
	b.Peers[0].Keepalive = 1
	beforeA, beforeB := sample(), sample()
	beforeB.Peers[0].Keepalive = 1
	config.Diff(a, b)
	if a.Peers[0].Node != beforeA.Peers[0].Node || a.Advertise[0] != beforeA.Advertise[0] || b.Peers[0].Node != beforeB.Peers[0].Node || b.Routes[0] != beforeB.Routes[0] {
		t.Error("Diff reordered a config")
	}
}

// TestDiffPlans checks nodes added and removed are listed, and a node that did not
// change is not.
func TestDiffPlans(t *testing.T) {
	a, b, c := sample(), sample(), sample()
	a.Node, b.Node, c.Node = "n-a", "n-b", "n-c"
	changed := b.Clone()
	changed.Forwarding = true
	d := config.DiffPlans([]*config.NodeConfig{a, b}, []*config.NodeConfig{changed, c})
	if len(d.NodesRemoved) != 1 || d.NodesRemoved[0] != "n-a" || len(d.NodesAdded) != 1 || d.NodesAdded[0] != "n-c" {
		t.Errorf("added %v removed %v", d.NodesAdded, d.NodesRemoved)
	}
	if len(d.Nodes) != 1 || d.Nodes[0].Node != "n-b" || d.Empty() || d.Text() == "" {
		t.Errorf("nodes %+v", d.Nodes)
	}
	if !config.DiffPlans([]*config.NodeConfig{a}, []*config.NodeConfig{a.Clone()}).Empty() {
		t.Error("a plan differs from itself")
	}
}
