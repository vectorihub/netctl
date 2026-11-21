package tests

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// TestOutdatedSignalCannotEraseANewerFailure sends failures at 35 s and 40 s, then
// a success stamped 30 s that arrived late, then a failure at 45 s. The late
// success must be skipped with a reason, so the third failure in a row takes the
// direction Down instead of finding it restored to Healthy.
func TestOutdatedSignalCannotEraseANewerFailure(t *testing.T) {
	e := baseline(t)
	probe := func(ok bool, s int) health.Signal {
		return health.Signal{Link: direct, From: b1, Kind: health.SignalProbe, OK: ok, At: at(sec(s))}
	}
	if _, skipped := e.Apply([]health.Signal{probe(false, 35), probe(false, 40)}, at(sec(41))); len(skipped) != 0 {
		t.Fatalf("skipped %+v", skipped)
	}
	_, skipped := e.Apply([]health.Signal{probe(true, 30)}, at(sec(42)))
	if len(skipped) != 1 || !strings.Contains(skipped[0].Why, "outdated") {
		t.Fatalf("the late success was not skipped as outdated: %+v", skipped)
	}
	if got := e.Links[direct].Dirs[b1]; got.FailRun != 2 || got.State != health.Degraded {
		t.Fatalf("the late success changed the direction: %+v", *got)
	}
	e.Apply([]health.Signal{probe(false, 45)}, at(sec(46)))
	if got := e.Links[direct].Dirs[b1].State; got != health.Down {
		t.Errorf("after the third failure: %s, want down", got)
	}
}

// TestOutdatedAppliesToInterfaceSignalsAndToTheSameBatch checks the newest-signal
// rule counts interface signals too, and holds within one batch handed in out of
// order only by time: signals at the same instant are not outdated.
func TestOutdatedAppliesToInterfaceSignalsAndToTheSameBatch(t *testing.T) {
	e := baseline(t)
	iface := health.Signal{Link: direct, From: b1, Kind: health.SignalInterface, OK: true, At: at(sec(50))}
	late := health.Signal{Link: direct, From: b1, Kind: health.SignalProbe, OK: false, At: at(sec(45))}
	if _, skipped := e.Apply([]health.Signal{iface}, at(sec(51))); len(skipped) != 0 {
		t.Fatalf("an interface-up signal was skipped: %+v", skipped)
	}
	if _, skipped := e.Apply([]health.Signal{late}, at(sec(52))); len(skipped) != 1 {
		t.Errorf("a probe older than an applied interface signal: skipped %+v, want it skipped", skipped)
	}
	same := health.Signal{Link: direct, From: b1, Kind: health.SignalProbe, OK: true, At: at(sec(50))}
	if _, skipped := e.Apply([]health.Signal{same}, at(sec(53))); len(skipped) != 0 {
		t.Errorf("a signal at the same instant as the newest was skipped: %+v", skipped)
	}
	if e.Links[direct].Dirs[b1].LastSignal != at(sec(50)) || e.Links[direct].Dirs[dgx].LastSignal != at(sec(25)) {
		t.Errorf("last signals %v and %v, want 50 s for builder-1 and 25 s for dgx-spark",
			e.Links[direct].Dirs[b1].LastSignal, e.Links[direct].Dirs[dgx].LastSignal)
	}
}

// TestUnsupportedSignalsDoNotMoveTheClock sends an admin release that has nothing
// to release and a signal of no known kind, both stamped 50 s, then a real
// failure at 45 s. The skipped signals leave the direction alone, so the failure is not
// mistaken for an outdated one and is applied.
func TestUnsupportedSignalsDoNotMoveTheClock(t *testing.T) {
	e := baseline(t)
	admin := health.Signal{Link: direct, From: b1, Kind: health.SignalAdmin, OK: true, At: at(sec(50))}
	odd := health.Signal{Link: direct, From: b1, Kind: health.SignalKind(99), At: at(sec(50))}
	before := *e.Links[direct].Dirs[b1]
	if _, skipped := e.Apply([]health.Signal{admin, odd}, at(sec(50))); len(skipped) != 2 {
		t.Fatalf("skipped %+v, want both unsupported signals", skipped)
	}
	if got := e.Links[direct].Dirs[b1].LastSignal; got != before.LastSignal {
		t.Fatalf("an unsupported signal moved LastSignal from %v to %v", before.LastSignal, got)
	}
	failure := health.Signal{Link: direct, From: b1, Kind: health.SignalProbe, OK: false, At: at(sec(45))}
	if _, skipped := e.Apply([]health.Signal{failure}, at(sec(51))); len(skipped) != 0 {
		t.Errorf("a real failure after unsupported signals was skipped: %+v", skipped)
	}
	if got := e.Links[direct].Dirs[b1].FailRun; got != 1 {
		t.Errorf("FailRun = %d, want 1: the failure was not applied", got)
	}
}

// TestStaleOutageFollowsTimeNotNames checks that when both directions of a link go
// quiet, the decision records the end that went stale first, not the end whose
// name sorts first: a last heard at 25 s goes stale at 40 s, b last heard at 20 s
// goes stale at 35 s, so the link went down at 35 s because of b.
func TestStaleOutageFollowsTimeNotNames(t *testing.T) {
	l, err := health.NewLink("a~b", "a", "b", true)
	if err != nil {
		t.Fatal(err)
	}
	e, err := health.NewEvaluator(cfg, l)
	if err != nil {
		t.Fatal(err)
	}
	var signals []health.Signal
	for i := 0; i < 5; i++ {
		signals = append(signals,
			health.Signal{Link: "a~b", From: "a", Kind: health.SignalProbe, OK: true, At: at(sec(5 + 5*i))},
			health.Signal{Link: "a~b", From: types.NodeID("b"), Kind: health.SignalProbe, OK: true, At: at(sec(5 * i))})
	}
	e.Apply(signals, at(sec(26)))
	if l.State != health.Healthy {
		t.Fatalf("setup: %s, want healthy", l.State)
	}
	decisions, _ := e.Apply(nil, at(sec(50)))
	if len(decisions) != 1 || !decisions[0].At.Equal(at(sec(35))) || !strings.Contains(decisions[0].Reason, "from b") {
		t.Errorf("decisions = %+v, want one at 35 s naming b", decisions)
	}
}

// TestEnumErrorsCarryTheInvalidValueCode checks the two enums of the package refuse
// a bad name or value with INVALID_VALUE, found with errors.Is, directly and
// through encoding/json.
func TestEnumErrorsCarryTheInvalidValueCode(t *testing.T) {
	want := &types.Error{Code: types.ErrInvalidValue}
	var kind health.SignalKind
	var state health.LinkHealth
	_, kindErr := health.SignalUnknown.MarshalText()
	_, stateErr := health.LinkHealth(99).MarshalText()
	_, jsonErr := json.Marshal(health.Signal{Link: "x"})
	for name, err := range map[string]error{
		"kind unmarshal":  kind.UnmarshalText([]byte("ping")),
		"state unmarshal": state.UnmarshalText([]byte("fine")),
		"kind json":       json.Unmarshal([]byte(`"ping"`), &kind),
		"state json":      json.Unmarshal([]byte(`"fine"`), &state),
		"kind marshal":    kindErr,
		"state marshal":   stateErr,
		"signal json":     jsonErr,
	} {
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want INVALID_VALUE", name, err)
		}
	}
}
