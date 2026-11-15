// Package netctl is the network planner for Beaver Cloud.
//
// It is a pure library: it decides how the network should look and returns the
// result. It has no server, no port, no database, no command-line program and
// no file, clock, environment or network access. Callers do all I/O and pass
// everything in.
//
// The public entry point (Plan, Inputs, Result) is added in Era 10 (V74).
package netctl
