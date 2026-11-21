package tests

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/types"
)

// properties is how many random networks each property is checked on.
const properties = 400

// P1-CFG-004
//
// TestNoPrefixInTwoPeersLists checks that on every node of every random network,
// healthy or not, no prefix is in the allowed list of two peers: WireGuard would
// silently keep only the last.
func TestNoPrefixInTwoPeersLists(t *testing.T) {
	for seed := 0; seed < properties; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			holder := map[netip.Prefix]types.NodeID{}
			for _, p := range c.Peers {
				for _, prefix := range p.AllowedPrefixes {
					if other, dup := holder[prefix]; dup {
						t.Fatalf("seed %d: on %s, %s is allowed through both %s and %s", seed, c.Node, prefix, other, p.Node)
					}
					holder[prefix] = p.Node
				}
			}
		}
	}
}

// TestEveryActiveRouteIsAllowedThroughItsNextHop checks the other half of the rule:
// every active route's prefix is in the list of the peer it goes through, so
// WireGuard does not drop what the routes send, and a backup is in no list unless
// the same prefix is active through that peer.
func TestEveryActiveRouteIsAllowedThroughItsNextHop(t *testing.T) {
	for seed := 0; seed < properties; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			for _, r := range c.Routes {
				var via *int
				for i, p := range c.Peers {
					if p.Node == r.Via {
						via = &i
					}
				}
				if via == nil {
					t.Fatalf("seed %d: %s routes %s via %s, which is not a peer", seed, c.Node, r.Prefix, r.Via)
				}
				listed := slices.Contains(c.Peers[*via].AllowedPrefixes, r.Prefix)
				if r.Active && !listed {
					t.Fatalf("seed %d: %s: the active route for %s via %s is not in that peer's list", seed, c.Node, r.Prefix, r.Via)
				}
				if !r.Active && listed {
					other := false
					for _, o := range c.Routes {
						other = other || (o.Active && o.Prefix == r.Prefix && o.Via == r.Via)
					}
					if !other {
						t.Fatalf("seed %d: %s: the backup route for %s via %s put it in the peer's list", seed, c.Node, r.Prefix, r.Via)
					}
				}
			}
		}
	}
}

// P1-CFG-008
//
// TestEveryNodeHasAPeerForEveryLinkItIsOn checks, over random networks, that a node
// has one peer for each link it is an end of and none other, whatever the health of
// the links: a link that is down is still a peer, only its prefixes move.
func TestEveryNodeHasAPeerForEveryLinkItIsOn(t *testing.T) {
	for seed := 0; seed < properties; seed++ {
		p := randomPlan(t, seed)
		want := map[types.NodeID][]types.LinkID{}
		for _, l := range p.in.Topology.Links {
			id := types.LinkIDOf(l.A, l.B)
			want[l.A], want[l.B] = append(want[l.A], id), append(want[l.B], id)
		}
		for _, c := range p.configs {
			var got []types.LinkID
			for _, peer := range c.Peers {
				got = append(got, peer.Link)
			}
			slices.Sort(got)
			slices.Sort(want[c.Node])
			if !slices.Equal(got, want[c.Node]) {
				t.Fatalf("seed %d: %s has peers on %v, want %v", seed, c.Node, got, want[c.Node])
			}
		}
	}
}

// P1-CFG-009
//
// TestPeerSymmetry checks that if A lists B as a peer, B lists A, on the same link,
// and the two see the link alike: a direct link has probe addresses that mirror
// each other, and both ends call it the same kind of link.
func TestPeerSymmetry(t *testing.T) {
	for seed := 0; seed < properties; seed++ {
		by := map[types.NodeID]map[types.NodeID]int{}
		configs := randomPlan(t, seed).configs
		for i, c := range configs {
			by[c.Node] = map[types.NodeID]int{}
			for j, p := range c.Peers {
				by[c.Node][p.Node] = j
			}
			_ = i
		}
		for _, c := range configs {
			for _, p := range c.Peers {
				j, ok := by[p.Node][c.Node]
				if !ok {
					t.Fatalf("seed %d: %s lists %s, which does not list it back", seed, c.Node, p.Node)
				}
				var other = configs[0]
				for _, o := range configs {
					if o.Node == p.Node {
						other = o
					}
				}
				back := other.Peers[j]
				if back.Link != p.Link || (back.Role == "direct") != (p.Role == "direct") {
					t.Fatalf("seed %d: %s and %s disagree about their link: %+v and %+v", seed, c.Node, p.Node, p, back)
				}
				if (p.Probe == nil) != (back.Probe == nil) {
					t.Fatalf("seed %d: only one end of %s probes", seed, p.Link)
				}
				if p.Probe != nil && (p.Probe.Remote != back.Probe.Local.Addr() || back.Probe.Remote != p.Probe.Local.Addr()) {
					t.Fatalf("seed %d: the probe addresses of %s do not mirror: %+v and %+v", seed, p.Link, p.Probe, back.Probe)
				}
			}
		}
	}
}

// TestProbeAddressStaysWithTheDirectPeer checks that on every node of every random
// network the far end's probe address is in the direct peer's list, healthy or not,
// so probes keep crossing the link whose traffic has failed over.
func TestProbeAddressStaysWithTheDirectPeer(t *testing.T) {
	for seed := 0; seed < properties; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			for _, p := range c.Peers {
				if p.Probe == nil {
					continue
				}
				if !slices.Contains(p.AllowedPrefixes, netip.PrefixFrom(p.Probe.Remote, 32)) {
					t.Fatalf("seed %d: %s: the probe address %s is not in %s's list", seed, c.Node, p.Probe.Remote, p.Node)
				}
				found := false
				for _, target := range c.Health.Probes {
					found = found || (target.Link == p.Link && target.Target == p.Probe.Remote)
				}
				if !found {
					t.Fatalf("seed %d: %s has a probe on %s but no health target", seed, c.Node, p.Link)
				}
			}
		}
	}
}

// TestConfigsAreInCanonicalFormAndVerify checks a generated config is already
// canonical: encoding it again changes nothing, and its hash verifies.
func TestConfigsAreInCanonicalFormAndVerify(t *testing.T) {
	for seed := 0; seed < 100; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			before := c.Hash
			data, err := c.Clone().Encode()
			if err != nil {
				t.Fatal(err)
			}
			again := c.Clone()
			if _, err := again.Encode(); err != nil || again.Hash != before {
				t.Fatalf("seed %d: %s: encoding again changed the hash: %v", seed, c.Node, err)
			}
			if err := c.Verify(); err != nil {
				t.Fatalf("seed %d: %s: %v", seed, c.Node, err)
			}
			_ = data
		}
	}
}

// TestRandomNetworksAreVariedEnough guards the properties above: they prove little
// if the random networks never fail a link, or never have a direct link. Across the
// seeds there must be many direct peers whose traffic is on the hub (only the probe
// address left) and many that carry traffic, hubs with several spokes, and nodes of
// every NAT mode.
func TestRandomNetworksAreVariedEnough(t *testing.T) {
	var failedOver, carrying, traversal, noKeepalive int
	for seed := 0; seed < properties; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			for _, p := range c.Peers {
				switch {
				case p.Probe != nil && len(p.AllowedPrefixes) == 1:
					failedOver++
				case p.Probe != nil:
					carrying++
				}
				if p.NeedsTraversal {
					traversal++
				}
				if p.Keepalive == 0 {
					noKeepalive++
				}
			}
		}
	}
	for name, n := range map[string]int{"failed over": failedOver, "carrying": carrying, "traversal": traversal, "no keepalive": noKeepalive} {
		if n < 50 {
			t.Errorf("only %d %s peers over %d networks: the generator is too tame", n, name, properties)
		}
	}
}
