package tests

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/failover"
	"github.com/vectorihub/netctl/health"
)

// update rewrites the golden files from the current output instead of comparing
// with them: go test ./failover/tests -run Golden -update. Read the diff before
// committing it.
var update = flag.Bool("update", false, "rewrite the golden decision logs")

// golden compares got with the golden file, or rewrites the file under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "../../testdata/golden/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden file: %v (create it with -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file; run with -update and review the change\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

// logEntry is one evaluation as the golden log records it.
type logEntry struct {
	// At is the time the evaluation was made.
	At time.Time `json:"at"`
	// Decisions are the decisions it made.
	Decisions []failover.Decision `json:"decisions"`
	// Skipped are the signals it could not use.
	Skipped []health.Skipped `json:"skipped"`
	// Rejected is the guard's refusal, if any.
	Rejected *failover.FailoverRejected `json:"rejected"`
}

// timeline runs the reviewed story on the three-site network and returns what
// each evaluation decided: the network is proven at 30 s, the direct link fails
// at 45 s, comes back and is trusted again at 130 s, and an operator drains the
// builder-1 hub link at 200 s, undrains it at 260 s, and it is trusted again at 345 s
// after the hold-down.
func timeline(t *testing.T) []logEntry {
	t.Helper()
	net := threeSite(t)
	drain := health.Signal{Link: hubB, Kind: health.SignalAdmin, OK: false, At: at(200)}
	undrain := health.Signal{Link: hubB, Kind: health.SignalAdmin, OK: true, At: at(260)}
	steps := []struct {
		signals []health.Signal
		now     int
	}{
		{probes(direct, true, 5, 5), 30},
		{probes(direct, false, 3, 35), 50},
		{probes(direct, true, 5, 110), 131},
		{join(probes(direct, true, 12, 135), []health.Signal{drain}), 200},
		{join([]health.Signal{undrain}, probes(direct, true, 17, 195)), 280},
		{join(probes(direct, true, 16, 285), probes(hubB, true, 5, 325)), 360},
	}
	var log []logEntry
	state := failover.State{}
	for _, step := range steps {
		result := evaluate(t, net, state, step.signals, step.now)
		state = result.State
		log = append(log, logEntry{At: at(step.now), Decisions: result.Decisions, Skipped: result.Skipped, Rejected: result.Rejected})
	}
	return log
}

// text renders the log one decision to a line, then the routes it moved, indented:
// the form a person reviews.
func text(log []logEntry) []byte {
	var b strings.Builder
	for _, entry := range log {
		fmt.Fprintf(&b, "evaluation at %s\n", entry.At.Format(time.RFC3339))
		if len(entry.Decisions) == 0 {
			b.WriteString("  no decisions\n")
		}
		for _, d := range entry.Decisions {
			fmt.Fprintf(&b, "  %s %s %s -> %s installed=%v: %s\n", d.At.Format(time.RFC3339), d.Link, d.Before, d.After, d.Installed, d.Reason)
			for _, r := range d.Withdrawn {
				fmt.Fprintf(&b, "    withdrawn %s %s via %s\n", r.Node, r.Prefix, r.Via)
			}
			for _, r := range d.Activated {
				fmt.Fprintf(&b, "    activated %s %s via %s\n", r.Node, r.Prefix, r.Via)
			}
		}
	}
	return []byte(b.String())
}

// P1-FAIL-027
//
// TestGoldenDecisionLog checks the decisions, with their reasons and timestamps,
// match the reviewed log, as text for a person and as JSON for a machine, and
// that running the story twice gives the same bytes.
func TestGoldenDecisionLog(t *testing.T) {
	log := timeline(t)
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	again, err := json.MarshalIndent(timeline(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(append(again, '\n')) {
		t.Error("the same story gave different decision logs")
	}
	golden(t, "failover-decisions.txt", text(log))
	golden(t, "failover-decisions.json", data)
	for _, entry := range log {
		for _, d := range entry.Decisions {
			if d.Reason == "" || d.At.IsZero() {
				t.Errorf("a decision has no reason or no time: %+v", d)
			}
		}
	}
}
