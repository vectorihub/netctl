package tests

import (
	"reflect"
	"testing"

	"github.com/vectorihub/netctl/internal/fixture"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/sim"
)

// scanning returns a Net with the same tables and questions as net but no index,
// so every lookup scans the tables.
func scanning(net sim.Net) sim.Net {
	return sim.Net{Tables: net.Tables, Local: net.Local, Adjacent: net.Adjacent, Up: net.Up}
}

// TestIndexedNetAgreesWithScanning runs a sample of flows of a 500-node network
// through a Net made by NewNet (indexed) and through one with no index, and
// checks every result is identical, trace for trace. The index is a speed-up and
// must change no answer.
func TestIndexedNetAgreesWithScanning(t *testing.T) {
	g, input, names := scaleNetwork(120, 50)
	result, err := route.Compute(g, input)
	if err != nil {
		t.Fatal(err)
	}
	indexed := sim.NewNet(g, result.Tables, input.Owned, input.Local)
	plain := scanning(indexed)
	flows := allPairsOverlay(names)
	for i := 0; i < len(flows); i += 7 {
		if got, want := indexed.Run(flows[i], false), plain.Run(flows[i], false); !reflect.DeepEqual(got, want) {
			t.Fatalf("flow %s: indexed %+v, scanning %+v", flows[i].Label, got, want)
		}
	}
}

// TestNewNetReadsTablesOnce documents the snapshot: a table changed after NewNet
// is not seen by that Net, while a Net with no index sees the change at once.
func TestNewNetReadsTablesOnce(t *testing.T) {
	p := computePlan(t, fixture.ThreeSite(false))
	indexed := p.net()
	plain := scanning(indexed)
	p.tables["aws-hub"].Routes = nil // the hub forgets everything, after the Net was built
	if got := verdictOf(indexed, "builder-1", "dgx-spark"); got.Verdict != sim.VerdictOK {
		t.Errorf("the Net made by NewNet saw a later change: %s", got.Verdict)
	}
	if got := verdictOf(plain, "builder-1", "dgx-spark"); got.Verdict != sim.VerdictBlackHole {
		t.Errorf("the Net with no index did not see the change: %s", got.Verdict)
	}
}
