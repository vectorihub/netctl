package tests

import (
	"math/rand/v2"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// hashOf returns the Hash of a decoded fixture.
func hashOf(t *testing.T, name string) string {
	t.Helper()
	return decode(t, name).Hash()
}

// P1-TOPO-002, hash half: the same network written differently hashes the same.
func TestHashIgnoresOrderCommentsAndWhitespace(t *testing.T) {
	want := hashOf(t, "three-site.yaml")
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(want) {
		t.Fatalf("hash %q", want)
	}
	text := string(fixture(t, "three-site.yaml"))
	rewritten := strings.NewReplacer(
		"    # how other nodes can reach it (public)", "",
		"  - id: aws\n    provider: aws\n    region: eu-west-1\n", "  -   id: aws\n      region:    eu-west-1\n      provider: aws\n",
	).Replace(text)
	rewritten = "# a leading comment\n\n" + rewritten + "\n\n\n"
	got, err := topology.Decode("w.yaml", []byte(rewritten), 0)
	if err != nil || got.Hash() != want {
		t.Errorf("rewritten file: %v", err)
	}

	// Shuffle sites, nodes, links and swap link ends, 200 times.
	rng := rand.New(rand.NewPCG(3, 4))
	for range 200 {
		shuffled := decode(t, "three-site.yaml")
		rng.Shuffle(len(shuffled.Sites), func(i, j int) { shuffled.Sites[i], shuffled.Sites[j] = shuffled.Sites[j], shuffled.Sites[i] })
		rng.Shuffle(len(shuffled.Nodes), func(i, j int) { shuffled.Nodes[i], shuffled.Nodes[j] = shuffled.Nodes[j], shuffled.Nodes[i] })
		rng.Shuffle(len(shuffled.Links), func(i, j int) { shuffled.Links[i], shuffled.Links[j] = shuffled.Links[j], shuffled.Links[i] })
		for i := range shuffled.Links {
			if rng.IntN(2) == 0 {
				shuffled.Links[i].A, shuffled.Links[i].B = shuffled.Links[i].B, shuffled.Links[i].A
			}
		}
		if shuffled.Hash() != want {
			t.Fatal("a reordering changed the hash")
		}
	}
}

// Defaults omitted or written out, and links given as peers, are the same network.
func TestHashSameMeaningSameHash(t *testing.T) {
	want := hashOf(t, "three-site.yaml")
	if got := hashOf(t, "adjacency.yaml"); got != want {
		t.Error("peers form hashes differently from links form")
	}
	// Not normalised at all: costs and health unset, ends swapped, no IDs.
	raw := decode(t, "three-site.yaml")
	for i := range raw.Links {
		raw.Links[i].ID = ""
		raw.Links[i].A, raw.Links[i].B = raw.Links[i].B, raw.Links[i].A
		if raw.Links[i].Kind == types.LinkHubSpoke {
			raw.Links[i].Cost, raw.Links[i].Health = 0, topology.HealthUnset
		}
	}
	if raw.Hash() != want {
		t.Error("an un-normalised copy hashes differently")
	}
	// The order of things that are sets.
	a := decode(t, "three-site.yaml")
	b := decode(t, "three-site.yaml")
	a.Nodes[0].Endpoints = []netip.AddrPort{netip.MustParseAddrPort("203.0.113.10:51820"), netip.MustParseAddrPort("203.0.113.11:51820")}
	b.Nodes[0].Endpoints = []netip.AddrPort{netip.MustParseAddrPort("203.0.113.11:51820"), netip.MustParseAddrPort("203.0.113.10:51820")}
	a.Intents[3].Ports, b.Intents[3].Ports = []uint16{80, 5432}, []uint16{5432, 80, 80}
	if a.Hash() != b.Hash() {
		t.Error("endpoint or port order changed the hash")
	}
}

// Anything that changes the network changes the hash.
func TestHashChangesWithMeaning(t *testing.T) {
	want := hashOf(t, "three-site.yaml")
	for name, change := range map[string]func(*topology.Topology){
		"name":         func(x *topology.Topology) { x.Name = "other" },
		"schema":       func(x *topology.Topology) { x.Schema = "beaver.topology/v2" },
		"cost":         func(x *topology.Topology) { x.Links[0].Cost++ },
		"health":       func(x *topology.Topology) { x.Links[2].Health = topology.HealthAssumed },
		"kind":         func(x *topology.Topology) { x.Links[0].Kind = types.LinkDirect },
		"nat":          func(x *topology.Topology) { x.Nodes[1].NAT = topology.NATNone },
		"role":         func(x *topology.Topology) { x.Nodes[1].Role = types.RoleHub },
		"site":         func(x *topology.Topology) { x.Nodes[1].Site = "dgx" },
		"label":        func(x *topology.Topology) { x.Nodes[1].Labels = map[string]string{"env": "prod"} },
		"advert":       func(x *topology.Topology) { x.Nodes[1].Advertises = nil },
		"endpoint":     func(x *topology.Topology) { x.Nodes[0].Endpoints[0] = netip.MustParseAddrPort("203.0.113.10:51821") },
		"region":       func(x *topology.Topology) { x.Sites[0].Region = "eu-west-2" },
		"ref":          func(x *topology.Topology) { x.Addressing.Ref = "other.yaml" },
		"intent order": func(x *topology.Topology) { x.Intents[0], x.Intents[1] = x.Intents[1], x.Intents[0] },
		"intent port":  func(x *topology.Topology) { x.Intents[3].Ports = []uint16{5433} },
		"intent drop":  func(x *topology.Topology) { x.Intents = x.Intents[:3] },
		"action":       func(x *topology.Topology) { x.Intents[0].Action = topology.ActionDeny },
		"selector": func(x *topology.Topology) {
			x.Intents[2].To = topology.Selector{Kind: topology.SelectorSite, ID: "aws"}
		},
		"node": func(x *topology.Topology) { x.Nodes = x.Nodes[:2] },
	} {
		changed := decode(t, "three-site.yaml")
		change(changed)
		if changed.Hash() == want {
			t.Errorf("%s did not change the hash", name)
		}
	}
	// Moving a boundary between fields must not collide: "ab"+"c" vs "a"+"bc".
	x := &topology.Topology{Name: "ab", Addressing: topology.Addressing{Ref: "c"}}
	y := &topology.Topology{Name: "a", Addressing: topology.Addressing{Ref: "bc"}}
	if x.Hash() == y.Hash() {
		t.Error("field boundaries collide")
	}
}

// TestHashDoesNotChangeItsReceiver checks that Hash neither normalises nor imports peers into the
// topology it is called on.
func TestHashDoesNotChangeItsReceiver(t *testing.T) {
	raw := decode(t, "three-site.yaml")
	raw.Links[0].Cost = 0
	raw.Nodes[0], raw.Nodes[2] = raw.Nodes[2], raw.Nodes[0]
	raw.Nodes[0].Endpoints = []netip.AddrPort{netip.MustParseAddrPort("203.0.113.12:1"), netip.MustParseAddrPort("203.0.113.10:1")}
	before := raw.Nodes[0].ID
	endpoints := append([]netip.AddrPort(nil), raw.Nodes[0].Endpoints...)
	peerNode := decode(t, "adjacency.yaml")
	peerNode.Nodes[0].Peers = []types.NodeID{"builder-1", "dgx-spark"}
	peerNode.Links = nil
	first := peerNode.Hash()
	if len(peerNode.Nodes[0].Peers) != 2 || peerNode.Links != nil || peerNode.Hash() != first {
		t.Error("Hash imported peers into its receiver")
	}
	raw.Hash()
	if raw.Links[0].Cost != 0 || raw.Nodes[0].ID != before || raw.Nodes[0].Endpoints[0] != endpoints[0] {
		t.Error("Hash changed its receiver")
	}
}

// TestHashIgnoresAdvertisementOrderAndImportsPeers checks that advertisement order does not matter
// and that unimported peers hash like their links.
func TestHashIgnoresAdvertisementOrderAndImportsPeers(t *testing.T) {
	a := decode(t, "three-site.yaml")
	b := decode(t, "three-site.yaml")
	extra := topology.Advertisement{Prefix: netip.MustParsePrefix("192.168.7.0/24")}
	a.Nodes[1].Advertises = []topology.Advertisement{{Site: true}, extra}
	b.Nodes[1].Advertises = []topology.Advertisement{extra, {Site: true}}
	if a.Hash() != b.Hash() {
		t.Error("advertisement order changed the hash")
	}
	// A Topology still holding peers hashes as the links they stand for.
	unimported := decode(t, "three-site.yaml")
	unimported.Links = nil
	unimported.Nodes[0].Peers = []types.NodeID{"dgx-spark", "builder-1"}
	unimported.Nodes[1].Peers = []types.NodeID{"dgx-spark", "aws-hub"}
	unimported.Nodes[2].Peers = []types.NodeID{"aws-hub", "builder-1"}
	if unimported.Hash() != hashOf(t, "three-site.yaml") {
		t.Error("unimported peers hash differently from their links")
	}
}

// TestHashOfNilAndEmpty checks that a nil and an empty topology have the same hash, different from
// a real one.
func TestHashOfNilAndEmpty(t *testing.T) {
	var none *topology.Topology
	if none.Hash() != (&topology.Topology{}).Hash() || none.Hash() == hashOf(t, "three-site.yaml") {
		t.Error("nil and empty")
	}
}

// A known value pins the canonical form: if this changes, every stored hash
// changed, which needs hashVersion to move.
func TestHashIsStableAcrossReleases(t *testing.T) {
	const pinned = "8cf7e5a2a645331da0df33b34c521d5f116eada3c3b7892dfaf7c9be5aca3285"
	if got := hashOf(t, "three-site.yaml"); got != pinned {
		t.Errorf("three-site hash = %s, pinned %s", got, pinned)
	}
}
