package tests

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// TestDefaultConfig checks every default against the plan's table, and that the
// defaults are themselves valid.
func TestDefaultConfig(t *testing.T) {
	want := health.Config{
		FailThreshold: 3, RecoverThreshold: 5, StaleAfter: 15 * time.Second, HoldDown: 60 * time.Second,
		HandshakeMaxAge: 180 * time.Second, FlapLimit: 4, FlapWindow: 10 * time.Minute, PinFor: 30 * time.Minute,
		ProbeInterval: 5 * time.Second,
	}
	if got := health.DefaultConfig(); got != want {
		t.Errorf("DefaultConfig() = %+v, want %+v", got, want)
	}
	if err := health.DefaultConfig().Validate(); err != nil {
		t.Errorf("the defaults are not valid: %v", err)
	}
}

// TestConfigValidate breaks one setting at a time and checks each is refused with
// INVALID_CONFIG naming it, and that several broken settings are all reported at
// once.
func TestConfigValidate(t *testing.T) {
	cases := map[string]func(c *health.Config){
		"fail_threshold":    func(c *health.Config) { c.FailThreshold = 0 },
		"recover_threshold": func(c *health.Config) { c.RecoverThreshold = 0 },
		"flap_limit":        func(c *health.Config) { c.FlapLimit = -1 },
		"stale_after":       func(c *health.Config) { c.StaleAfter = 0 },
		"hold_down":         func(c *health.Config) { c.HoldDown = -time.Second },
		"handshake_max_age": func(c *health.Config) { c.HandshakeMaxAge = 0 },
		"flap_window":       func(c *health.Config) { c.FlapWindow = 0 },
		"pin_for":           func(c *health.Config) { c.PinFor = 0 },
		"probe_interval":    func(c *health.Config) { c.ProbeInterval = 0 },
	}
	for name, break_ := range cases {
		c := health.DefaultConfig()
		break_(&c)
		err := c.Validate()
		if !types.HasCode(err, types.ErrInvalidConfig) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error %v, want INVALID_CONFIG naming it", name, err)
		}
	}
	c := health.DefaultConfig()
	c.FailThreshold, c.HoldDown = 0, 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "fail_threshold") || !strings.Contains(err.Error(), "hold_down") {
		t.Errorf("two broken settings: %v, want both named", err)
	}
	if err := (health.Config{}).Validate(); err == nil {
		t.Error("the zero Config was accepted")
	}
}

// TestStaleAfterMustExceedProbeInterval checks a stale time that is not longer
// than the probe interval is refused, because a healthy link would go stale
// between two probes.
func TestStaleAfterMustExceedProbeInterval(t *testing.T) {
	c := health.DefaultConfig()
	c.StaleAfter = c.ProbeInterval
	if err := c.Validate(); !types.HasCode(err, types.ErrInvalidConfig) {
		t.Errorf("stale_after equal to probe_interval: %v, want INVALID_CONFIG", err)
	}
	c.StaleAfter = c.ProbeInterval + time.Nanosecond
	if err := c.Validate(); err != nil {
		t.Errorf("stale_after just over probe_interval: %v, want it accepted", err)
	}
}

// TestLinkHealthText checks the states write and read as their names, the zero
// value is Unknown, and a value outside the five is refused.
func TestLinkHealthText(t *testing.T) {
	var zero health.LinkHealth
	if zero != health.Unknown || zero.String() != "unknown" {
		t.Errorf("the zero value is %s, want unknown", zero)
	}
	for _, state := range []health.LinkHealth{health.Unknown, health.Healthy, health.Degraded, health.Down, health.Recovering} {
		text, err := state.MarshalText()
		if err != nil || string(text) != state.String() {
			t.Fatalf("MarshalText(%d) = %q, %v", int(state), text, err)
		}
		parsed, ok := health.ParseLinkHealth(string(text))
		var viaText health.LinkHealth
		if !ok || parsed != state || viaText.UnmarshalText(text) != nil || viaText != state {
			t.Errorf("%s did not round-trip", state)
		}
	}
	if _, err := health.LinkHealth(99).MarshalText(); err == nil {
		t.Error("an invalid state was written")
	}
	if got := health.LinkHealth(99).String(); got != "invalid" {
		t.Errorf("String of an invalid state = %q", got)
	}
	state := health.Down
	if err := state.UnmarshalText([]byte("Healthy")); err == nil || state != health.Down {
		t.Errorf("a wrong capitalisation was accepted or changed the receiver: %v, %s", err, state)
	}
	if _, ok := health.ParseLinkHealth("fine"); ok {
		t.Error("ParseLinkHealth accepted nonsense")
	}
}

// TestSignalKindText checks the kinds write and read as their names, and that the
// zero kind is unknown and cannot be written out.
func TestSignalKindText(t *testing.T) {
	var zero health.SignalKind
	if zero != health.SignalUnknown || zero.String() != "unknown" {
		t.Errorf("the zero value is %s, want unknown", zero)
	}
	if _, err := zero.MarshalText(); err == nil {
		t.Error("an unknown kind was written")
	}
	for _, kind := range []health.SignalKind{health.SignalProbe, health.SignalHandshake, health.SignalInterface, health.SignalAdmin} {
		text, err := kind.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		parsed, ok := health.ParseSignalKind(string(text))
		var viaText health.SignalKind
		if !ok || parsed != kind || viaText.UnmarshalText(text) != nil || viaText != kind {
			t.Errorf("%s did not round-trip", kind)
		}
	}
	kind := health.SignalProbe
	if err := kind.UnmarshalText([]byte("ping")); err == nil || kind != health.SignalProbe {
		t.Errorf("an unknown name was accepted or changed the receiver: %v, %s", err, kind)
	}
}

// TestSignalJSON checks a signal survives a trip through JSON, with its kind as
// a word and its time exact, and that a signal with no kind will not be written.
func TestSignalJSON(t *testing.T) {
	in := health.Signal{Link: direct, From: b1, Kind: health.SignalProbe, OK: true, At: at(sec(7)), Latency: 12 * time.Millisecond}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"kind":"probe"`) {
		t.Errorf("the kind is not written as a word: %s", data)
	}
	var out health.Signal
	if err := json.Unmarshal(data, &out); err != nil || out != in {
		t.Errorf("round trip gave %+v, %v; want %+v", out, err, in)
	}
	if _, err := json.Marshal(health.Signal{Link: direct, From: b1}); err == nil {
		t.Error("a signal with no kind was written")
	}
}
