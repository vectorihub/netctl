package phase1

import (
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vectorihub/netctl/internal/e2e"
)

// init registers Group 5, reproducibility: the same input gives the same bytes whatever the
// order of the input, the CPU count or the time zone.
func init() {
	register("P1-E2E-090", e2e090ManyRuns)
	register("P1-E2E-091", e2e091Portable)
	register("P1-E2E-092", e2e092TimeZone)
}

// runHashes plans the baseline of a workspace and returns the hashes of its two plans.
func runHashes(w *e2e.W) string {
	w.T.Helper()
	fresh, base := e2e.BaselineFrom(w, "three-site.yaml")
	return fresh.SubtreeHash("") + "/" + base.SubtreeHash("")
}

// e2e090ManyRuns is P1-E2E-090: fifty runs with the sites, nodes and links shuffled and a
// different number of CPUs give identical output. Intents are never shuffled.
func e2e090ManyRuns(w *e2e.W) {
	old := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(old)
	var want string
	for i := 0; i < 50; i++ {
		runtime.GOMAXPROCS(1 + i%8)
		x := e2e.New(w.T, w.ID)
		x.Copy("e2e/address-plan.yaml", "e2e/three-site.yaml")
		x.Edit("three-site.yaml", e2e.Shuffle(int64(i), "sites", "nodes", "links"))
		got := runHashes(x)
		if i == 0 {
			want = got
		} else if got != want {
			w.Fatalf("run %d (seed %d, GOMAXPROCS %d) differs from run 0", i, i, 1+i%8)
		}
	}
}

// e2e091Portable is P1-E2E-091: the output holds nothing that depends on the machine, no
// carriage returns and no backslashes in a path. The CI matrix compares the hashes across
// operating systems; the hashes are pinned in a golden file every platform must match.
func e2e091Portable(w *e2e.W) {
	fresh, base := e2e.Baseline(w)
	for _, out := range []*e2e.Out{fresh, base} {
		for path, data := range out.Files {
			if strings.Contains(string(data), "\r") {
				w.Fatalf("%s/%s holds a carriage return", out.Name, path)
			}
			if strings.Contains(path, `\`) {
				w.Fatalf("%s path %q holds a backslash", out.Name, path)
			}
			if strings.HasSuffix(path, ".json") && strings.Contains(string(data), `\\`) {
				w.Fatalf("%s/%s holds an escaped backslash, a Windows path", out.Name, path)
			}
		}
	}
	// The golden file holds the hashes. CI runs this on Linux and macOS against the same file,
	// which is the cross-platform comparison; a Windows-only difference would show the same way.
	w.Golden("hashes.txt", []byte(fresh.SubtreeHash("")+"\n"+base.SubtreeHash("")+"\n"))
}

// e2e092TimeZone is P1-E2E-092: the process time zone never leaks into the output, and every
// timestamp is UTC.
func e2e092TimeZone(w *e2e.W) {
	old := time.Local
	defer func() { time.Local = old }()
	var want string
	for i, zone := range []string{"UTC", "Africa/Nairobi", "America/Los_Angeles"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			w.T.Skipf("no tzdata for %s: %v", zone, err)
		}
		time.Local = loc
		x := e2e.New(w.T, w.ID+"-"+strconv.Itoa(i))
		x.Setup()
		got := runHashes(x)
		if i == 0 {
			want = got
		} else if got != want {
			w.Fatalf("output under %s differs from UTC", zone)
		}
	}
	_, base := e2e.Baseline(w)
	w.ExpectUTC(base)
}
