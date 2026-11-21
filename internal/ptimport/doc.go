// Package ptimport reads Packet Tracer lab observations and turns them into expectations a
// plan can be compared with. It is a test-support package, imported by tests only, and it has
// no binary.
//
// An observation is what was seen on the lab's devices: the output of show ip route and the
// hops of traceroutes, transcribed by hand into one YAML file per device (see Parse). The
// importer maps device names to node IDs and next-hop addresses to node IDs through the plan's
// own address allocations, so the lab and the fixture share one address plan and a mismatch
// always means a real difference in behaviour.
//
// The match rules are the plan's: every allocation and every active route matches exactly
// (prefix and next hop); metrics may differ, because Cisco administrative distance is not a
// Beaver metric; backups match by prefix and next hop only, and must be inactive.
package ptimport
