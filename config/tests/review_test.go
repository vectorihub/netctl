package tests

import (
	"math/rand/v2"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// TestEveryProblemIsReportedTogetherAndInAFixedOrder checks a bad health setting no
// longer hides the other problems, and that the same bad input gives the same error
// text every time even with two settings wrong at once.
func TestEveryProblemIsReportedTogetherAndInAFixedOrder(t *testing.T) {
	in := threeSiteInputs(t, true)
	in.Health = health.DefaultConfig()
	in.Health.FailThreshold = 0
	in.Health.ProbeInterval, in.Health.HoldDown = 1500*time.Millisecond, 61500*time.Millisecond
	in.ListenPort = 70000
	delete(in.Overlay, builder)
	delete(in.Tables, hub)
	_, first := config.Generate(in)
	if !types.HasCode(first, types.ErrInvalidConfig) || !types.HasCode(first, types.ErrNotFound) {
		t.Fatalf("%v, want both INVALID_CONFIG and NOT_FOUND", first)
	}
	for _, want := range []string{"fail_threshold", "probe_interval", "hold_down", "listen port", "overlay address", "routing table"} {
		if !strings.Contains(first.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, first)
		}
	}
	for i := 0; i < 50; i++ {
		if _, again := config.Generate(in); again.Error() != first.Error() {
			t.Fatalf("run %d: the error text changed:\n%v\n%v", i, first, again)
		}
	}
}

// TestInvalidAddressesAreRefused checks an overlay address or site block that is
// present but not usable is an error, not a config the schema would reject.
func TestInvalidAddressesAreRefused(t *testing.T) {
	for name, mutate := range map[string]func(*config.Inputs){
		"empty overlay":     func(in *config.Inputs) { in.Overlay[builder] = netip.Prefix{} },
		"overlay not a /32": func(in *config.Inputs) { in.Overlay[builder] = netip.MustParsePrefix("10.42.240.0/24") },
		"ipv6 overlay":      func(in *config.Inputs) { in.Overlay[builder] = netip.MustParsePrefix("fd00::2/128") },
		"empty site block":  func(in *config.Inputs) { in.Sites["builder"] = netip.Prefix{} },
		"ipv6 site block":   func(in *config.Inputs) { in.Sites["builder"] = netip.MustParsePrefix("fd00::/48") },
		"empty advertisement": func(in *config.Inputs) {
			in.Topology.Nodes[0].Advertises = append(slices.Clone(in.Topology.Nodes[0].Advertises), advertisement(netip.Prefix{}))
		},
	} {
		in := threeSiteInputs(t, true)
		mutate(&in)
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidPrefix) {
			t.Errorf("%s: %v, want INVALID_PREFIX", name, err)
		}
	}
}

// TestPolicyPortsAreASet checks the order of a rule's ports, and a repeat, do not
// change the bytes or the hash, while the order of the rules still does.
func TestPolicyPortsAreASet(t *testing.T) {
	entry := func(ports ...uint16) []config.PolicyEntry {
		return []config.PolicyEntry{{Action: "allow", From: "a", To: "b", Ports: ports}}
	}
	with := func(policy []config.PolicyEntry) string {
		c := sample()
		c.Policy = policy
		_, hash := encoded(t, c)
		return hash
	}
	if with(entry(80, 443)) != with(entry(443, 80)) || with(entry(80, 443)) != with(entry(443, 80, 80)) {
		t.Error("the order or repetition of ports changed the hash")
	}
	if with(entry(80, 443)) == with(entry(80, 8443)) {
		t.Error("different ports gave the same hash")
	}
	two := []config.PolicyEntry{{Action: "allow", From: "a", To: "b"}, {Action: "deny", From: "c", To: "d"}}
	swapped := []config.PolicyEntry{two[1], two[0]}
	if with(two) == with(swapped) {
		t.Error("the order of rules did not change the hash, and it must")
	}
}

// TestPeersOfOneNodeAreInAFixedOrder checks two peers that share a node, which a
// hand-built config can have, are ordered by what else they hold and not by the
// order they came in.
func TestPeersOfOneNodeAreInAFixedOrder(t *testing.T) {
	mk := func(link types.LinkID, keepalive int) config.Peer {
		return config.Peer{Node: "dgx-spark", Link: link, Role: config.PeerDirect, PublicKeyRef: "node:dgx-spark", Keepalive: keepalive}
	}
	a, b, c := mk("a~dgx-spark", 0), mk("b~dgx-spark", 25), mk("b~dgx-spark", 0)
	var want string
	for i, order := range [][]config.Peer{{a, b, c}, {c, b, a}, {b, a, c}, {c, a, b}} {
		cfg := &config.NodeConfig{Schema: config.Schema, Node: "x", Role: "spoke", Peers: slices.Clone(order)}
		_, hash := encoded(t, cfg)
		if i == 0 {
			want = hash
		} else if hash != want {
			t.Errorf("order %d gave a different hash", i)
		}
	}
}

// TestAllowedPrefixesConflictsDoNotDependOnOrder checks that when two peers both
// claim a prefix the result is the same whatever order the routes and the probe
// map are in: the same conflicts and the same lists.
func TestAllowedPrefixesConflictsDoNotDependOnOrder(t *testing.T) {
	r := func(prefix, via string, metric int) route.Route {
		return route.Route{Prefix: netip.MustParsePrefix(prefix), NextHop: types.NodeID(via), Metric: metric, Active: true}
	}
	routes := []route.Route{r("10.1.0.0/16", "a", 1), r("10.1.0.0/16", "b", 2), r("10.2.0.0/16", "c", 1), r("10.2.0.0/16", "a", 3), r("10.3.0.0/16", "b", 1)}
	probes := map[types.NodeID]netip.Addr{"a": netip.MustParseAddr("10.9.0.1"), "b": netip.MustParseAddr("10.9.0.2"), "c": netip.MustParseAddr("10.1.0.0")}
	wantLists, wantConflicts := config.AllowedPrefixes(&route.Table{Routes: slices.Clone(routes)}, probes)
	if len(wantConflicts) != 2 {
		t.Fatalf("conflicts %+v, want two", wantConflicts)
	}
	rng := rand.New(rand.NewPCG(63, 1))
	for i := 0; i < 200; i++ {
		shuffled := slices.Clone(routes)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		lists, conflicts := config.AllowedPrefixes(&route.Table{Routes: shuffled}, probes)
		if !reflect.DeepEqual(lists, wantLists) || !reflect.DeepEqual(conflicts, wantConflicts) {
			t.Fatalf("run %d: the result depends on order:\n%v %v\n%v %v", i, lists, conflicts, wantLists, wantConflicts)
		}
	}
}
