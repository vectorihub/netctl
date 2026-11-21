package phase1

import (
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vectorihub/netctl"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/internal/topogen"
	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// chaosCases is how many random cases a run plans: E2E_CHAOS_CASES, or 100. A case takes about
// four seconds on four cores, so the guide's 2,000 per push and 20,000 nightly are set with
// E2E_CHAOS_CASES by CI. A single case is reproduced with E2E_CHAOS_SEED.
func chaosCases() []uint64 {
	if s := os.Getenv("E2E_CHAOS_SEED"); s != "" {
		n, _ := strconv.ParseUint(s, 10, 64)
		return []uint64{n}
	}
	count := 100
	if s := os.Getenv("E2E_CHAOS_CASES"); s != "" {
		count, _ = strconv.Atoi(s)
	}
	seeds := make([]uint64, count)
	for i := range seeds {
		seeds[i] = uint64(i + 1)
	}
	return seeds
}

// invariant names the five invariants of the chaos group.
type invariant string

// The invariants, by test ID.
const (
	inv120 invariant = "P1-E2E-120" // a successful plan simulates and forwards correctly
	inv121 invariant = "P1-E2E-121" // no validator errors, clean IPAM
	inv122 invariant = "P1-E2E-122" // surviving nodes never change address
	inv123 invariant = "P1-E2E-123" // nothing crashes or fails internally
	inv124 invariant = "P1-E2E-124" // the network returns to its baseline
)

// violation is one invariant broken in one case.
type violation struct {
	inv  invariant
	seed uint64
	what string
}

// String writes a violation the way the chaos report lists it.
func (v violation) String() string { return fmt.Sprintf("%s seed %d: %s", v.inv, v.seed, v.what) }

func init() {
	registerSlow("P1-E2E-120", func(w *e2e.W) { runChaos(w, inv120) })
	registerSlow("P1-E2E-121", func(w *e2e.W) { runChaos(w, inv121) })
	registerSlow("P1-E2E-122", func(w *e2e.W) { runChaos(w, inv122) })
	registerSlow("P1-E2E-123", func(w *e2e.W) { runChaos(w, inv123) })
	registerSlow("P1-E2E-124", func(w *e2e.W) { runChaos(w, inv124) })
}

var (
	chaosOnce sync.Once
	chaosAll  []violation
)

// runChaos runs every case once for the whole suite (the five tests share the results) and
// fails the test for its invariant if any case broke it.
func runChaos(w *e2e.W, inv invariant) {
	chaosOnce.Do(func() {
		var mu sync.Mutex
		seeds := make(chan uint64)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for seed := range seeds {
					vs := runCase(seed)
					mu.Lock()
					chaosAll = append(chaosAll, vs...)
					mu.Unlock()
				}
			}()
		}
		for _, s := range chaosCases() {
			seeds <- s
		}
		close(seeds)
		wg.Wait()
	})
	n := 0
	for _, v := range chaosAll {
		if v.inv == inv {
			w.T.Errorf("%s", v)
			if n++; n >= 10 {
				break
			}
		}
	}
	w.T.Logf("%s: %d cases", inv, len(chaosCases()))
}

// timeline is the health stream of one case: for every direct link, the moments it is failing.
type timeline struct {
	links  []string
	outage map[string][]time.Duration // start offsets of three failed probes
}

// signalsBetween returns the probes of every link from both ends, five seconds apart, in the
// half-open window [from, to): failed inside an outage, good otherwise. Probing never stops,
// because a link nobody hears from goes stale.
func (t timeline) signalsBetween(from, to time.Duration) []health.Signal {
	s := new(e2e.Signals)
	for _, l := range t.links {
		for at := from.Truncate(5 * time.Second); at < to; at += 5 * time.Second {
			if at < from {
				continue
			}
			ok := true
			for _, start := range t.outage[l] {
				if at >= start && at < start+15*time.Second {
					ok = false
				}
			}
			s.Probes(l, ok, 1, at, 0)
		}
	}
	return s.List()
}

// checkpoints returns the moments at which a case plans while the links are living through
// their outages, in order and without repeats: twenty seconds into every outage, when the link
// is down, and ninety seconds after it ends, when it has had time to prove itself again. A
// plan at each of them means a failover that misbehaves while a link is down cannot hide
// behind a healthy plan at the end. Quiet stretches are still planned every ten minutes.
func (t timeline) checkpoints() []time.Duration {
	seen := map[time.Duration]bool{}
	for _, minutes := range []int{10, 20, 30} {
		seen[time.Duration(minutes)*time.Minute] = true
	}
	for _, starts := range t.outage {
		for _, start := range starts {
			seen[start+20*time.Second] = true
			seen[start+15*time.Second+90*time.Second] = true
		}
	}
	var out []time.Duration
	for at := range seen {
		out = append(out, at)
	}
	slices.Sort(out)
	return out
}

// runCase plans one random network through a random life and returns the invariants it broke.
func runCase(seed uint64) (found []violation) {
	rng := rand.New(rand.NewPCG(seed, 99))
	add := func(inv invariant, format string, args ...any) {
		found = append(found, violation{inv, seed, fmt.Sprintf(format, args...)})
	}
	defer func() {
		if r := recover(); r != nil {
			add(inv123, "panic: %v", r)
		}
	}()
	topo, spec := topogen.Random(seed)
	w := e2e.New(nopTB{}, "chaos")
	w.NoDisk = true
	clock := func(d time.Duration) { w.At(d) }
	var prev *e2e.Out
	plan := func(label string, t *topology.Topology, signals []health.Signal, at time.Duration) *e2e.Out {
		clock(at)
		o := w.Plan(e2e.Options{Topology: t, From: prev, Signals: signals, Name: label, SkipSweep: true})
		check(add, o, spec)
		if o.OK() {
			prev = o
		}
		return o
	}

	tl := timeline{links: topogen.DirectLinks(topo), outage: map[string][]time.Duration{}}
	for _, l := range tl.links {
		if rng.IntN(2) == 0 {
			continue
		}
		at := 60*time.Second + time.Duration(rng.IntN(60))*time.Second
		for k := 0; k < 1+rng.IntN(3) && at < 1500*time.Second; k++ {
			tl.outage[l] = append(tl.outage[l], at.Truncate(5*time.Second))
			at += 200*time.Second + time.Duration(rng.IntN(100))*time.Second
		}
	}
	fresh := plan("out-0", topo, nil, 0)
	if !fresh.OK() {
		add(inv123, "the fresh plan of a generated network was refused: %v", fresh.Err)
		return found
	}
	proof := new(e2e.Signals)
	for _, l := range tl.links {
		proof.Probes(l, true, 5, 5*time.Second, 5*time.Second)
	}
	base := plan("out-1", topo, proof.List(), 30*time.Second)
	if !base.OK() {
		add(inv123, "the proven baseline plan of a generated network was refused: %v", base.Err)
		return found
	}
	cursor := 30 * time.Second
	for i, end := range tl.checkpoints() {
		plan(fmt.Sprintf("slice-%d", i+1), topo, tl.signalsBetween(cursor+5*time.Second, end), end)
		cursor = end
	}
	heal := cursor + 4*time.Minute
	final := plan("healed", topo, tl.signalsBetween(cursor+5*time.Second, heal), heal)
	if !final.OK() {
		add(inv124, "the healed plan was refused: %v", final.Err)
	} else if final.Result.PlanID != base.Result.PlanID {
		add(inv124, "after healing the plan is %s, the baseline %s (%d nodes, %d direct links)", final.Result.PlanID, base.Result.PlanID, spec.Nodes, spec.Direct)
	}
	cursor = heal

	cur := topo
	for edit := 0; edit < 10 && prev != nil; edit++ {
		next := mutate(cur, rng, edit)
		cursor += 30 * time.Second
		tl.links = topogen.DirectLinks(next)
		before := prev
		o := plan(fmt.Sprintf("edit-%d", edit), next, tl.signalsBetween(cursor-25*time.Second, cursor), cursor)
		if o.OK() {
			sameAddresses(add, before, o, next)
			cur = next
		}
	}
	return found
}

// mutate returns a copy of the topology after one random day-two edit: add a spoke, remove a
// spoke, add a direct link or change a link cost.
func mutate(t *topology.Topology, rng *rand.Rand, n int) *topology.Topology {
	out := *t
	out.Sites = append([]topology.Site(nil), t.Sites...)
	out.Nodes = append([]topology.Node(nil), t.Nodes...)
	out.Links = append([]topology.Link(nil), t.Links...)
	var spokes []types.NodeID
	var hub types.NodeID
	for _, nd := range out.Nodes {
		if nd.Role == types.RoleSpoke {
			spokes = append(spokes, nd.ID)
		} else if hub == "" {
			hub = nd.ID
		}
	}
	switch rng.IntN(4) {
	case 0:
		id := types.NodeID(fmt.Sprintf("x%03d", n))
		site := types.SiteID(fmt.Sprintf("xsite-%03d", n))
		out.Sites = append(out.Sites, topology.Site{ID: site, Provider: "owned"})
		out.Nodes = append(out.Nodes, topology.Node{ID: id, Site: site, Role: types.RoleSpoke, NAT: topology.NATBehind, Advertises: []topology.Advertisement{{Site: true}}})
		out.Links = append(out.Links, topology.Link{Kind: types.LinkHubSpoke, A: hub, B: id})
	case 1:
		if len(spokes) > 1 {
			gone := spokes[rng.IntN(len(spokes))]
			out.Nodes = filter(out.Nodes, func(nd topology.Node) bool { return nd.ID != gone })
			out.Links = filter(out.Links, func(l topology.Link) bool { return l.A != gone && l.B != gone })
		}
	case 2:
		if len(spokes) > 1 {
			a, b := spokes[rng.IntN(len(spokes))], spokes[rng.IntN(len(spokes))]
			if a != b && !hasLink(out.Links, a, b) {
				out.Links = append(out.Links, topology.Link{Kind: types.LinkDirect, A: a, B: b, Cost: 10, Health: topology.HealthRequired})
			}
		}
	default:
		if len(out.Links) > 0 {
			i := rng.IntN(len(out.Links))
			if out.Links[i].Kind == types.LinkDirect {
				out.Links[i].Cost = 5 + rng.IntN(60)
			}
		}
	}
	out.Normalize()
	return &out
}

// filter returns the items for which keep is true.
func filter[T any](items []T, keep func(T) bool) []T {
	var out []T
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

// hasLink reports whether the two nodes are already linked.
func hasLink(links []topology.Link, a, b types.NodeID) bool {
	for _, l := range links {
		if (l.A == a && l.B == b) || (l.A == b && l.B == a) {
			return true
		}
	}
	return false
}

// check applies the per-plan invariants to a plan: 123 (nothing fails internally), 120 and 121
// for a plan that succeeded.
func check(add func(invariant, string, ...any), o *e2e.Out, spec topogen.Spec) {
	if o.Err != nil {
		if !types.HasCode(o.Err, types.ErrValidationFailed) && !types.HasCode(o.Err, types.ErrPoolExhausted) && !types.HasCode(o.Err, types.ErrInvalidTopology) {
			add(inv123, "%s failed with %v", o.Name, o.Err)
		}
		return
	}
	if !o.Result.Simulation.Passed {
		add(inv120, "%s succeeded but its simulation failed", o.Name)
	}
	pairs, err := o.Oracle()
	if err != nil {
		add(inv120, "%s: oracle: %v", o.Name, err)
	} else {
		for _, f := range o.Result.Simulation.Flows {
			if f.Flow.Family.String() != "overlay" || f.Verdict.String() != "OK" {
				continue
			}
			p := pairs[[2]string{string(f.Flow.From), string(f.Flow.To)}]
			if !p.Forward.Delivered || !p.Return.Delivered || !p.Symmetric {
				add(inv120, "%s: the simulation delivers %s but the oracle does not (%s%s)", o.Name, f.Flow.Label, p.Forward.Problem, p.Return.Problem)
				break
			}
		}
	}
	if errs := o.Result.Validation.Errors(); len(errs) > 0 {
		add(inv121, "%s succeeded with validator errors", o.Name)
	}
	if problems, counts := e2e.CheckIPAM(o.Files["ipam.json"]); len(problems) > 0 || counts.Total != 1<<24 {
		add(inv121, "%s: ipam: %v %+v", o.Name, problems, counts)
	}
	for path, data := range o.Files {
		if strings.Contains(string(data), "panic:") || strings.Contains(string(data), "goroutine ") {
			add(inv123, "%s: %s holds a crash trace", o.Name, path)
		}
	}
}

// sameAddresses is invariant 122: a node present before and after an edit has the same
// overlay address and the same site prefix.
func sameAddresses(add func(invariant, string, ...any), before, after *e2e.Out, now *topology.Topology) {
	for _, n := range after.Nodes() {
		b, ok := before.Files["nodes/"+n+".json"]
		if !ok {
			continue
		}
		a := after.Files["nodes/"+n+".json"]
		if field(b, "address") != field(a, "address") || field(b, "site_prefix") != field(a, "site_prefix") {
			add(inv122, "%s changed address across %s -> %s", n, before.Name, after.Name)
		}
	}
	_ = now
	_ = netctl.Diff
}

// field extracts the first value of a JSON key from rendered config text.
func field(data []byte, key string) string {
	text := string(data)
	i := strings.Index(text, `"`+key+`"`)
	if i < 0 {
		return ""
	}
	rest := text[i+len(key)+2:]
	end := strings.IndexAny(rest, ",\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// nopTB is a testing.TB for the chaos workspaces: they never fail a test themselves, they
// only plan, and the invariants are reported by runChaos.
type nopTB struct{ testing.TB }

// Helper does nothing.
func (nopTB) Helper() {}

// Cleanup does nothing: a chaos workspace has no files to keep.
func (nopTB) Cleanup(func()) {}

// TempDir returns no directory; chaos workspaces run with NoDisk.
func (nopTB) TempDir() string { return "" }

// Failed reports false.
func (nopTB) Failed() bool { return false }

// Logf drops the message.
func (nopTB) Logf(string, ...any) {}

// Fatalf panics so runCase's recover reports it as a crash.
func (nopTB) Fatalf(f string, a ...any) { panic(fmt.Sprintf(f, a...)) }
