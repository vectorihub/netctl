package tests

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// randomSignal returns one random signal on the direct link at time clock: a
// probe, handshake or interface signal, good or bad, from either end. Good
// probes are the most likely, so links do become healthy now and then.
func randomSignal(rng *rand.Rand, clock time.Time) health.Signal {
	kind := []health.SignalKind{health.SignalProbe, health.SignalProbe, health.SignalProbe, health.SignalHandshake, health.SignalInterface}[rng.IntN(5)]
	end := b1
	if rng.IntN(2) == 0 {
		end = dgx
	}
	return health.Signal{Link: direct, From: end, Kind: kind, OK: rng.IntN(5) != 0, At: clock}
}

// P1-FAIL-012
//
// TestHealthyNeverWithoutEnoughGoodSignals runs 10,000 random signal streams,
// feeding each signal and then letting random time pass, and checks after every
// step that a direction only becomes Healthy by one of the two lawful routes:
// from Degraded, which was already proven, or from Unknown or Recovering after at
// least RecoverThreshold successful probes or handshakes in a row from that end
// with no failure between. It never reaches Healthy straight from Down, and the
// link as a whole is Healthy only when both directions are.
func TestHealthyNeverWithoutEnoughGoodSignals(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 5))
	for stream := 0; stream < 10000; stream++ {
		e := newDirect(t)
		l := e.Links[direct]
		run := map[types.NodeID]int{} // consecutive good probes or handshakes, per end
		clock := t0
		for step := 0; step < 40; step++ {
			clock = clock.Add(time.Duration(1+rng.IntN(12)) * time.Second)
			s := randomSignal(rng, clock)
			before := map[types.NodeID]health.LinkHealth{b1: l.Dirs[b1].State, dgx: l.Dirs[dgx].State}
			switch {
			case s.Kind == health.SignalInterface && s.OK:
			case s.Kind == health.SignalInterface:
				run[s.From] = 0
			case s.OK:
				run[s.From]++
			default:
				run[s.From] = 0
			}
			if _, skipped := e.Apply([]health.Signal{s}, clock); len(skipped) != 0 {
				t.Fatalf("stream %d: skipped %+v", stream, skipped)
			}
			for _, end := range []types.NodeID{b1, dgx} {
				after := l.Dirs[end].State
				if after != health.Healthy || before[end] == health.Healthy || before[end] == health.Degraded {
					continue
				}
				if before[end] == health.Down {
					t.Fatalf("stream %d step %d: %s went straight from down to healthy", stream, step, end)
				}
				if s.From != end || run[end] < cfg.RecoverThreshold {
					t.Fatalf("stream %d step %d: %s became healthy from %s after only %d good signals in a row", stream, step, end, before[end], run[end])
				}
			}
			if (l.State == health.Healthy) != (l.Dirs[b1].State == health.Healthy && l.Dirs[dgx].State == health.Healthy) {
				t.Fatalf("stream %d step %d: link %s but directions %s and %s", stream, step, l.State, l.Dirs[b1].State, l.Dirs[dgx].State)
			}
		}
	}
}

// TestInterfaceSignalsAloneNeverHeal runs 2,000 random streams made only of
// interface signals, up and down, and checks no direction is ever Healthy or
// Recovering: interface state can lower health and never raise it.
func TestInterfaceSignalsAloneNeverHeal(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 9))
	for stream := 0; stream < 2000; stream++ {
		e := newDirect(t)
		clock := t0
		for step := 0; step < 60; step++ {
			clock = clock.Add(time.Duration(1+rng.IntN(90)) * time.Second)
			end := []types.NodeID{b1, dgx}[rng.IntN(2)]
			e.Apply([]health.Signal{{Link: direct, From: end, Kind: health.SignalInterface, OK: rng.IntN(2) == 0, At: clock}}, clock)
			for _, d := range e.Links[direct].Dirs {
				if d.State == health.Healthy || d.State == health.Recovering || d.State == health.Degraded {
					t.Fatalf("stream %d step %d: a direction is %s from interface signals alone", stream, step, d.State)
				}
			}
		}
	}
}

// TestBatchOrderDoesNotMatter checks that the same signals handed in as one batch
// in a shuffled order, or one at a time in time order, give the same final state:
// Apply sorts by time and does not depend on how the caller sliced the stream.
func TestBatchOrderDoesNotMatter(t *testing.T) {
	rng := rand.New(rand.NewPCG(8, 8))
	for stream := 0; stream < 500; stream++ {
		var signals []health.Signal
		clock := t0
		for i := 0; i < 30; i++ {
			clock = clock.Add(time.Duration(1+rng.IntN(8)) * time.Second)
			signals = append(signals, randomSignal(rng, clock))
		}
		end := clock.Add(time.Second)
		oneByOne := newDirect(t)
		for _, s := range signals {
			oneByOne.Apply([]health.Signal{s}, s.At)
		}
		oneByOne.Apply(nil, end)
		shuffled := append([]health.Signal(nil), signals...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		batch := newDirect(t)
		batch.Apply(shuffled, end)
		a, b := oneByOne.Links[direct], batch.Links[direct]
		if a.State != b.State || *a.Dirs[b1] != *b.Dirs[b1] || *a.Dirs[dgx] != *b.Dirs[dgx] {
			t.Fatalf("stream %d: one at a time %+v / %+v, as a batch %+v / %+v", stream, *a.Dirs[b1], *a.Dirs[dgx], *b.Dirs[b1], *b.Dirs[dgx])
		}
	}
}
