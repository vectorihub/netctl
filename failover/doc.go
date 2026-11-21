// Package failover decides what the routing tables should be when links fail or
// come back, and refuses to install a change that makes things worse.
//
// It joins three packages that know nothing of each other. [health] says which
// links can be trusted. route computes the tables for the links that can. sim
// plays flows over the result. [Evaluate] runs them in that order, once, and
// explains what changed and why.
//
// # One evaluation
//
// Evaluate takes the network, the last [State] and a batch of signals, and
// returns a [Result]: the next State, the tables to install, and a [Decision]
// for every link whose health changed. A decision says which link, from what
// to what, at what time and for what reason, and lists the routes it activated
// and withdrew on every node. When the direct link between two spokes goes
// down, the one decision lists the backups through the hub on both spokes,
// because the link is only usable when both directions are healthy, so both
// ends move in the same evaluation, and the same is true on the way back.
//
// # The guard
//
// Before anything is installed, the new tables are simulated over the network as
// health now has it. If a flow works with the old tables and fails with the new,
// a loop for example, nothing is installed: the old tables stay, the health
// change is still recorded, and [Result.Rejected] says which flows would have
// broken. A flow that is cut off because every path to it has failed is not a
// reason to reject: isolating a node whose links are all down is correct.
//
// # Restarts
//
// A [State] holds the link states with their hold-down, pin and flap timers, and
// the installed tables. [State.JSON] writes it and [ParseState] reads it back, so
// the control plane can stop mid hold-down and carry on, and the hold-down ends
// when it would have. The package reads no clock and no file: the caller passes
// the time, and saves and loads the bytes.
package failover
