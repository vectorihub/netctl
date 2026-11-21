package health

import (
	"fmt"
	"time"

	"github.com/vectorihub/netctl/types"
)

// SignalKind says what a Signal is evidence of.
//
// The zero value is SignalUnknown, so a signal whose kind was never set is
// refused instead of being counted as evidence.
type SignalKind int

const (
	// SignalUnknown is the zero value: no kind was set.
	SignalUnknown SignalKind = iota
	// SignalProbe is the result of a test packet sent across the link to the
	// other end and answered, or not. It is evidence that traffic really flows.
	SignalProbe
	// SignalHandshake is the state of the WireGuard handshake with the other end:
	// a recent one is evidence that the two ends can talk, an old one is not.
	SignalHandshake
	// SignalInterface is what the operating system says about the network
	// interface. It can show a link is down, because an interface that is gone
	// certainly carries nothing, but it can never show a link is healthy, because
	// an interface can be up on a path that is broken. Only probes and handshakes
	// make a link Healthy.
	SignalInterface
	// SignalAdmin is an operator's instruction about the link, such as draining it.
	SignalAdmin
)

// signalNames holds the text of each kind.
var signalNames = map[SignalKind]string{
	SignalProbe: "probe", SignalHandshake: "handshake", SignalInterface: "interface", SignalAdmin: "admin",
}

// String returns "probe", "handshake", "interface", "admin" or "unknown".
func (kind SignalKind) String() string {
	if name, ok := signalNames[kind]; ok {
		return name
	}
	return "unknown"
}

// ParseSignalKind converts the text String returns back into a SignalKind. Any
// other text, including different capitalisation, returns SignalUnknown and
// false.
//
// Example: ParseSignalKind("probe") returns SignalProbe, true.
func ParseSignalKind(text string) (SignalKind, bool) {
	for kind, name := range signalNames {
		if name == text {
			return kind, true
		}
	}
	return SignalUnknown, false
}

// MarshalText writes the kind as its name. An unknown kind returns an error, so a
// signal with no kind cannot be written out.
func (kind SignalKind) MarshalText() ([]byte, error) {
	if _, ok := signalNames[kind]; !ok {
		return nil, fmt.Errorf("health: cannot write unknown signal kind %d", int(kind))
	}
	return []byte(kind.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
func (kind *SignalKind) UnmarshalText(text []byte) error {
	parsed, ok := ParseSignalKind(string(text))
	if !ok {
		return fmt.Errorf("health: unknown signal kind %q: want probe, handshake, interface or admin", text)
	}
	*kind = parsed
	return nil
}

// Signal is one piece of evidence about one direction of one link: what one end
// of the link saw of the other.
//
// A signal can be made up by a test or come from a real probe or WireGuard
// handshake; the evaluator cannot tell the difference, and does not need to.
type Signal struct {
	// Link is the link the evidence is about.
	Link types.LinkID `json:"link"`
	// From is the end that made the observation. A probe from builder-1 on the
	// link builder-1~dgx-spark is evidence about the direction builder-1 to
	// dgx-spark only.
	From types.NodeID `json:"from"`
	// Kind says what sort of evidence it is.
	Kind SignalKind `json:"kind"`
	// OK says the evidence is good: the probe was answered, the handshake is
	// recent, the interface is up. For an admin signal, OK means "undrain" and
	// not OK means "drain".
	OK bool `json:"ok"`
	// At is when the observation was made. The evaluator never reads the clock;
	// it trusts this time and the time it is given.
	At time.Time `json:"at"`
	// Latency is how long a probe took to answer. It is recorded for reports and
	// plays no part in the state machine.
	Latency time.Duration `json:"latency,omitzero"`
}

// LinkHealth says how far a link, or one direction of it, can be trusted.
//
// The zero value is Unknown, which is the safe default: nothing has been proven.
type LinkHealth int

const (
	// Unknown means no evidence yet, or not enough. A required link in this state
	// is not used.
	Unknown LinkHealth = iota
	// Healthy means enough consecutive good evidence has come in and nothing bad
	// since.
	Healthy
	// Degraded means one failure has come in after Healthy. Routes do not change
	// yet, so a single lost probe does not make traffic move.
	Degraded
	// Down means the link failed, or an operator drained it, or it went silent.
	// It stays down for at least the hold-down time.
	Down
	// Recovering means a success has come in after the hold-down. The link has
	// not yet shown it is stable, so it is still not used.
	Recovering
)

// healthNames holds the text of each state.
var healthNames = map[LinkHealth]string{
	Unknown: "unknown", Healthy: "healthy", Degraded: "degraded", Down: "down", Recovering: "recovering",
}

// String returns "unknown", "healthy", "degraded", "down" or "recovering".
// A value outside the five returns "invalid".
func (state LinkHealth) String() string {
	if name, ok := healthNames[state]; ok {
		return name
	}
	return "invalid"
}

// ParseLinkHealth converts the text String returns back into a LinkHealth. Any
// other text returns Unknown and false.
//
// Example: ParseLinkHealth("down") returns Down, true.
func ParseLinkHealth(text string) (LinkHealth, bool) {
	for state, name := range healthNames {
		if name == text {
			return state, true
		}
	}
	return Unknown, false
}

// MarshalText writes the state as its name. A value outside the five states
// returns an error.
func (state LinkHealth) MarshalText() ([]byte, error) {
	if _, ok := healthNames[state]; !ok {
		return nil, fmt.Errorf("health: cannot write invalid link health %d", int(state))
	}
	return []byte(state.String()), nil
}

// UnmarshalText reads a name written by MarshalText. An unknown name is an error
// and leaves the receiver unchanged.
func (state *LinkHealth) UnmarshalText(text []byte) error {
	parsed, ok := ParseLinkHealth(string(text))
	if !ok {
		return fmt.Errorf("health: unknown link health %q: want unknown, healthy, degraded, down or recovering", text)
	}
	*state = parsed
	return nil
}

// Config holds the thresholds and times of the state machine. Use DefaultConfig
// for the standard values and change only what you mean to.
type Config struct {
	// FailThreshold is how many failures in a row take a Healthy direction to
	// Down. The first of them already makes it Degraded.
	FailThreshold int `json:"fail_threshold"`
	// RecoverThreshold is how many good signals in a row prove a direction: from
	// Unknown, or from Recovering, to Healthy.
	RecoverThreshold int `json:"recover_threshold"`
	// StaleAfter is how long a trusted direction may go without any probe or
	// handshake before the silence counts as a failure. It must be longer than
	// ProbeInterval, or a healthy link would go stale between two probes.
	StaleAfter time.Duration `json:"stale_after"`
	// HoldDown is the least time a direction stays Down before it may start to
	// recover. It stops a link that flaps from bouncing traffic back and forth.
	HoldDown time.Duration `json:"hold_down"`
	// HandshakeMaxAge is the age past which a WireGuard handshake is a failure
	// signal. Whatever reads the handshake time applies it and sends a Signal
	// with OK false; the state machine only sees the result.
	HandshakeMaxAge time.Duration `json:"handshake_max_age"`
	// FlapLimit is how many times a link may go Down within FlapWindow before
	// more than that pins it Down.
	FlapLimit int `json:"flap_limit"`
	// FlapWindow is the period over which FlapLimit is counted.
	FlapWindow time.Duration `json:"flap_window"`
	// PinFor is how long a pinned link stays Down unless an operator clears it.
	PinFor time.Duration `json:"pin_for"`
	// ProbeInterval is how often a prober should send a probe. The state machine
	// does not send probes; it needs the figure to check that StaleAfter leaves
	// room for them.
	ProbeInterval time.Duration `json:"probe_interval"`
}

// DefaultConfig returns the standard settings: three failures to go Down, five
// good signals to recover, silence for 15 seconds is a failure, a 60 second
// hold-down, a handshake older than 180 seconds is a failure, more than four
// downs in ten minutes pins a link for 30 minutes, and a probe every 5 seconds.
//
// Example: DefaultConfig().StaleAfter is 15 seconds.
func DefaultConfig() Config {
	return Config{
		FailThreshold:    3,
		RecoverThreshold: 5,
		StaleAfter:       15 * time.Second,
		HoldDown:         60 * time.Second,
		HandshakeMaxAge:  180 * time.Second,
		FlapLimit:        4,
		FlapWindow:       10 * time.Minute,
		PinFor:           30 * time.Minute,
		ProbeInterval:    5 * time.Second,
	}
}

// Validate checks every setting is usable and returns INVALID_CONFIG listing all
// that are not: a count below one, a time of zero or less, or a StaleAfter that
// is not longer than ProbeInterval.
//
// Example: a Config with RecoverThreshold 0 is refused, since no evidence would
// ever be needed.
func (cfg Config) Validate() error {
	var problems types.List
	count := func(name string, value int) {
		if value < 1 {
			problems.Add(types.Errf(types.ErrInvalidConfig, "health "+name, "it is %d, want at least 1", value))
		}
	}
	span := func(name string, value time.Duration) {
		if value <= 0 {
			problems.Add(types.Errf(types.ErrInvalidConfig, "health "+name, "it is %v, want more than zero", value))
		}
	}
	count("fail_threshold", cfg.FailThreshold)
	count("recover_threshold", cfg.RecoverThreshold)
	count("flap_limit", cfg.FlapLimit)
	span("stale_after", cfg.StaleAfter)
	span("hold_down", cfg.HoldDown)
	span("handshake_max_age", cfg.HandshakeMaxAge)
	span("flap_window", cfg.FlapWindow)
	span("pin_for", cfg.PinFor)
	span("probe_interval", cfg.ProbeInterval)
	if cfg.StaleAfter > 0 && cfg.ProbeInterval > 0 && cfg.StaleAfter <= cfg.ProbeInterval {
		problems.Add(types.Errf(types.ErrInvalidConfig, "health stale_after",
			"it is %v, which is not longer than probe_interval %v: a healthy link would go stale between probes", cfg.StaleAfter, cfg.ProbeInterval))
	}
	return problems.Err()
}
