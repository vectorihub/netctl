package tests

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/topology"
	"github.com/vectorihub/netctl/types"
)

// P1-E2E-066 (a): a valid topology followed by 50 MB of comments is refused at
// once, without being parsed.
func TestDecodeRefusesHugeInput(t *testing.T) {
	huge := append(fixture(t, "three-site.yaml"), bytes.Repeat([]byte("# padding padding padding padding padding padding\n"), 1_100_000)...)
	if len(huge) < 50<<20 {
		t.Fatalf("test input is only %d bytes", len(huge))
	}
	start := time.Now()
	_, err := topology.Decode("huge.yaml", huge, 0)
	if !types.HasCode(err, types.ErrInvalidTopology) || !strings.Contains(err.Error(), "input exceeds 5 MiB limit") {
		t.Fatalf("got %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %s, want under 2s", took)
	}
}

// TestDecodeSizeLimitIsExact checks that input of exactly the limit is accepted and one byte more
// is refused.
func TestDecodeSizeLimitIsExact(t *testing.T) {
	data := fixture(t, "three-site.yaml")
	if _, err := topology.Decode("t.yaml", data, len(data)); err != nil {
		t.Errorf("input exactly at the limit: %v", err)
	}
	_, err := topology.Decode("t.yaml", data, len(data)-1)
	if !types.HasCode(err, types.ErrInvalidTopology) || !strings.Contains(err.Error(), fmt.Sprintf("exceeds %d byte limit", len(data)-1)) {
		t.Errorf("one byte over: %v", err)
	}
	// A limit of zero or less means the default, not "nothing fits".
	for _, limit := range []int{0, -1} {
		if _, err := topology.Decode("t.yaml", data, limit); err != nil {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
	// A file just under the default limit still decodes quickly.
	padded := append(append([]byte{}, data...), bytes.Repeat([]byte("# x\n"), (topology.DefaultMaxBytes-len(data))/4-1)...)
	start := time.Now()
	if _, err := topology.Decode("t.yaml", padded, 0); err != nil {
		t.Errorf("near-limit file: %v", err)
	}
	// Not a speed requirement (only refusing hostile input must be fast): this
	// catches a quadratic blowup, and leaves room for the race detector.
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("near-limit file took %s", took)
	}
}

// laughs builds the "billion laughs" file: 9 levels of aliases, each level
// referencing the one before it 10 times.
func laughs(levels, fanout int) string {
	var b strings.Builder
	b.WriteString("schema: beaver.topology/v1alpha1\nlol: &a0 [\"lol\"]\n")
	for level := 1; level <= levels; level++ {
		refs := make([]string, fanout)
		for i := range refs {
			refs[i] = fmt.Sprintf("*a%d", level-1)
		}
		fmt.Fprintf(&b, "l%d: &a%d [%s]\n", level, level, strings.Join(refs, ", "))
	}
	return b.String()
}

// P1-E2E-066 (b)
//
// TestDecodeRefusesBillionLaughs checks that a tiny file whose aliases expand enormously is
// refused quickly.
func TestDecodeRefusesBillionLaughs(t *testing.T) {
	file := laughs(9, 10)
	if len(file) > 2000 {
		t.Fatalf("the hostile file should be tiny, it is %d bytes", len(file))
	}
	start := time.Now()
	_, err := topology.Decode("laughs.yaml", []byte(file), 0)
	if !types.HasCode(err, types.ErrInvalidTopology) || !strings.Contains(err.Error(), "excessive aliasing") {
		t.Fatalf("got %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %s, want under 2s", took)
	}
}

// TestDecodeAllowsModestAliasing checks that ordinary alias use, such as a shared label set, still
// works.
func TestDecodeAllowsModestAliasing(t *testing.T) {
	// The same label set shared by two nodes is the reason aliases are allowed.
	got, err := topology.Decode("t.yaml", []byte(`
schema: beaver.topology/v1alpha1
nodes:
  - {id: a, site: s, role: hub, labels: &common {env: prod, tier: edge}}
  - {id: b, site: s, role: spoke, labels: *common}
`), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Nodes[1].Labels["env"] != "prod" || got.Nodes[0].Labels["tier"] != "edge" {
		t.Errorf("labels = %v / %v", got.Nodes[0].Labels, got.Nodes[1].Labels)
	}
	// A smaller laugh stays under the bound and is then judged on its content.
	_, err = topology.Decode("t.yaml", []byte(laughs(3, 10)), 0)
	if err == nil || strings.Contains(err.Error(), "excessive aliasing") {
		t.Errorf("a small alias tree should fail on its fields, not on aliasing: %v", err)
	}
}

// TestDecodeRefusesSelfReferencingAlias checks that an alias that contains itself is refused with
// its line.
func TestDecodeRefusesSelfReferencingAlias(t *testing.T) {
	_, err := topology.Decode("t.yaml", []byte("schema: x\nloop: &a [*a]\n"), 0)
	if !types.HasCode(err, types.ErrInvalidTopology) || !strings.Contains(err.Error(), "t.yaml:2: an alias refers to a node that contains it") {
		t.Errorf("got %v", err)
	}
	// A YAML syntax error names its line once, in the subject.
	_, err = topology.Decode("f.yaml", []byte("schema: x\nnodes: [a, b\n"), 0)
	if err == nil || !strings.HasPrefix(err.Error(), "INVALID_TOPOLOGY: f.yaml:1: did not find") {
		t.Errorf("got %v", err)
	}
}

// TestDecodeDeeplyNestedInput checks that very deeply nested input is refused or handled without
// crashing or taking long.
func TestDecodeDeeplyNestedInput(t *testing.T) {
	for _, depth := range []int{100, 5000, 100000} {
		text := "schema: x\nnodes: " + strings.Repeat("[", depth) + strings.Repeat("]", depth) + "\n"
		start := time.Now()
		if _, err := topology.Decode("t.yaml", []byte(text), 0); err == nil {
			t.Errorf("depth %d: accepted", depth)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("depth %d took %s", depth, took)
		}
	}
}

// A large honest topology decodes fast.
func TestDecodeLargeTopology(t *testing.T) {
	var b strings.Builder
	b.WriteString("schema: beaver.topology/v1alpha1\nsites:\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "  - id: site-%02d\n    provider: owned\n", i)
	}
	b.WriteString("nodes:\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "  - id: node-%04d\n    site: site-%02d\n    role: spoke\n    nat: behind\n    advertises: [site]\n    labels: {rack: \"%d\"}\n", i, i%50, i%20)
	}
	b.WriteString("links:\n")
	for i := 1; i < 2000; i++ {
		fmt.Fprintf(&b, "  - {kind: hub-spoke, a: node-0000, b: node-%04d}\n", i)
	}
	start := time.Now()
	got, err := topology.Decode("big.yaml", []byte(b.String()), 0)
	if err != nil || len(got.Nodes) != 2000 || len(got.Links) != 1999 {
		t.Fatalf("%v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %s", took)
	}
}

// Decode must never panic, whatever the bytes, and stay fast and bounded.
func FuzzDecode(f *testing.F) {
	three := fixtureBytes("three-site.yaml")
	f.Add(three)
	f.Add(fixtureBytes("three-site-typo.yaml"))
	f.Add([]byte(laughs(9, 10)))
	f.Add([]byte("schema: x\nloop: &a [*a]\n"))
	f.Add([]byte("---\n---\n"))
	f.Add([]byte("nodes: [[[[[[[[[[\n"))
	f.Add([]byte("\x00\xff\xfe"))
	f.Add([]byte("intents:\n  - allow: {from: '*', to: 'label:a=b', ports: [1, 65535]}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := topology.Decode("fuzz.yaml", data, 1<<20)
		if (got == nil) == (err == nil) {
			t.Fatalf("want exactly one of a topology and an error, got %v and %v", got, err)
		}
		if err != nil {
			if !types.HasCode(err, types.ErrInvalidTopology) {
				t.Fatalf("error without INVALID_TOPOLOGY: %v", err)
			}
			return
		}
		// Whatever decodes must decode again to the same thing.
		again, err := topology.Decode("fuzz.yaml", data, 1<<20)
		if err != nil || fmt.Sprintf("%+v", again) != fmt.Sprintf("%+v", got) {
			t.Fatalf("second decode differs: %v", err)
		}
	})
}
