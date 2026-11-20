// Package sim is a virtual test bench for a network's routing tables. It pushes
// simulated packets through the tables a plan produced and reports every way the
// network would fail in reality, before anything is applied to a real machine.
//
// A set of routing tables can look right on paper and still not work. The
// simulator looks for four kinds of failure:
//
//   - a black hole: a packet reaches a node that has no route for it;
//   - a routing loop: a packet travels round and round between nodes;
//   - a missing return path: the destination receives the packet but its reply
//     cannot get back;
//   - an asymmetric path: the packet and its reply take different routes.
//
// Asymmetry is not always wrong, but it is an error here unless a pair is
// explicitly allowed to be asymmetric, because WireGuard's cryptokey routing and
// stateful firewalls both break on it.
//
// # The network under test
//
// A [Net] is every node's routing table plus three questions the tables cannot
// answer: is this address the node's own, are these two nodes really joined by
// this link, and is the link up. They are functions, so the package depends on
// neither the topology nor the health packages. [NewNet] builds a Net from a
// [route.Graph] and the prefixes each node holds.
//
// # Sending a packet
//
// [Net.Send] forwards one [Packet] from a node, hop by hop, until it is
// delivered or something stops it, and returns a [Trace] of every step. A loop
// is detected exactly: forwarding is deterministic and the destination is fixed,
// so a packet that returns to a node will repeat its journey for ever. The hop
// limit (TTL) is a second, independent net behind it. Every packet ends with a
// [HopResult]: delivered, black hole, link down, wrong neighbour, loop or TTL
// exceeded.
//
// # Judging a flow
//
// A [Flow] is a packet from one address to another with a reply expected back.
// [Net.Run] sends the packet, and if it arrives sends the reply, then [Judge]
// turns the two traces into one [Verdict]. [Symmetric] says whether the return
// path is exactly the forward path reversed, in nodes and in links. Each
// verdict says whether it fails the gate ([Verdict.FailsGate]): OK, an allowed
// asymmetry, an expected unreachable address and a pair with no healthy path
// pass; the rest fail.
//
// # Deriving the flows
//
// The simulator tests more than the user asked for. [Flows] derives the flow set
// from a topology and its address plan: an overlay flow between every ordered
// pair of nodes (allow intents are merged into these), a site flow between
// every ordered pair of advertised sites, one flow for each deny intent, and
// one negative flow per node to an address outside the supernet. The order is
// fixed.
//
// # Reports
//
// [NewReport] gathers the results of a set of flows, sorted by label, with a
// [Summary] that counts verdicts and problems, and says whether the run passed.
//
// # Scope
//
// The simulator models per-node forwarding by longest-prefix match, the hop
// limit, link health, delivery to a node's own addresses, and the reply. It does
// not model bandwidth, latency, loss, MTU, NAT or firewall policy. It does no
// I/O and never calls the routing engine itself: the tables it is given are the
// only routing it knows.
package sim
