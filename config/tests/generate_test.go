package tests

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// TestBuildersHubPeerWhenHealthy is P1-CFG-002: with the direct link healthy,
// builder-1's hub peer lists the hub's site and the hub's /32, and nothing that is
// reached directly; its dgx-spark peer lists dgx-spark's site, its /32 and the
// probe address.
func TestBuildersHubPeerWhenHealthy(t *testing.T) {
	c := threeSite(t, true)[builder]
	if got := peerOf(t, c, hub).AllowedPrefixes; !equalPrefixes(got, prefixes("10.42.0.0/20", "10.42.240.1/32")) {
		t.Errorf("hub peer: %v", got)
	}
	if got := peerOf(t, c, dgx).AllowedPrefixes; !equalPrefixes(got, prefixes("10.42.32.0/20", "10.42.240.3/32", "10.42.252.2/32")) {
		t.Errorf("direct peer: %v", got)
	}
}

// TestBuildersPeersAfterFailover is P1-CFG-003: after the direct link fails the DGX
// site and the DGX /32 have moved from the dgx-spark peer to the hub peer, and the
// probe address stays where it was so probes keep testing the direct link.
func TestBuildersPeersAfterFailover(t *testing.T) {
	c := threeSite(t, false)[builder]
	if got := peerOf(t, c, hub).AllowedPrefixes; !equalPrefixes(got, prefixes("10.42.0.0/20", "10.42.32.0/20", "10.42.240.1/32", "10.42.240.3/32")) {
		t.Errorf("hub peer after failover: %v", got)
	}
	if got := peerOf(t, c, dgx).AllowedPrefixes; !equalPrefixes(got, prefixes("10.42.252.2/32")) {
		t.Errorf("direct peer after failover: %v", got)
	}
	if len(c.Health.Probes) != 1 || c.Health.Probes[0].Target != netip.MustParseAddr("10.42.252.2") {
		t.Errorf("the probe moved: %+v", c.Health.Probes)
	}
}

// TestHubConfigDoesNotMoveWithFailover checks the hub's peers list the same prefixes
// healthy and failed over: the hub reaches each spoke directly either way.
func TestHubConfigDoesNotMoveWithFailover(t *testing.T) {
	up, down := threeSite(t, true)[hub], threeSite(t, false)[hub]
	if d := config.Diff(up, down); !d.Empty() {
		t.Errorf("the hub's config changed with the direct link: %s", d.Text())
	}
	if got := peerOf(t, up, builder).AllowedPrefixes; !equalPrefixes(got, prefixes("10.42.16.0/20", "10.42.240.2/32")) {
		t.Errorf("hub's builder peer: %v", got)
	}
	if got := peerOf(t, up, dgx).AllowedPrefixes; !equalPrefixes(got, prefixes("10.42.32.0/20", "10.42.240.3/32")) {
		t.Errorf("hub's dgx peer: %v", got)
	}
}

// TestForwardingOnlyForHubs is P1-CFG-005: the hub forwards and the spokes do not.
func TestForwardingOnlyForHubs(t *testing.T) {
	for node, c := range threeSite(t, true) {
		if want := node == hub; c.Forwarding != want {
			t.Errorf("%s: forwarding %v, want %v", node, c.Forwarding, want)
		}
		if want := map[bool]string{true: "hub", false: "spoke"}[node == hub]; c.Role != want {
			t.Errorf("%s: role %q, want %q", node, c.Role, want)
		}
	}
}

// TestKeepaliveOnlyWhereNeeded is P1-CFG-006: keepalive is 25 s exactly on the
// spokes' peers, where the spoke is behind NAT, and 0 on the hub's, where neither
// end needs it.
func TestKeepaliveOnlyWhereNeeded(t *testing.T) {
	got := map[string]int{}
	for node, c := range threeSite(t, true) {
		for _, p := range c.Peers {
			got[string(node)+">"+string(p.Node)] = p.Keepalive
		}
	}
	want := map[string]int{
		"builder-1>aws-hub": 25, "builder-1>dgx-spark": 25, "dgx-spark>aws-hub": 25, "dgx-spark>builder-1": 25,
		"aws-hub>builder-1": 0, "aws-hub>dgx-spark": 0,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: keepalive %d, want %d", k, got[k], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("peers %v, want exactly %d", got, len(want))
	}
}

// TestNATedPeerHasNoEndpoints is P1-CFG-007: a peer behind NAT is listed without
// endpoints, and when neither end can be dialled the peer needs traversal. The hub
// is dialled by its spokes, so it is never marked as needing it, and a spoke's
// view of the hub carries the hub's one endpoint.
func TestNATedPeerHasNoEndpoints(t *testing.T) {
	all := threeSite(t, true)
	d := peerOf(t, all[builder], dgx)
	if len(d.Endpoints) != 0 || !d.NeedsTraversal {
		t.Errorf("builder-1's dgx-spark peer: endpoints %v, needs_traversal %v; want none and true", d.Endpoints, d.NeedsTraversal)
	}
	h := peerOf(t, all[builder], hub)
	if len(h.Endpoints) != 1 || h.Endpoints[0] != netip.MustParseAddrPort("203.0.113.10:51820") || h.NeedsTraversal {
		t.Errorf("builder-1's hub peer: endpoints %v, needs_traversal %v", h.Endpoints, h.NeedsTraversal)
	}
	b := peerOf(t, all[hub], builder)
	if len(b.Endpoints) != 0 || b.NeedsTraversal {
		t.Errorf("the hub's builder-1 peer: endpoints %v, needs_traversal %v; want none and false", b.Endpoints, b.NeedsTraversal)
	}
}

// TestReferenceConfigOfBuilder checks the whole of builder-1's config, healthy,
// against the reference in the end-to-end guide: every field, not only the ones
// named by a rule.
func TestReferenceConfigOfBuilder(t *testing.T) {
	c := threeSite(t, true)[builder]
	if c.Schema != config.Schema || c.Node != builder || c.Role != "spoke" || c.Forwarding {
		t.Errorf("header: %+v", c)
	}
	if c.Overlay.Address != netip.MustParsePrefix("10.42.240.2/32") || c.Overlay.ListenPort != 51820 {
		t.Errorf("overlay %+v", c.Overlay)
	}
	if c.SitePrefix == nil || *c.SitePrefix != netip.MustParsePrefix("10.42.16.0/20") {
		t.Errorf("site prefix %v", c.SitePrefix)
	}
	if !equalPrefixes(c.Advertise, prefixes("10.42.16.0/20", "10.42.240.2/32")) {
		t.Errorf("advertise %v", c.Advertise)
	}
	d := peerOf(t, c, dgx)
	if d.Role != config.PeerDirect || d.PublicKeyRef != "node:dgx-spark" || d.Link != direct {
		t.Errorf("direct peer %+v", d)
	}
	if d.Probe == nil || d.Probe.Local != netip.MustParsePrefix("10.42.252.1/30") || d.Probe.Remote != netip.MustParseAddr("10.42.252.2") {
		t.Errorf("probe %+v", d.Probe)
	}
	if h := peerOf(t, c, hub); h.Role != config.PeerHub || h.Probe != nil {
		t.Errorf("hub peer %+v", h)
	}
	wantRoutes := []config.Route{
		{Prefix: netip.MustParsePrefix("10.42.0.0/20"), Via: hub, Metric: 20, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.32.0/20"), Via: dgx, Metric: 10, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.32.0/20"), Via: hub, Metric: 40, Backup: true},
		{Prefix: netip.MustParsePrefix("10.42.240.1/32"), Via: hub, Metric: 20, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.240.3/32"), Via: dgx, Metric: 10, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.240.3/32"), Via: hub, Metric: 40, Backup: true},
	}
	if len(c.Routes) != len(wantRoutes) {
		t.Fatalf("routes %+v", c.Routes)
	}
	for i, w := range wantRoutes {
		if c.Routes[i] != w {
			t.Errorf("route %d: %+v, want %+v", i, c.Routes[i], w)
		}
	}
	h := c.Health
	if len(h.Probes) != 1 || h.Probes[0].Link != direct || h.Probes[0].Interval != 5 || h.FailThreshold != 3 || h.RecoverThreshold != 5 || h.HoldDownSeconds != 60 {
		t.Errorf("health %+v", h)
	}
	if len(c.Policy) != 0 {
		t.Errorf("policy %v", c.Policy)
	}
}

// TestRoutesAfterFailoverMatchTheReference checks builder-1's routes after the
// direct link fails: the backups are active and are no longer marked inactive, and
// the direct routes are gone.
func TestRoutesAfterFailoverMatchTheReference(t *testing.T) {
	c := threeSite(t, false)[builder]
	want := []config.Route{
		{Prefix: netip.MustParsePrefix("10.42.0.0/20"), Via: hub, Metric: 20, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.32.0/20"), Via: hub, Metric: 40, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.240.1/32"), Via: hub, Metric: 20, Active: true},
		{Prefix: netip.MustParsePrefix("10.42.240.3/32"), Via: hub, Metric: 40, Active: true},
	}
	if len(c.Routes) != len(want) {
		t.Fatalf("routes %+v", c.Routes)
	}
	for i, w := range want {
		if c.Routes[i] != w {
			t.Errorf("route %d: %+v, want %+v", i, c.Routes[i], w)
		}
	}
}

// TestDGXIsTheMirrorOfBuilder checks dgx-spark's config is builder-1's with the two
// names swapped, which is what the guide says of the failed-over config.
func TestDGXIsTheMirrorOfBuilder(t *testing.T) {
	for _, up := range []bool{true, false} {
		all := threeSite(t, up)
		b, d := all[builder], all[dgx]
		bp, dp := peerOf(t, b, dgx), peerOf(t, d, builder)
		if bp.Probe.Local.Addr() != dp.Probe.Remote || bp.Probe.Remote != dp.Probe.Local.Addr() {
			t.Errorf("probes are not mirrored: %+v and %+v", bp.Probe, dp.Probe)
		}
		if len(b.Routes) != len(d.Routes) || len(b.Peers) != len(d.Peers) || len(bp.AllowedPrefixes) != len(dp.AllowedPrefixes) {
			t.Errorf("directUp=%v: shapes differ", up)
		}
	}
}

// TestPlanIDIsInEveryConfigAndFollowsContent checks all configs of a plan carry the
// same plan ID, it names the topology, it can be worked out again from the finished
// configs, and it differs between the healthy and failed-over plans.
func TestPlanIDIsInEveryConfigAndFollowsContent(t *testing.T) {
	up, down := threeSite(t, true), threeSite(t, false)
	id := up[hub].PlanID
	if !strings.HasPrefix(id, "three-site@") || len(id) != len("three-site@")+8 {
		t.Fatalf("plan id %q", id)
	}
	var list []*config.NodeConfig
	for _, c := range up {
		if c.PlanID != id {
			t.Errorf("%s has plan id %q, want %q", c.Node, c.PlanID, id)
		}
		list = append(list, c)
	}
	again, err := config.PlanID("three-site", list)
	if err != nil || again != id {
		t.Errorf("recomputed %q, %v; want %q", again, err, id)
	}
	if down[hub].PlanID == id {
		t.Error("the failed-over plan has the healthy plan's ID")
	}
}

// TestGenerateReportsEveryProblem checks the generator refuses bad inputs with the
// right codes, and reports several at once.
func TestGenerateReportsEveryProblem(t *testing.T) {
	if _, err := config.Generate(config.Inputs{}); !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("no topology: %v", err)
	}
	t.Run("missing addresses and tables", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		delete(in.Overlay, builder)
		delete(in.Sites, "dgx")
		delete(in.Transit, direct)
		delete(in.Tables, hub)
		_, err := config.Generate(in)
		if !types.HasCode(err, types.ErrNotFound) {
			t.Fatalf("%v, want NOT_FOUND", err)
		}
		for _, want := range []string{"overlay address", "block for site", "transit /30", "routing table"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error does not mention %q: %v", want, err)
			}
		}
	})
	t.Run("a prefix active through two peers", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		table := in.Tables[builder]
		bad := route.Route{Prefix: netip.MustParsePrefix("10.42.32.0/20"), NextHop: hub, Via: "aws-hub~builder-1", Metric: 5, Source: route.SourceStatic, Active: true}
		table.Routes = append(table.Routes, bad)
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidConfig) || !strings.Contains(err.Error(), "10.42.32.0/20") {
			t.Errorf("%v, want INVALID_CONFIG naming the prefix", err)
		}
	})
	t.Run("an active route to a node that is not a peer", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		in.Tables[hub].Routes = append(in.Tables[hub].Routes, route.Route{Prefix: netip.MustParsePrefix("10.99.0.0/16"), NextHop: "nowhere", Active: true})
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidConfig) {
			t.Errorf("%v, want INVALID_CONFIG", err)
		}
	})
	t.Run("a transit block that is not a /30", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		in.Transit[direct] = netip.MustParsePrefix("10.42.252.0/29")
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidConfig) {
			t.Errorf("%v, want INVALID_CONFIG", err)
		}
	})
	t.Run("a health time that is not whole seconds", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		in.Health = health.DefaultConfig()
		in.Health.HoldDown = 1500 * time.Millisecond
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidConfig) {
			t.Errorf("%v, want INVALID_CONFIG", err)
		}
	})
	t.Run("a bad health setting", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		in.Health.FailThreshold = -1
		in.Health.RecoverThreshold = 1
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidConfig) {
			t.Errorf("%v, want INVALID_CONFIG", err)
		}
	})
	t.Run("a bad listen port", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		in.ListenPort = 70000
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidConfig) {
			t.Errorf("%v, want INVALID_CONFIG", err)
		}
	})
	t.Run("two links to one neighbour", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		in.Topology.Links = append(in.Topology.Links, in.Topology.Links[0])
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidTopology) {
			t.Errorf("%v, want INVALID_TOPOLOGY", err)
		}
	})
	t.Run("a link to a node that is not there", func(t *testing.T) {
		in := threeSiteInputs(t, true)
		in.Topology.Links = append(in.Topology.Links, in.Topology.Links[0])
		in.Topology.Links[len(in.Topology.Links)-1].B = "ghost"
		if _, err := config.Generate(in); !types.HasCode(err, types.ErrInvalidTopology) {
			t.Errorf("%v, want INVALID_TOPOLOGY", err)
		}
	})
}

// TestSettingsFlowIntoEveryConfig checks the health thresholds and the listen port
// the caller sets reach every config.
func TestSettingsFlowIntoEveryConfig(t *testing.T) {
	in := threeSiteInputs(t, true)
	in.ListenPort = 51999
	in.Health = health.DefaultConfig()
	in.Health.FailThreshold, in.Health.RecoverThreshold = 4, 7
	in.Health.HoldDown, in.Health.ProbeInterval, in.Health.StaleAfter = 90*time.Second, 10*time.Second, 40*time.Second
	configs, err := config.Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range configs {
		h := c.Health
		if c.Overlay.ListenPort != 51999 || h.FailThreshold != 4 || h.RecoverThreshold != 7 || h.HoldDownSeconds != 90 {
			t.Errorf("%s: port %d, health %+v", c.Node, c.Overlay.ListenPort, h)
		}
		for _, p := range h.Probes {
			if p.Interval != 10 {
				t.Errorf("%s: probe interval %d, want 10", c.Node, p.Interval)
			}
		}
	}
}

// TestFromPlanReportsEveryMissingAddress checks FromPlan names each address the plan
// lacks, all at once, and refuses nil.
func TestFromPlanReportsEveryMissingAddress(t *testing.T) {
	if _, err := config.FromPlan(nil, nil, nil); !types.HasCode(err, types.ErrInvalidValue) {
		t.Errorf("nil: %v", err)
	}
	plan := threeSiteInputs(t, true)
	empty, err := newEmptyPlan()
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.FromPlan(plan.Topology, empty, plan.Tables)
	if !types.HasCode(err, types.ErrNotFound) {
		t.Fatalf("%v, want NOT_FOUND", err)
	}
	for _, want := range []string{"aws-hub", "builder-1", "dgx-spark", "site", "transit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}
