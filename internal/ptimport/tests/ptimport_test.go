package tests

import (
	"os"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/e2e"
	"github.com/vectorihub/netctl/internal/ptimport"
)

// hub is a transcription of the hub router of lab A2 as the plan documents it: static routes
// to both spoke sites through the hub's transit addresses, a connected route to its own site.
const hub = `
device: R-HUB
maps_to: aws-hub
show_ip_route:
  - { code: C, prefix: 10.42.0.0/20, via: connected }
  - { code: S, prefix: 10.42.16.0/20, via: 10.42.252.2 }
  - { code: S, prefix: 10.42.32.0/20, via: 10.42.252.6 }
traceroutes:
  - { from: PC-BUILDER, to: 10.42.32.10, hops: [10.42.252.1, 10.42.252.6, 10.42.32.10] }
`

// builder transcribes the Builder router of lab A2: its default-like routes go to the hub.
const builder = `
device: R-BUILDER
maps_to: builder-1
show_ip_route:
  - { code: C, prefix: 10.42.16.0/20, via: connected }
  - { code: S, prefix: 10.42.0.0/20, via: 10.42.252.1 }
  - { code: S, prefix: 10.42.32.0/20, via: 10.42.252.1 }
`

// dgx transcribes the DGX Spark router of lab A2.
const dgx = `
device: R-DGX
maps_to: dgx-spark
show_ip_route:
  - { code: C, prefix: 10.42.32.0/20, via: connected }
  - { code: S, prefix: 10.42.0.0/20, via: 10.42.252.5 }
  - { code: S, prefix: 10.42.16.0/20, via: 10.42.252.5 }
`

// mapping names the addresses of the hub links, which the plan allocates no transit /30 for.
const mapping = `
hosts:
  PC-BUILDER: builder-1
hops:
  10.42.252.1: aws-hub
  10.42.252.5: aws-hub
  10.42.252.2: builder-1
  10.42.252.6: dgx-spark
`

// files returns the transcription as the files of an observation directory.
func files(extra ...string) map[string][]byte {
	m := map[string][]byte{"R-HUB.yaml": []byte(hub), "R-BUILDER.yaml": []byte(builder), "R-DGX.yaml": []byte(dgx), "mapping.yaml": []byte(mapping)}
	for i, e := range extra {
		m["extra"+string(rune('a'+i))+".yaml"] = []byte(e)
	}
	return m
}

// plannedLab plans lab A2 and returns what a comparison needs.
func plannedLab(t *testing.T) (*e2e.W, *e2e.Out) {
	t.Helper()
	w := e2e.New(t, "P1-PT-001")
	w.Copy("e2e/address-plan.yaml", "e2e/lab-a2.yaml")
	out := w.MustPlan(e2e.Options{Topology: w.Load("lab-a2.yaml")})
	return w, out
}

// TestAMatchingTranscriptionHasNoMismatch compares a transcription that agrees with the plan
// and expects neither a route nor a path to differ.
func TestAMatchingTranscriptionHasNoMismatch(t *testing.T) {
	w, out := plannedLab(t)
	obs, err := ptimport.Parse(files())
	if err != nil {
		t.Fatal(err)
	}
	plan := out.ForPacketTracer(w.Load("lab-a2.yaml"), obs.Mapping)
	if problems := ptimport.CompareRoutes(obs, "", plan); len(problems) != 0 {
		t.Errorf("routes: %v", problems)
	}
	if problems := ptimport.ComparePaths(obs, "", plan); len(problems) != 0 {
		t.Errorf("paths: %v", problems)
	}
}

// TestADifferentNextHopIsReported changes one transcribed next hop and expects the
// comparison to name the device, the prefix and the node the plan uses instead.
func TestADifferentNextHopIsReported(t *testing.T) {
	w, out := plannedLab(t)
	bad := strings.Replace(builder, "10.42.0.0/20, via: 10.42.252.1", "10.42.0.0/20, via: 10.42.252.6", 1)
	obs, err := ptimport.Parse(map[string][]byte{"R-BUILDER.yaml": []byte(bad), "mapping.yaml": []byte(mapping)})
	if err != nil {
		t.Fatal(err)
	}
	problems := ptimport.CompareRoutes(obs, "", out.ForPacketTracer(w.Load("lab-a2.yaml"), obs.Mapping))
	if len(problems) == 0 || !strings.Contains(problems[0], "R-BUILDER") || !strings.Contains(problems[0], "10.42.0.0/20") {
		t.Errorf("the wrong next hop was not named: %v", problems)
	}
}

// TestAMissingRouteInTheLabIsReported leaves a route out of the transcription and expects
// the plan's extra route to be reported, since the lab shows none.
func TestAMissingRouteInTheLabIsReported(t *testing.T) {
	w, out := plannedLab(t)
	short := strings.Replace(builder, "  - { code: S, prefix: 10.42.32.0/20, via: 10.42.252.1 }\n", "", 1)
	obs, err := ptimport.Parse(map[string][]byte{"R-BUILDER.yaml": []byte(short), "mapping.yaml": []byte(mapping)})
	if err != nil {
		t.Fatal(err)
	}
	problems := ptimport.CompareRoutes(obs, "", out.ForPacketTracer(w.Load("lab-a2.yaml"), obs.Mapping))
	if len(problems) != 1 || !strings.Contains(problems[0], "the lab shows no such route") {
		t.Errorf("problems: %v", problems)
	}
}

// TestADifferentTraceroutePathIsReported transcribes a trace that goes straight to the DGX
// Spark, which the hub-and-spoke plan never does, and expects both paths in the message.
func TestADifferentTraceroutePathIsReported(t *testing.T) {
	w, out := plannedLab(t)
	direct := strings.Replace(hub, "hops: [10.42.252.1, 10.42.252.6, 10.42.32.10]", "hops: [10.42.252.6, 10.42.32.10]", 1)
	obs, err := ptimport.Parse(map[string][]byte{"R-HUB.yaml": []byte(direct), "mapping.yaml": []byte(mapping)})
	if err != nil {
		t.Fatal(err)
	}
	problems := ptimport.ComparePaths(obs, "", out.ForPacketTracer(w.Load("lab-a2.yaml"), obs.Mapping))
	if len(problems) != 1 || !strings.Contains(problems[0], "lab path [builder-1 dgx-spark]") || !strings.Contains(problems[0], "plan path [builder-1 aws-hub dgx-spark]") {
		t.Errorf("problems: %v", problems)
	}
}

// TestUnknownFieldsAndMissingNamesAreRefused checks that a typo in a transcription is an
// error and not an empty table.
func TestUnknownFieldsAndMissingNamesAreRefused(t *testing.T) {
	if _, err := ptimport.Parse(map[string][]byte{"X.yaml": []byte("device: R\nmaps_to: a\nshow_ip_rout: []\n")}); err == nil {
		t.Error("an unknown field was accepted")
	}
	if _, err := ptimport.Parse(map[string][]byte{"X.yaml": []byte("device: R\n")}); err == nil {
		t.Error("a device without maps_to was accepted")
	}
}

// TestLoadReportsAMissingDirectoryAsNotExist checks the signal a lab test uses to skip a lab
// whose results are not transcribed yet.
func TestLoadReportsAMissingDirectoryAsNotExist(t *testing.T) {
	_, err := ptimport.Load(t.TempDir() + "/nothing")
	if !os.IsNotExist(err) {
		t.Errorf("got %v, want a not-exist error", err)
	}
}

// TestTheResolverMapsOverlayAndTransitAddresses checks the plan-based resolution: an overlay
// address belongs to its node, and the two hosts of a direct link's /30 to its two ends in
// order.
func TestTheResolverMapsOverlayAndTransitAddresses(t *testing.T) {
	w := e2e.New(t, "P1-PT-002")
	_, base := e2e.Baseline(w)
	r := ptimport.NewResolver(base.Result.IPAM, ptimport.Mapping{})
	for address, want := range map[string]string{"10.42.240.2": "builder-1", "10.42.252.1": "builder-1", "10.42.252.2": "dgx-spark", "10.42.240.1": "aws-hub"} {
		if got, ok := r.Node(address); !ok || got != want {
			t.Errorf("%s resolves to %q (%v), want %s", address, got, ok, want)
		}
	}
	if _, ok := r.Node("192.168.1.1"); ok {
		t.Error("an address outside the plan resolved")
	}
}
