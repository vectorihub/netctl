// Package e2e is the end-to-end test harness for Phase 1. It is imported by tests only.
//
// An end-to-end test starts from what a user would hand over (a topology file, a saved
// address plan, a stream of health signals), plans through the exported netctl API only,
// writes what Result.Render returns into a workspace directory, and checks the files, their
// hashes, golden files and the JSON reports. It never reaches an unexported function, so a
// wiring mistake in the public surface shows up here.
//
// Tests that need a process (exit codes, signals, unwritable directories) belong to the
// beaver-cli suite, which drives the same planner through the command line.
//
// A workspace is one test's private directory. The harness keeps a numbered log of every
// step, so a failure names the step it stopped at, and copies the workspace under
// artifacts/<test-id>/ when a test fails or when the -keep flag is given.
package e2e
