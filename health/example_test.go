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

// ExampleSignalKind_String shows the name of a signal kind.
func ExampleSignalKind_String() {
	fmt.Println(health.SignalHandshake, health.SignalUnknown)
	// Output:
	// handshake unknown
}

// ExampleSignalKind_MarshalText shows a kind written as its name, and the zero
// kind refused with a coded error.
func ExampleSignalKind_MarshalText() {
	text, _ := health.SignalProbe.MarshalText()
	_, err := health.SignalUnknown.MarshalText()
	fmt.Println(string(text), types.HasCode(err, types.ErrInvalidValue))
	// Output:
	// probe true
}

// ExampleSignalKind_UnmarshalText shows a kind read back, and a wrong name
// refused with the receiver left alone.
func ExampleSignalKind_UnmarshalText() {
	var kind health.SignalKind
	fmt.Println(kind.UnmarshalText([]byte("interface")), kind)
	fmt.Println(types.HasCode(kind.UnmarshalText([]byte("ping")), types.ErrInvalidValue), kind)
	// Output:
	// <nil> interface
	// true interface
}

// ExampleLinkHealth_String shows the name of a state.
func ExampleLinkHealth_String() {
	fmt.Println(health.Recovering, health.Unknown)
	// Output:
	// recovering unknown
}

// ExampleLinkHealth_MarshalText shows a state written as its name, and a value
// outside the five refused with a coded error.
func ExampleLinkHealth_MarshalText() {
	text, _ := health.Down.MarshalText()
	_, err := health.LinkHealth(99).MarshalText()
	fmt.Println(string(text), types.HasCode(err, types.ErrInvalidValue))
	// Output:
	// down true
}

// ExampleLinkHealth_UnmarshalText shows a state read back, and a wrong
// capitalisation refused.
func ExampleLinkHealth_UnmarshalText() {
	var state health.LinkHealth
	fmt.Println(state.UnmarshalText([]byte("healthy")), state)
	fmt.Println(types.HasCode(state.UnmarshalText([]byte("Healthy")), types.ErrInvalidValue), state)
	// Output:
	// <nil> healthy
	// true healthy
}

// ExampleLink_Usable shows the two kinds of link before any evidence: a required
// direct link must be proven by probes before routing may use it, while an
// assumed hub-spoke link is usable until a failure arrives.
func ExampleLink_Usable() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	hub, _ := health.NewLink("aws-hub~builder-1", "aws-hub", "builder-1", false)
	fmt.Println(direct.Usable(), hub.Usable())
	e, _ := health.NewEvaluator(health.DefaultConfig(), direct)
	e.Apply(probes(true, 5, 5), at(30))
	fmt.Println(direct.Usable())
	// Output:
	// false true
	// true
}

// ExampleNewEvaluator builds an evaluator for one link, and shows that two links
// with the same ID are refused with INVALID_TOPOLOGY.
func ExampleNewEvaluator() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	e, err := health.NewEvaluator(health.DefaultConfig(), direct)
	fmt.Println(len(e.Links), err)
	twin, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	_, err = health.NewEvaluator(health.DefaultConfig(), direct, twin)
	fmt.Println(types.HasCode(err, types.ErrInvalidTopology))
	// Output:
	// 1 <nil>
	// true
}

// ExampleEvaluator_Apply_drain shows an operator taking a healthy link out of
// service. The drain is Down at once and good probes cannot undo it. The undrain
// does not bring the link back either: it restarts the hold-down, and the link
// must prove itself again, through Recovering.
func ExampleEvaluator_Apply_drain() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	e, _ := health.NewEvaluator(health.DefaultConfig(), direct)
	e.Apply(probes(true, 5, 5), at(30))

	drain := health.Signal{Link: "builder-1~dgx-spark", Kind: health.SignalAdmin, OK: false, At: at(40)}
	decisions, _ := e.Apply([]health.Signal{drain}, at(40))
	fmt.Println(decisions[0].After, "-", decisions[0].Reason)

	e.Apply(probes(true, 10, 45), at(95)) // good probes while drained change nothing
	fmt.Println(direct.State, direct.Usable())

	undrain := health.Signal{Link: "builder-1~dgx-spark", Kind: health.SignalAdmin, OK: true, At: at(100)}
	e.Apply([]health.Signal{undrain}, at(100))
	fmt.Println(direct.State) // down until the hold-down, 60 s, has passed
	decisions, _ = e.Apply(probes(true, 5, 165), at(186))
	for _, d := range decisions {
		fmt.Println(d.Before, "to", d.After)
	}
	// Output:
	// down - admin drain: an operator took the link out of service
	// down false
	// down
	// down to recovering
	// recovering to healthy
}

// ExampleEvaluator_Apply_flapLimit shows a link that keeps failing and recovering
// being pinned Down. The fifth time it goes Down within ten minutes is more than
// the limit of four, and from then on good evidence is not enough.
func ExampleEvaluator_Apply_flapLimit() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	e, _ := health.NewEvaluator(health.DefaultConfig(), direct)
	e.Apply(probes(true, 5, 5), at(30))
	for _, from := range []int{35, 135, 235, 335, 435} {
		e.Apply(probes(false, 3, from), at(from+15))
		if from != 435 {
			e.Apply(probes(true, 5, from+75), at(from+96)) // back to Healthy
		}
	}
	fmt.Println(direct.State, "pinned:", direct.Pinned, "flaps:", len(direct.Flaps))
	fmt.Println("pinned until", direct.PinnedUntil.Sub(start))
	e.Apply(probes(true, 10, 500), at(560)) // perfect probes do not lift a pin
	fmt.Println(direct.State)
	// Output:
	// down pinned: true flaps: 5
	// pinned until 37m25s
	// down
}

// ExampleLink_Clone shows a copy being changed without touching the original.
func ExampleLink_Clone() {
	direct, _ := health.NewLink("builder-1~dgx-spark", "builder-1", "dgx-spark", true)
	copy := direct.Clone()
	copy.Drained = true
	copy.Dirs["builder-1"].State = health.Down
	fmt.Println(direct.Drained, direct.Dirs["builder-1"].State)
	// Output:
	// false unknown
}

// ExampleLinkHealth_Usable shows which states routing may use: a required link
// must be proven, so Unknown is only usable for a link that is assumed.
func ExampleLinkHealth_Usable() {
	fmt.Println(health.Healthy.Usable(true), health.Degraded.Usable(true))
	fmt.Println(health.Down.Usable(false), health.Recovering.Usable(false))
	fmt.Println(health.Unknown.Usable(true), health.Unknown.Usable(false))
	// Output:
	// true true
	// false false
	// false true
}
