package e2e

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// maxHops is how many nodes a packet may cross before the oracle calls it a loop.
const maxHops = 8

// oracleNode is what the oracle reads of one node's rendered config. The oracle uses no
// netctl package: it sees only the JSON an agent would be given, so that it checks the
// configs themselves, independently of the planner and the simulator.
type oracleNode struct {
	Overlay struct {
		Address string `json:"address"`
	} `json:"overlay"`
	Advertise []string `json:"advertise"`
	Peers     []struct {
		Node            string   `json:"node"`
		AllowedPrefixes []string `json:"allowed_prefixes"`
	} `json:"peers"`
	Routes []struct {
		Prefix string `json:"prefix"`
		Via    string `json:"via"`
		Metric int    `json:"metric"`
		Active bool   `json:"active"`
	} `json:"routes"`
}

// Path is one packet's journey as the oracle walks it.
//
// Example:
//
//	path := w.OraclePath(out, "builder-1", "dgx-spark")
//	hops := path.Hops
type Path struct {
	// Hops are the node IDs visited, source first.
	Hops []string
	// Delivered says the packet reached the node that owns the destination.
	Delivered bool
	// Problem says why it did not, or is empty.
	Problem string
}

// OraclePair is both directions between two nodes.
//
// Example:
//
//	pairs, err := out.Oracle()
//	pair := pairs[[2]string{"builder-1", "dgx-spark"}]
type OraclePair struct {
	// Forward is the walk from the first node to the second's overlay address.
	Forward Path
	// Return is the walk back.
	Return Path
	// Symmetric says the return path is the forward path reversed.
	Symmetric bool
}

// Oracle walks a packet between every ordered pair of nodes of a plan, using only the
// rendered node configs. At each node it takes the longest matching active route, checks
// that the next hop is a peer whose allowed prefixes hold the destination (otherwise
// WireGuard would drop the packet), and checks that the receiving peer would accept the
// source address. It fails on a missing route, a missing peer, a disallowed prefix, a
// revisited node, or more than eight hops. The keys are [source, destination].
//
// Example:
//
//	pairs, err := out.Oracle() // every pair of nodes
func (o *Out) Oracle() (map[[2]string]OraclePair, error) {
	nodes, err := o.oracleNodes()
	if err != nil {
		return nil, err
	}
	pairs := map[[2]string]OraclePair{}
	for _, src := range sortedKeys(nodes) {
		for _, dst := range sortedKeys(nodes) {
			if src == dst {
				continue
			}
			fwd := walk(nodes, src, dst)
			ret := walk(nodes, dst, src)
			sym := fwd.Delivered && ret.Delivered && slices.Equal(fwd.Hops, reversed(ret.Hops))
			pairs[[2]string{src, dst}] = OraclePair{Forward: fwd, Return: ret, Symmetric: sym}
		}
	}
	return pairs, nil
}

// OracleSample is Oracle for the given pairs only, for networks too large to walk in full.
//
// Example:
//
//	pairs, err := out.OracleSample([][2]string{{"builder-1", "dgx-spark"}})
func (o *Out) OracleSample(pairs [][2]string) (map[[2]string]OraclePair, error) {
	nodes, err := o.oracleNodes()
	if err != nil {
		return nil, err
	}
	out := map[[2]string]OraclePair{}
	for _, p := range pairs {
		fwd, ret := walk(nodes, p[0], p[1]), walk(nodes, p[1], p[0])
		out[p] = OraclePair{Forward: fwd, Return: ret, Symmetric: fwd.Delivered && ret.Delivered && slices.Equal(fwd.Hops, reversed(ret.Hops))}
	}
	return out, nil
}

// oracleNodes parses the node configs of a plan, by node ID.
func (o *Out) oracleNodes() (map[string]*oracleNode, error) {
	nodes := map[string]*oracleNode{}
	for path, data := range o.Files {
		rest, ok := strings.CutPrefix(path, "nodes/")
		if !ok {
			continue
		}
		var n oracleNode
		if err := json.Unmarshal(data, &n); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		nodes[strings.TrimSuffix(rest, ".json")] = &n
	}
	return nodes, nil
}

// sortedKeys returns the keys of the node map in order.
func sortedKeys(m map[string]*oracleNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// reversed returns a reversed copy of a slice.
func reversed(s []string) []string {
	r := slices.Clone(s)
	slices.Reverse(r)
	return r
}

// walk sends one packet from src to the overlay address of dst and reports its journey.
func walk(nodes map[string]*oracleNode, src, dst string) Path {
	from, to := nodes[src], nodes[dst]
	path := Path{Hops: []string{src}}
	if from == nil || to == nil {
		path.Problem = "unknown node"
		return path
	}
	srcAddr, err1 := netip.ParsePrefix(from.Overlay.Address)
	dstAddr, err2 := netip.ParsePrefix(to.Overlay.Address)
	if err1 != nil || err2 != nil {
		path.Problem = "bad overlay address"
		return path
	}
	cur := src
	seen := map[string]bool{src: true}
	for {
		node := nodes[cur]
		if owns(node, dstAddr.Addr()) {
			path.Delivered = true
			return path
		}
		if len(path.Hops) > maxHops {
			path.Problem = fmt.Sprintf("more than %d hops", maxHops)
			return path
		}
		via, ok := lookup(node, dstAddr.Addr())
		if !ok {
			path.Problem = "no route at " + cur
			return path
		}
		next := nodes[via]
		if next == nil {
			path.Problem = fmt.Sprintf("%s routes via %s, which has no config", cur, via)
			return path
		}
		if !allowed(node, via, dstAddr.Addr()) {
			path.Problem = fmt.Sprintf("%s not in allowed_prefixes of peer %s on %s", dstAddr.Addr(), via, cur)
			return path
		}
		if !allowed(next, cur, srcAddr.Addr()) {
			path.Problem = fmt.Sprintf("%s would drop %s: not in allowed_prefixes of its peer %s", via, srcAddr.Addr(), cur)
			return path
		}
		if seen[via] {
			path.Problem = "loop through " + via
			path.Hops = append(path.Hops, via)
			return path
		}
		seen[via] = true
		path.Hops = append(path.Hops, via)
		cur = via
	}
}

// owns reports whether the address falls in a prefix the node advertises as its own.
func owns(n *oracleNode, addr netip.Addr) bool {
	for _, p := range n.Advertise {
		if prefix, err := netip.ParsePrefix(p); err == nil && prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// lookup returns the next hop of the longest active route that holds the address; among
// equal lengths the lowest metric wins.
func lookup(n *oracleNode, addr netip.Addr) (string, bool) {
	best, bestLen, bestMetric := "", -1, 0
	for _, r := range n.Routes {
		prefix, err := netip.ParsePrefix(r.Prefix)
		if err != nil || !r.Active || !prefix.Contains(addr) {
			continue
		}
		if prefix.Bits() > bestLen || (prefix.Bits() == bestLen && r.Metric < bestMetric) {
			best, bestLen, bestMetric = r.Via, prefix.Bits(), r.Metric
		}
	}
	return best, bestLen >= 0
}

// allowed reports whether the node has a peer by that name whose allowed prefixes hold the
// address.
func allowed(n *oracleNode, peer string, addr netip.Addr) bool {
	for _, p := range n.Peers {
		if p.Node != peer {
			continue
		}
		for _, text := range p.AllowedPrefixes {
			if prefix, err := netip.ParsePrefix(text); err == nil && prefix.Contains(addr) {
				return true
			}
		}
	}
	return false
}

// ExpectOracleOK fails the test unless the oracle delivers every ordered pair of nodes in
// both directions, with the return path the exact reverse of the forward path. Pairs that
// involve a node in skip are not checked.
//
// Example:
//
//	w.ExpectOracleOK(out)            // every pair
//	w.ExpectOracleOK(out, "builder-1") // everything except builder-1
func (w *W) ExpectOracleOK(o *Out, skip ...string) {
	w.T.Helper()
	pairs, err := o.Oracle()
	if err != nil {
		w.Fatalf("oracle %s: %v", o.Name, err)
	}
	for key, pair := range pairs {
		if slices.Contains(skip, key[0]) || slices.Contains(skip, key[1]) {
			continue
		}
		switch {
		case !pair.Forward.Delivered:
			w.Fatalf("oracle %s: %s -> %s: %s", o.Name, key[0], key[1], pair.Forward.Problem)
		case !pair.Return.Delivered:
			w.Fatalf("oracle %s: %s -> %s return: %s", o.Name, key[0], key[1], pair.Return.Problem)
		case !pair.Symmetric:
			w.Fatalf("oracle %s: %s <-> %s: return %v is not the reverse of %v", o.Name, key[0], key[1], pair.Return.Hops, pair.Forward.Hops)
		}
	}
}

// OraclePath returns the oracle's forward path between two nodes, failing the test when the
// configs cannot be walked.
//
// Example:
//
//	path := w.OraclePath(out, "builder-1", "dgx-spark")
func (w *W) OraclePath(o *Out, src, dst string) Path {
	w.T.Helper()
	pairs, err := o.Oracle()
	if err != nil {
		w.Fatalf("oracle %s: %v", o.Name, err)
	}
	return pairs[[2]string{src, dst}].Forward
}
