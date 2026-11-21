// Package plan is the one pure function from a network description and its state to a
// plan: the addresses, the routes, a config for every node, and the verdict of the
// validator. It reads no file, opens no connection and asks no clock but the one it is
// given. The same inputs give the same plan, byte for byte.
//
// # The order of operations
//
// [Plan] runs the pipeline in a fixed order, and each step depends on the one before:
//
//  1. Load the address plan with ipam.New. A damaged snapshot is refused before anything
//     else runs.
//  2. Release what is gone: allocations whose owner is no longer in the topology, nodes and
//     links first and then sites, so a site and its machines can go in one plan.
//  3. Allocate the sites, sorted by ID.
//  4. Allocate the overlay addresses, sorted by ID.
//  5. Allocate a transit /30 for every direct link whose health must be proven, sorted by
//     link ID.
//  6. Carry the health of each link over by its ID; a new link starts Unknown.
//  7. Build the graph with only the usable links.
//  8. Compute every node's routes.
//  9. Generate a config for every node.
//  10. Validate the whole plan, simulation and the single-failure sweep included.
//  11. Guard: a plan that fails validation, when the plan in force passed, is rejected and
//     the plan in force stays.
//
// When the caller passes health signals or the failover state of the last plan, steps 7 and 8
// run under the failover guard: the signals are folded in, the new routes are simulated
// before they are used, and a failover that would break a flow that worked is refused. The
// routes in force stay, the plan still succeeds, and a FailoverRejected decision says why.
// [Result].Failover holds the state to save for the next plan.
//
// A failure in steps 1 to 5 fails the plan before any config exists. Addresses are
// allocated in sorted order and never in the order the topology lists them, so shuffling
// the file changes nothing, and idempotency keeps every machine's address on later plans.
//
// # Refusing
//
// If the validator finds an error, Plan returns the result with its validation report and
// an error with the code VALIDATION_FAILED, and the result holds no configs. A caller that
// writes files writes nothing in that case.
package plan
