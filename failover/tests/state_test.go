package tests

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/route"
	"github.com/vectorihub/netctl/types"
)

// roundTrip writes the state and reads it back, as a restart would.
func roundTrip(t *testing.T, s failover.State) failover.State {
	t.Helper()
	data, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := failover.ParseState(data)
	if err != nil {
		t.Fatalf("ParseState: %v\n%s", err, data)
	}
	return back
}

// stateJSON returns the state's JSON, failing the test on an error.
func stateJSON(t *testing.T, s failover.State) string {
	t.Helper()
	data, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRestartMidHoldDownKeepsTheHoldDown is P1-FAIL-013: the direct link goes Down
// at 45 s with a hold-down until 105 s. The state is written at 50 s, read back,
// and carried on. The hold-down still ends at 105 s, so a success at 100 s is
// ignored, one after 105 s makes the link Recovering, and the whole run is
// identical to one that never stopped.
func TestRestartMidHoldDownKeepsTheHoldDown(t *testing.T) {
	net := threeSite(t)
	failed := down(t, net, healthy(t, net).State)
	l, _ := failed.State.Link(direct)
	if want := at(105); !l.Dirs[builder].HoldDownUntil.Equal(want) || !l.Dirs[dgx].HoldDownUntil.Equal(want) {
		t.Fatalf("hold-down ends %v and %v, want %v", l.Dirs[builder].HoldDownUntil, l.Dirs[dgx].HoldDownUntil, want)
	}
	restarted := roundTrip(t, failed.State)
	if r, _ := restarted.Link(direct); !r.Dirs[builder].HoldDownUntil.Equal(at(105)) || !r.Dirs[dgx].HoldDownUntil.Equal(at(105)) {
		t.Fatalf("after the restart the hold-down ends %v and %v, want 105 s", r.Dirs[builder].HoldDownUntil, r.Dirs[dgx].HoldDownUntil)
	}
	steps := [][2]int{{100, 101}, {110, 111}, {115, 120}}
	straight, again := failed.State, restarted
	for _, step := range steps {
		signals := probes(direct, true, 1, step[0])
		a := evaluate(t, net, straight, signals, step[1])
		b := evaluate(t, net, again, signals, step[1])
		straight, again = a.State, roundTrip(t, b.State)
		if stateJSON(t, straight) != stateJSON(t, again) {
			t.Fatalf("after %v the restarted run differs from the one that never stopped", step)
		}
	}
	if got := linkState(t, straight, direct); got != health.Recovering {
		t.Errorf("the link is %s, want Recovering after the hold-down and three good rounds", got)
	}
}

// TestHoldDownStillHoldsAfterRestart checks the point of P1-FAIL-013 directly: a
// success inside the hold-down is ignored by the restarted state, and one a second
// after it counts.
func TestHoldDownStillHoldsAfterRestart(t *testing.T) {
	net := threeSite(t)
	failed := down(t, net, healthy(t, net).State)
	restarted := roundTrip(t, failed.State)
	early := evaluate(t, net, restarted, probes(direct, true, 1, 100), 104)
	if got := linkState(t, early.State, direct); got != health.Down {
		t.Errorf("a success at 100 s inside the hold-down: the link is %s, want Down", got)
	}
	late := evaluate(t, net, early.State, probes(direct, true, 1, 106), 107)
	if got := linkState(t, late.State, direct); got != health.Recovering {
		t.Errorf("a success at 106 s after the hold-down: the link is %s, want Recovering", got)
	}
}

// TestPinAndDrainSurviveARestart checks a flap pin, its end time and the flap
// record, and a drain, come back from a saved state.
func TestPinAndDrainSurviveARestart(t *testing.T) {
	net := threeSite(t)
	start := healthy(t, net)
	state := start.State
	l, _ := state.Link(direct)
	l.Pinned, l.PinnedUntil, l.Flaps, l.LastAdmin = true, at(1845), []time.Time{at(100), at(200)}, at(150)
	l, _ = state.Link(hubB)
	l.Drained = true
	back := roundTrip(t, state)
	d, _ := back.Link(direct)
	if !d.Pinned || !d.PinnedUntil.Equal(at(1845)) || len(d.Flaps) != 2 || !d.Flaps[1].Equal(at(200)) || !d.LastAdmin.Equal(at(150)) {
		t.Errorf("the pin did not survive: %+v", d)
	}
	if h, _ := back.Link(hubB); !h.Drained {
		t.Error("the drain did not survive")
	}
}

// TestStateJSONIsStable checks the JSON is byte-identical every time, does not
// change when read and written again, and does not depend on the time zone the
// times were in.
func TestStateJSONIsStable(t *testing.T) {
	net := threeSite(t)
	state := down(t, net, healthy(t, net).State).State
	first := stateJSON(t, state)
	if second := stateJSON(t, state); first != second {
		t.Error("two writes of one state differ")
	}
	if again := stateJSON(t, roundTrip(t, state)); first != again {
		t.Errorf("read and written again it differs:\n%s\n%s", first, again)
	}
	zone := time.FixedZone("far", 5*3600+1800)
	shifted := roundTrip(t, state)
	shifted.EvaluatedAt = shifted.EvaluatedAt.In(zone)
	for _, l := range shifted.Links {
		for _, d := range l.Dirs {
			d.HoldDownUntil, d.LastEvidence, d.LastSignal = d.HoldDownUntil.In(zone), d.LastEvidence.In(zone), d.LastSignal.In(zone)
		}
	}
	if got := stateJSON(t, shifted); got != first {
		t.Error("the JSON depends on the time zone of the times")
	}
	if !strings.HasSuffix(first, "}\n") || !strings.Contains(first, `"schema": "beaver.failover/v1"`) {
		t.Errorf("unexpected shape: %.200s", first)
	}
	if !reflect.DeepEqual(roundTrip(t, state).Tables.Nodes(), state.Tables.Nodes()) {
		t.Error("the tables' nodes changed over a restart")
	}
}

// TestZeroStateWritesAndReads checks the empty State writes as a valid empty
// state, with links as an empty list and not null, and reads back.
func TestZeroStateWritesAndReads(t *testing.T) {
	data := stateJSON(t, failover.State{})
	if !strings.Contains(data, `"links": []`) {
		t.Errorf("an empty state writes its links as %s", data)
	}
	back, err := failover.ParseState([]byte(data))
	if err != nil || len(back.Links) != 0 || back.Schema != failover.Schema {
		t.Errorf("read back %+v, %v", back, err)
	}
}

// TestParseStateRefusesWhatItCannotTrust checks each way a saved state can be
// wrong is refused with its code.
func TestParseStateRefusesWhatItCannotTrust(t *testing.T) {
	good := `{"schema":"beaver.failover/v1","links":[]}`
	oneDir := `{"schema":"beaver.failover/v1","links":[{"link":"a~b","required":true,"state":"unknown","directions":{"a":{"state":"unknown","consecutive_ok":0,"consecutive_fail":0}}}]}`
	twice := `{"schema":"beaver.failover/v1","links":[` + strings.Repeat(`{"link":"a~b","required":true,"state":"unknown","directions":{"a":{"state":"unknown","consecutive_ok":0,"consecutive_fail":0},"b":{"state":"unknown","consecutive_ok":0,"consecutive_fail":0}}},`, 2)
	twice = strings.TrimSuffix(twice, ",") + `]}`
	for name, tc := range map[string]struct {
		text string
		code types.Code
	}{
		"not json":         {`{`, types.ErrInvalidValue},
		"unknown field":    {`{"schema":"beaver.failover/v1","links":[],"extra":1}`, types.ErrInvalidValue},
		"two values":       {good + good, types.ErrInvalidValue},
		"stray bracket":    {good + `}`, types.ErrInvalidValue},
		"stray bracket 2":  {good + `]`, types.ErrInvalidValue},
		"old schema":       {`{"schema":"beaver.failover/v0","links":[]}`, types.ErrUnsupportedSchema},
		"no schema":        {`{"links":[]}`, types.ErrUnsupportedSchema},
		"bad state word":   {`{"schema":"beaver.failover/v1","links":[{"link":"a~b","state":"sideways","directions":{}}]}`, types.ErrInvalidValue},
		"one direction":    {oneDir, types.ErrInvalidTopology},
		"link twice":       {twice, types.ErrInvalidTopology},
		"table wrong node": {`{"schema":"beaver.failover/v1","links":[],"tables":{"a":{"node":"b","routes":[]}}}`, types.ErrInvalidTopology},
		"link without id":  {`{"schema":"beaver.failover/v1","links":[null]}`, types.ErrInvalidTopology},
	} {
		if _, err := failover.ParseState([]byte(tc.text)); !types.HasCode(err, tc.code) {
			t.Errorf("%s: %v, want %s", name, err, tc.code)
		}
	}
	if _, err := failover.ParseState([]byte(good)); err != nil {
		t.Errorf("the empty state: %v", err)
	}
	if _, err := (failover.State{Schema: "other"}).JSON(); !types.HasCode(err, types.ErrUnsupportedSchema) {
		t.Errorf("writing a state of another schema: %v, want UNSUPPORTED_SCHEMA", err)
	}
	if _, err := (failover.State{Links: []*health.Link{nil}}).JSON(); !types.HasCode(err, types.ErrInvalidTopology) {
		t.Errorf("writing a state with a nil link: %v, want INVALID_TOPOLOGY", err)
	}
}

// randomBatches returns signals for the three links of the three-site network,
// in time order and cut into batches, with the time to evaluate each batch at.
func randomBatches(rng *rand.Rand) (batches [][]health.Signal, nows []int) {
	now := 0
	for b := 0; b < 3+rng.IntN(6); b++ {
		var batch []health.Signal
		for i := 0; i < 1+rng.IntN(12); i++ {
			now += 1 + rng.IntN(25)
			link := []types.LinkID{direct, hubB, hubD}[rng.IntN(3)]
			s := health.Signal{Link: link, From: ends[link][rng.IntN(2)], OK: rng.IntN(4) > 0, At: at(now)}
			switch k := rng.IntN(12); {
			case k == 0:
				s.Kind = health.SignalAdmin
			case k < 3:
				s.Kind = health.SignalInterface
			case k < 5:
				s.Kind = health.SignalHandshake
			default:
				s.Kind = health.SignalProbe
			}
			batch = append(batch, s)
		}
		batches = append(batches, batch)
		now += rng.IntN(20)
		nows = append(nows, now)
	}
	return batches, nows
}

// TestStoppingAnywhereGivesTheSameResult is the property behind P1-FAIL-013: a run
// that writes its state and reads it back between every batch ends exactly where
// a run that never stopped does, with the same decisions on the way, however the
// signals fall: 300 random runs over the three links.
func TestStoppingAnywhereGivesTheSameResult(t *testing.T) {
	net := threeSite(t)
	net.Flows = nil
	for run := 0; run < 300; run++ {
		rng := rand.New(rand.NewPCG(53, uint64(run)))
		batches, nows := randomBatches(rng)
		straight, stopping := failover.State{}, failover.State{}
		for i, batch := range batches {
			a := evaluate(t, net, straight, batch, nows[i])
			b := evaluate(t, net, stopping, batch, nows[i])
			straight, stopping = a.State, roundTrip(t, b.State)
			if !reflect.DeepEqual(a.Decisions, b.Decisions) || !reflect.DeepEqual(a.Skipped, b.Skipped) {
				t.Fatalf("run %d batch %d: decisions differ:\n%+v\n%+v", run, i, a.Decisions, b.Decisions)
			}
			if stateJSON(t, straight) != stateJSON(t, stopping) {
				t.Fatalf("run %d batch %d: states differ after a restart", run, i)
			}
		}
	}
}

// TestBatchingDoesNotMatter checks that the same signals in time order, as one
// batch or cut into several evaluated at the time of each cut, leave the links in
// the same health. Tables follow from health, so they match too.
func TestBatchingDoesNotMatter(t *testing.T) {
	net := threeSite(t)
	net.Flows = nil
	for run := 0; run < 200; run++ {
		rng := rand.New(rand.NewPCG(54, uint64(run)))
		batches, nows := randomBatches(rng)
		var all []health.Signal
		for _, b := range batches {
			all = append(all, b...)
		}
		whole := evaluate(t, net, failover.State{}, all, nows[len(nows)-1])
		state := failover.State{}
		for i, b := range batches {
			state = evaluate(t, net, state, b, nows[i]).State
		}
		for _, id := range []types.LinkID{direct, hubB, hubD} {
			a, _ := whole.State.Link(id)
			b, _ := state.Link(id)
			if a.State != b.State || a.Pinned != b.Pinned || a.Drained != b.Drained {
				t.Fatalf("run %d: %s is %s (pinned %v) in one batch and %s (pinned %v) in several", run, id, a.State, a.Pinned, b.State, b.Pinned)
			}
		}
		if diff := route.Diff(whole.Tables, state.Tables); len(diff) != 0 {
			t.Fatalf("run %d: tables differ: %+v", run, diff)
		}
	}
}

// TestRestartedStateJSONHasNoSecrets is a guard on what is written: a state holds
// links, times and routes and nothing else.
func TestRestartedStateJSONHasNoSecrets(t *testing.T) {
	net := threeSite(t)
	data := stateJSON(t, healthy(t, net).State)
	for _, word := range []string{"key", "secret", "password", "token"} {
		if bytes.Contains([]byte(strings.ToLower(data)), []byte(word)) {
			t.Errorf("the state mentions %q", word)
		}
	}
}

// TestOrderWithinABatchDoesNotMatter checks the same signals handed over in any
// order give the same state, decisions and tables: Evaluate puts them in time
// order itself. 200 random batches, each shuffled.
func TestOrderWithinABatchDoesNotMatter(t *testing.T) {
	net := threeSite(t)
	net.Flows = nil
	for run := 0; run < 200; run++ {
		rng := rand.New(rand.NewPCG(56, uint64(run)))
		batches, nows := randomBatches(rng)
		batch, now := batches[0], nows[0]
		// Equal times on different links or ends are independent, so only
		// batches with distinct times are compared signal for signal.
		times := map[time.Time]bool{}
		for _, s := range batch {
			times[s.At] = true
		}
		if len(times) != len(batch) {
			continue
		}
		straight := evaluate(t, net, failover.State{}, batch, now)
		mixed := evaluate(t, net, failover.State{}, shuffled(rng, batch), now)
		if stateJSON(t, straight.State) != stateJSON(t, mixed.State) || !reflect.DeepEqual(straight.Decisions, mixed.Decisions) {
			t.Fatalf("run %d: a shuffled batch gave a different result", run)
		}
	}
}

// TestEverySavedTimeIsWrittenInUTC checks that each time a link can carry (pin end,
// last admin order, flaps, hold-down, evidence) is written the same whatever zone
// it was given in, so the bytes of a saved state do not depend on the time zone.
func TestEverySavedTimeIsWrittenInUTC(t *testing.T) {
	net := threeSite(t)
	state := healthy(t, net).State
	l, _ := state.Link(direct)
	l.Pinned, l.PinnedUntil, l.LastAdmin, l.Flaps = true, at(1845), at(150), []time.Time{at(100), at(200)}
	want := stateJSON(t, state)
	zone := time.FixedZone("far", 5*3600+1800)
	l.PinnedUntil, l.LastAdmin = l.PinnedUntil.In(zone), l.LastAdmin.In(zone)
	for i := range l.Flaps {
		l.Flaps[i] = l.Flaps[i].In(zone)
	}
	state.EvaluatedAt = state.EvaluatedAt.In(zone)
	if got := stateJSON(t, state); got != want {
		t.Errorf("the JSON depends on the time zone:\n%s\nwant\n%s", got, want)
	}
}
