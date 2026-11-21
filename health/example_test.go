package health_test

// This file holds the runnable examples shown on the package documentation. They
// use only the exported API. Times are written as seconds after a fixed start so
// that each example replays the same timeline every run.

import (
	"fmt"
	"time"

	"github.com/vectorihub/netctl/health"
	"github.com/vectorihub/netctl/types"
)

// start is the instant the examples count seconds from.
var start = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// at returns the instant n seconds after start.
func at(n int) time.Time { return start.Add(time.Duration(n) * time.Second) }

// probes returns n probe signals on the link from both ends, one every 5 seconds
// from the given second.
func probes(ok bool, n, from int) []health.Signal {
	var out []health.Signal
	for i := 0; i < n; i++ {
		for _, end := range []string{"builder-1", "dgx-spark"} {
			out = append(out, health.Signal{
				Link: "builder-1~dgx-spark", From: nodeID(end), Kind: health.SignalProbe, OK: ok, At: at(from + 5*i),
			})
		}
	}
	return out
}

// ExampleDefaultConfig shows the standard settings.
func ExampleDefaultConfig() {
	cfg := health.DefaultConfig()
	fmt.Println("failures to go down:", cfg.FailThreshold)
	fmt.Println("good signals to recover:", cfg.RecoverThreshold)
	fmt.Println("silence counts as failure after:", cfg.StaleAfter)
	fmt.Println("hold-down:", cfg.HoldDown)
	// Output:
	// failures to go down: 3
	// good signals to recover: 5
	// silence counts as failure after: 15s
	// hold-down: 1m0s
}

// ExampleConfig_Validate shows a setting that makes no sense being refused, and
// the refusal naming it.
func ExampleConfig_Validate() {
	cfg := health.DefaultConfig()
	cfg.RecoverThreshold = 0
	fmt.Println(cfg.Validate())
	// Output:
	// INVALID_CONFIG: health recover_threshold: it is 0, want at least 1
}

// ExampleDirection_Succeed shows a new direction earning trust: four good signals
// are not enough, the fifth makes it healthy.
func ExampleDirection_Succeed() {
	cfg := health.DefaultConfig()
	var d health.Direction
	for i := 1; i <= 5; i++ {
		state, _ := d.Succeed(at(5*i), cfg)
		fmt.Println(i, state)
	}
	// Output:
	// 1 unknown
	// 2 unknown
	// 3 unknown
	// 4 unknown
	// 5 healthy
}

// ExampleDirection_Fail shows a healthy direction failing: one failure makes it
// degraded, with routes unchanged, and the third in a row makes it down.
func ExampleDirection_Fail() {
	cfg := health.DefaultConfig()
	d := health.Direction{State: health.Healthy, LastEvidence: at(25)}
	for i := 0; i < 3; i++ {
		state, _ := d.Fail(at(35+5*i), cfg)
		fmt.Println(state)
	}
	fmt.Println("hold-down until", d.HoldDownUntil.Sub(start))
	// Output:
	// degraded
	// degraded
	// down
	// hold-down until 1m45s
}

// ExampleDirection_InterfaceDown shows an interface that has gone taking a
// direction down at once, without waiting for three failures.
func ExampleDirection_InterfaceDown() {
	cfg := health.DefaultConfig()
	d := health.Direction{State: health.Healthy, LastEvidence: at(25)}
	state, reason := d.InterfaceDown(at(30), cfg)
	fmt.Println(state, "-", reason)
	// Output:
	// down - interface is down
}

// ExampleDirection_CheckStale shows silence being judged. A direction last heard
// from at 25 seconds is fine at 40 seconds, and is down, since 40 seconds, at 41.
func ExampleDirection_CheckStale() {
	cfg := health.DefaultConfig()
	d := health.Direction{State: health.Healthy, LastEvidence: at(25)}
	_, stale := d.CheckStale(at(40), cfg)
	fmt.Println("at 40 s stale:", stale)
	wentStale, stale := d.CheckStale(at(41), cfg)
	fmt.Println("at 41 s stale:", stale, "since", wentStale.Sub(start), "state", d.State)
	// Output:
	// at 40 s stale: false
	// at 41 s stale: true since 40s state down
}

// ExampleNewLink shows the difference between a required link, which must be
// proven before it is used, and an assumed one, which is used until it fails.
func ExampleNewLink() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	hubSpoke, _ := health.NewLink("aws-hub~builder-1", "aws-hub", "builder-1", false)
	fmt.Println("direct usable:", direct.Usable())
	fmt.Println("hub-spoke usable:", hubSpoke.Usable())
	// Output:
	// direct usable: false
	// hub-spoke usable: true
}

// ExampleEvaluator_Apply replays a failure: a direct link proven by probes, then
// three failed probes from each end. Each change comes back as a decision with
// its reason.
func ExampleEvaluator_Apply() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	e, _ := health.NewEvaluator(health.DefaultConfig(), direct)

	decisions, _ := e.Apply(probes(true, 5, 5), at(30))
	for _, d := range decisions {
		fmt.Printf("%s: %s to %s at %s\n", d.Link, d.Before, d.After, d.At.Sub(start))
	}
	decisions, _ = e.Apply(probes(false, 3, 35), at(50))
	for _, d := range decisions {
		fmt.Printf("%s: %s to %s at %s\n", d.Link, d.Before, d.After, d.At.Sub(start))
	}
	// Output:
	// builder-1~dgx-spark: unknown to healthy at 25s
	// builder-1~dgx-spark: healthy to degraded at 35s
	// builder-1~dgx-spark: degraded to down at 45s
}

// ExampleEvaluator_Apply_oneDirection shows why a link is only as good as its
// worse direction. builder-1 hears dgx-spark, but dgx-spark hears nothing: the
// link is down for both.
func ExampleEvaluator_Apply_oneDirection() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	e, _ := health.NewEvaluator(health.DefaultConfig(), direct)
	e.Apply(probes(true, 5, 5), at(30))

	var signals []health.Signal
	for i := 0; i < 6; i++ {
		signals = append(signals,
			health.Signal{Link: direct.ID, From: "builder-1", Kind: health.SignalProbe, OK: true, At: at(35 + 5*i)},
			health.Signal{Link: direct.ID, From: "dgx-spark", Kind: health.SignalProbe, OK: false, At: at(35 + 5*i)})
	}
	e.Apply(signals, at(65))
	fmt.Println("link:", direct.State, "usable:", direct.Usable())
	fmt.Println("builder-1 sees:", direct.Dirs["builder-1"].State)
	fmt.Println("dgx-spark sees:", direct.Dirs["dgx-spark"].State)
	// Output:
	// link: down usable: false
	// builder-1 sees: healthy
	// dgx-spark sees: down
}

// ExampleEvaluator_Apply_interface shows that an interface that is up proves
// nothing. Ten minutes of interface-up signals leave a new link unknown, and so
// unused.
func ExampleEvaluator_Apply_interface() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	e, _ := health.NewEvaluator(health.DefaultConfig(), direct)
	var signals []health.Signal
	for i := 0; i < 120; i++ {
		signals = append(signals, health.Signal{Link: direct.ID, From: "builder-1", Kind: health.SignalInterface, OK: true, At: at(5 * i)})
	}
	e.Apply(signals, at(600))
	fmt.Println("link:", direct.State, "usable:", direct.Usable())
	// Output:
	// link: unknown usable: false
}

// ExampleEvaluator_UnusableLinks shows the list a routing graph needs: the links
// not to use right now.
func ExampleEvaluator_UnusableLinks() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	hubSpoke, _ := health.NewLink("aws-hub~builder-1", "aws-hub", "builder-1", false)
	e, _ := health.NewEvaluator(health.DefaultConfig(), direct, hubSpoke)
	fmt.Println("before any evidence:", e.UnusableLinks())
	e.Apply(probes(true, 5, 5), at(30))
	fmt.Println("after the direct link is proven:", e.UnusableLinks())
	// Output:
	// before any evidence: [builder-1~dgx-spark]
	// after the direct link is proven: []
}

// ExampleParseLinkHealth reads a state back from its name.
func ExampleParseLinkHealth() {
	state, ok := health.ParseLinkHealth("recovering")
	fmt.Println(state, ok)
	_, ok = health.ParseLinkHealth("fine")
	fmt.Println(ok)
	// Output:
	// recovering true
	// false
}

// ExampleParseSignalKind reads a kind back from its name.
func ExampleParseSignalKind() {
	kind, ok := health.ParseSignalKind("handshake")
	fmt.Println(kind, ok)
	// Output:
	// handshake true
}

// nodeID converts text to the type the health package uses for node IDs.
func nodeID(text string) types.NodeID { return types.NodeID(text) }
