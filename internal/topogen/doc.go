// Package topogen generates topologies for tests: scaled hub-and-spoke networks for the scale
// tests, and random valid ones for the chaos tests. The same seed always gives the same
// topology. It is a test-support package, imported by tests only.
//
// Generated topologies carry their own, larger address plan (a /8 with room for thousands of
// sites), because the standard plan has only eight site blocks. Several nodes share a site
// (up to sixteen) and only the first node of a site advertises the site prefix.
package topogen
