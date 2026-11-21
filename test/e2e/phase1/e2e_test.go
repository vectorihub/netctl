// Package phase1 is the Phase 1 end-to-end suite. It plans through the exported netctl API
// only and checks the files the plan renders, never an unexported function. The tests that
// need a process (exit codes, unwritable directories, hostile files) are the beaver-cli
// suite.
package phase1

import (
	"testing"

	"github.com/vectorihub/netctl/internal/e2e"
)

// testCase is one registered end-to-end test.
type testCase struct {
	// id is the test ID, for example "P1-E2E-020", also the name of its subtest.
	id string
	// slow marks the scale and chaos tests that -short skips.
	slow bool
	// run is the test body.
	run func(w *e2e.W)
}

// cases is every registered test, filled by the init functions of the group files.
var cases []testCase

// register adds a test to the suite.
func register(id string, run func(w *e2e.W)) { cases = append(cases, testCase{id: id, run: run}) }

// registerSlow adds a test that -short skips: the scale and chaos groups.
func registerSlow(id string, run func(w *e2e.W)) {
	cases = append(cases, testCase{id: id, slow: true, run: run})
}

// TestE2E runs every registered test as a parallel subtest named by its ID, so
// `-run 'TestE2E/P1-E2E-020'` selects one and `-run 'TestE2E/P1-E2E-02'` a group.
func TestE2E(t *testing.T) {
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			if c.slow && testing.Short() {
				t.Skip("scale and chaos tests are skipped under -short")
			}
			t.Parallel()
			c.run(e2e.New(t, c.id))
		})
	}
}
