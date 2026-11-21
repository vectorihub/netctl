package e2e

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vectorihub/netctl/topology"
)

// DefaultTopology is the file Plan loads when the options name no topology.
const DefaultTopology = "three-site.yaml"

// Copy copies fixture files from the repository's testdata/ into the workspace. Names are
// relative to testdata/ ("topologies/three-site.yaml"); each lands in the workspace root
// under its base name, which is how the guide's commands refer to them.
//
// Example:
//
//	w.Copy("topologies/address-plan.yaml", "topologies/three-site.yaml")
func (w *W) Copy(names ...string) {
	w.T.Helper()
	root, err := ModuleRoot()
	if err != nil {
		w.Fatalf("copy: %v", err)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, "testdata", filepath.FromSlash(name)))
		if err != nil {
			w.Fatalf("copy %s: %v", name, err)
		}
		w.Write(filepath.Base(name), data)
	}
}

// Write puts a file in the workspace root.
func (w *W) Write(name string, data []byte) {
	w.T.Helper()
	full := filepath.Join(w.Dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		w.Fatalf("write %s: %v", name, err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		w.Fatalf("write %s: %v", name, err)
	}
}

// Read returns a file from the workspace.
func (w *W) Read(name string) []byte {
	w.T.Helper()
	data, err := os.ReadFile(filepath.Join(w.Dir, name))
	if err != nil {
		w.Fatalf("read %s: %v", name, err)
	}
	return data
}

// Setup copies the address plan and the three-site topology into the workspace, which is
// where most tests start.
//
// Example:
//
//	w := e2e.New(t, "P1-E2E-001"); w.Setup()
func (w *W) Setup() {
	w.T.Helper()
	w.Copy("topologies/address-plan.yaml", "topologies/three-site.yaml")
}

// Load decodes and checks a topology file of the workspace and resolves its address plan
// reference from the workspace as well, the way a command line does. It fails the test when
// the file is refused; use TryLoad to examine the refusal.
//
// Example:
//
//	topo := w.Load("three-site.yaml")
func (w *W) Load(name string) *topology.Topology {
	w.T.Helper()
	topo, err := w.TryLoad(name)
	if err != nil {
		w.Fatalf("load %s: %v", name, err)
	}
	return topo
}

// TryLoad is Load that returns the refusal instead of failing the test. The topology is nil
// when the file did not decode.
func (w *W) TryLoad(name string) (*topology.Topology, error) {
	data, err := os.ReadFile(filepath.Join(w.Dir, name))
	if err != nil {
		return nil, err
	}
	return LoadBytes(name, data, w.Dir, 0)
}

// LoadBytes decodes topology bytes with topology.Load and resolves an address plan
// reference against dir. maxBytes is the input limit; zero means the library default.
//
// Example:
//
//	topo, err := e2e.LoadBytes("lab.yaml", data, dir, 0)
func LoadBytes(name string, data []byte, dir string, maxBytes int) (*topology.Topology, error) {
	topo, _, err := topology.Load(name, data, maxBytes)
	if topo == nil {
		return nil, err
	}
	if topo.Addressing.Ref != "" {
		plan, perr := readAddressPlan(filepath.Join(dir, topo.Addressing.Ref))
		if perr != nil {
			return topo, perr
		}
		topo.Addressing = plan
	}
	return topo, err
}

// addressPlanFile is the shape of an address plan file, read here because netctl reads no
// files: resolving a reference is the caller's job.
type addressPlanFile struct {
	Supernet string `yaml:"supernet"`
	Pools    []struct {
		Name      string `yaml:"name"`
		Prefix    string `yaml:"prefix"`
		ChildBits int    `yaml:"child_bits"`
		Reserved  bool   `yaml:"reserved"`
		SkipFirst int    `yaml:"skip_first"`
	} `yaml:"pools"`
}

// readAddressPlan reads an address plan file into the form a topology carries.
func readAddressPlan(path string) (topology.Addressing, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return topology.Addressing{}, err
	}
	var file addressPlanFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return topology.Addressing{}, err
	}
	supernet, err := netip.ParsePrefix(file.Supernet)
	if err != nil {
		return topology.Addressing{}, err
	}
	plan := topology.Addressing{Supernet: supernet}
	for _, p := range file.Pools {
		prefix, err := netip.ParsePrefix(p.Prefix)
		if err != nil {
			return topology.Addressing{}, err
		}
		plan.Pools = append(plan.Pools, topology.AddressPool{Name: p.Name, Prefix: prefix, ChildBits: p.ChildBits, Reserved: p.Reserved, SkipFirst: p.SkipFirst})
	}
	return plan, nil
}

// Rewrite replaces text in a workspace file, failing the test when the text is not there.
// It is for the one-line fixture variants (a typo, a schema version) the guide describes.
//
// Example:
//
//	w.Rewrite("t.yaml", "role: hub", "rol: hub")
func (w *W) Rewrite(name, old, updated string) {
	w.T.Helper()
	text := string(w.Read(name))
	if !strings.Contains(text, old) {
		w.Fatalf("rewrite %s: %q not found", name, old)
	}
	w.Write(name, []byte(strings.Replace(text, old, updated, 1)))
}
