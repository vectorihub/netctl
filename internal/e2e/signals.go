package e2e

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// Signals builds a stream of health observations the way the guide's tests describe them:
// "five good probes from both ends, every five seconds, starting at T0+5s".
//
// Example:
//
//	s := (&e2e.Signals{}).Probes("aws-hub~builder-1", true, 5, 0, time.Second)
type Signals struct{ list []health.Signal }

// probeOpts are the settings an Opt changes.
type probeOpts struct {
	from string
	kind health.SignalKind
}

// Opt changes how Probes builds its signals.
//
// Example:
//
//	s.Probes("aws-hub~builder-1", false, 3, 0, time.Second, e2e.OneWay("aws-hub"))
type Opt func(*probeOpts)

// OneWay makes only the given end of the link observe the signals, which is how the tests
// say "recovery in one direction only".
//
// Example:
//
//	s.Probes("builder-1~dgx-spark", true, 5, 0, 5*time.Second, e2e.OneWay("builder-1"))
func OneWay(from string) Opt { return func(o *probeOpts) { o.from = from } }

// Kind sets what sort of evidence the signals are: "probe" (the default), "handshake",
// "interface" or "admin". An unknown name panics, because a test that misspells it is wrong.
//
// Example:
//
//	s.Probes(link, false, 3, 35*time.Second, 5*time.Second, e2e.Kind("handshake"))
func Kind(name string) Opt {
	kind, ok := health.ParseSignalKind(name)
	if !ok {
		panic("e2e: unknown signal kind " + name)
	}
	return func(o *probeOpts) { o.kind = kind }
}

// Probes adds n observations of a link, one every interval from start (an offset from T0).
// Unless OneWay is given, each end of the link makes each observation, so n probes become
// 2n signals, the first end's before the second's at equal times. It returns the builder so
// calls chain.
//
// Example:
//
//	new(e2e.Signals).Probes("builder-1~dgx-spark", true, 5, 5*time.Second, 5*time.Second)
func (s *Signals) Probes(link string, ok bool, n int, start, interval time.Duration, opts ...Opt) *Signals {
	cfg := probeOpts{kind: health.SignalProbe}
	for _, opt := range opts {
		opt(&cfg)
	}
	ends := strings.Split(link, "~")
	if cfg.from != "" {
		ends = []string{cfg.from}
	}
	for i := 0; i < n; i++ {
		at := T0.Add(start + time.Duration(i)*interval)
		for _, end := range ends {
			s.list = append(s.list, health.Signal{Link: types.LinkID(link), From: types.NodeID(end), Kind: cfg.kind, OK: ok, At: at})
		}
	}
	return s
}

// Add appends the signals of other to s, for streams built in pieces.
//
// Example:
//
//	all := s.Add(other) // both lists, in order
func (s *Signals) Add(other *Signals) *Signals {
	s.list = append(s.list, other.list...)
	return s
}

// List returns the signals in time order. Equal times keep the order they were added in,
// which is the rule for a signals file.
//
// Example:
//
//	list := s.List() // the health.Signal values to give Inputs.Signals
func (s *Signals) List() []health.Signal {
	out := slices.Clone(s.list)
	slices.SortStableFunc(out, func(a, b health.Signal) int { return a.At.Compare(b.At) })
	return out
}

// Len returns how many signals there are.
//
// Example:
//
//	n := s.Len()
func (s *Signals) Len() int { return len(s.list) }

// signalsFile is the signals file format of the contract.
type signalsFile struct {
	Schema  string          `json:"schema"`
	Signals []health.Signal `json:"signals"`
}

// Write saves the signals as a signals file in the workspace and returns its name. The
// plan takes the signals directly; the file is kept so a kept workspace shows what was fed.
//
// Example:
//
//	s.Write(w, "s-fail.json")
func (s *Signals) Write(w *W, name string) string {
	w.T.Helper()
	raw, err := json.MarshalIndent(signalsFile{Schema: "beaver.signals/v1", Signals: s.List()}, "", "  ")
	if err != nil {
		w.Fatalf("write %s: %v", name, err)
	}
	w.Write(name, append(raw, '\n'))
	return name
}
