package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/plan"
	"github.com/vectorihub/netctl/topology"
)

// FuzzPlan feeds arbitrary bytes to the decoder and plans whatever decodes, with the standard
// address plan. Neither may panic, and a plan that succeeds must render: any input is either
// refused with an error or planned in full.
func FuzzPlan(f *testing.F) {
	seed, err := os.ReadFile(filepath.Join("..", "..", "testdata", "e2e", "three-site.yaml"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Fuzz(func(t *testing.T, data []byte) {
		topo, err := topology.Decode("fuzz.yaml", data, 1<<16)
		if err != nil {
			return
		}
		topo.Addressing = topology.Addressing{Supernet: fresh(t).IPAM.Supernet}
		in := fresh(t)
		in.Topology, in.IPAM = topo, ipam.Snapshot{}
		in.IPAM = fresh(t).IPAM
		in.Validate.SkipSweep = true
		if res, err := plan.Plan(in); err == nil {
			if _, err := res.Render(); err != nil {
				t.Fatalf("a plan that succeeded does not render: %v", err)
			}
		}
	})
}
