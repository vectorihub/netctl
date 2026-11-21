package tests

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/topology"
)

// shuffle puts the lists of a topology in a random order. The topology is not normalised
// afterwards: a file's order is whatever its author wrote.
func shuffle(rng *rand.Rand, in plan.Inputs) plan.Inputs {
	topo := *in.Topology
	topo.Nodes = slices.Clone(topo.Nodes)
	topo.Links = slices.Clone(topo.Links)
	topo.Sites = slices.Clone(topo.Sites)
	rng.Shuffle(len(topo.Nodes), func(i, j int) { topo.Nodes[i], topo.Nodes[j] = topo.Nodes[j], topo.Nodes[i] })
	rng.Shuffle(len(topo.Links), func(i, j int) { topo.Links[i], topo.Links[j] = topo.Links[j], topo.Links[i] })
	rng.Shuffle(len(topo.Sites), func(i, j int) { topo.Sites[i], topo.Sites[j] = topo.Sites[j], topo.Sites[i] })
	for i := range topo.Links { // which end is named first is the file's business too
		if rng.IntN(2) == 0 {
			topo.Links[i].A, topo.Links[i].B = topo.Links[i].B, topo.Links[i].A
		}
	}
	in.Topology = &topo
	return in
}

// bytes renders everything a plan produces as bytes: the snapshot, every config and the
// validation report.
func bytes(t *testing.T, r plan.Result) string {
	t.Helper()
	out, err := ipam.Marshal(r.IPAM)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, c := range r.Configs {
		data, err := c.Clone().Encode()
		if err != nil {
			t.Fatal(err)
		}
		text += string(data)
	}
	j, err := r.Validation.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return text + string(j) + r.PlanID
}

// TestShufflingTheFileChangesNothing is the reproducibility test of the lifecycle: fifty
// plans from the same network with its sites, nodes and links in a different order, and the
// ends of its links swapped, produce the same address plan, the same configs, the same
// validation report and the same plan ID, byte for byte. Allocation is in sorted order and
// never in the order of the file, so who gets .2 and who gets .3 does not depend on it.
func TestShufflingTheFileChangesNothing(t *testing.T) {
	want := bytes(t, mustPlan(t, fresh(t)))
	rng := rand.New(rand.NewPCG(70, 1))
	for i := 0; i < 50; i++ {
		got := bytes(t, mustPlan(t, shuffle(rng, fresh(t))))
		if got != want {
			t.Fatalf("shuffle %d gave a different plan", i)
		}
	}
}

// TestShufflingAfterTheFirstPlanChangesNothingEither checks the same on later plans, where
// idempotency and not sorting is what keeps the addresses.
func TestShufflingAfterTheFirstPlanChangesNothingEither(t *testing.T) {
	first := mustPlan(t, fresh(t))
	rng := rand.New(rand.NewPCG(70, 2))
	for i := 0; i < 20; i++ {
		in := shuffle(rng, fresh(t))
		in.IPAM = first.IPAM
		r := mustPlan(t, in)
		if len(r.Changes) != 0 || bytes(t, r) != bytes(t, first) {
			t.Fatalf("shuffle %d changed a plan that should be stable", i)
		}
	}
}

// TestNewNodesGetTheNextAddressesInOrder checks nodes added later are numbered in sorted
// order after the ones that were there, whatever order they are listed in.
func TestNewNodesGetTheNextAddressesInOrder(t *testing.T) {
	first := mustPlan(t, fresh(t))
	for _, order := range [][]string{{"zeta", "alpha"}, {"alpha", "zeta"}} {
		in := fresh(t)
		in.IPAM = first.IPAM
		for _, id := range order {
			in.Topology.Nodes = append(in.Topology.Nodes, topology.Node{ID: nodeID(id), Site: "builder", Role: 2, NAT: topology.NATBehind})
		}
		for _, id := range order {
			in.Topology.Links = append(in.Topology.Links, topology.Link{Kind: 1, A: hub, B: nodeID(id), Cost: 20})
		}
		r, err := plan.Plan(in)
		if err != nil {
			t.Fatalf("%v: %v", order, err)
		}
		got := map[string]string{}
		for _, c := range r.Changes {
			got[c.Subject] = c.Prefix.String()
		}
		if got["node:alpha"] != "10.42.240.4/32" || got["node:zeta"] != "10.42.240.5/32" {
			t.Errorf("listed %v: %v", order, got)
		}
	}
}
