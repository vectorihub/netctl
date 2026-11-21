package ptimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"sort"
)

// bytesReader wraps bytes for the YAML decoder.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// planRoute is the part of a rendered node config the comparison reads.
type planRoute struct {
	Prefix string `json:"prefix"`
	Via    string `json:"via"`
	Active bool   `json:"active"`
	Backup bool   `json:"backup"`
}

// planNode is a rendered node config as far as the comparison needs.
type planNode struct {
	Advertise []string    `json:"advertise"`
	Routes    []planRoute `json:"routes"`
}

// Plan is what a comparison needs of a plan: its rendered files and its address plan. The
// caller fills it from a plan result, which keeps this package free of the planner.
type Plan struct {
	// Files are the rendered files of the plan, by path ("nodes/aws-hub.json").
	Files map[string][]byte
	// Resolver resolves addresses to nodes.
	Resolver Resolver
	// SiteNodes maps a site ID to the node a host in that site stands for.
	SiteNodes map[string]string
	// SitePrefixes are the site blocks of the plan; routes to them are the ones compared.
	SitePrefixes []netip.Prefix
	// Path returns the plan's node-by-node path between two nodes, for traceroutes.
	Path func(from, to string) ([]string, error)
}

// CompareRoutes checks a plan's active routes against an observed lab state, with the
// plan's match rules: every observed static route that is active must be an active route of
// the plan with the same next hop; every observed backup must be an inactive backup of the
// plan; and the plan must hold no active route to a site block the lab did not show.
// Metrics are not compared. It returns the mismatches, empty when the plan matches.
//
// Example:
//
//	problems := ptimport.CompareRoutes(obs, "", plan)
func CompareRoutes(obs Observation, state string, plan Plan) []string {
	var problems []string
	nodes := map[string]planNode{}
	for path, data := range plan.Files {
		var n planNode
		if len(path) > 6 && path[:6] == "nodes/" && json.Unmarshal(data, &n) == nil {
			nodes[path[6:len(path)-5]] = n
		}
	}
	for _, d := range obs.Devices {
		if d.State != state {
			continue
		}
		node, ok := nodes[d.MapsTo]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s maps to %s, which the plan has no config for", d.Device, d.MapsTo))
			continue
		}
		seen := map[string]bool{}
		for _, r := range d.ShowIPRoute {
			if r.Code == "C" || r.Via == "connected" {
				continue
			}
			via, ok := plan.Resolver.Node(r.Via)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: next hop %s of %s is not an address the plan or the mapping knows", d.Device, r.Via, r.Prefix))
				continue
			}
			key := r.Prefix.String()
			if !r.Backup {
				seen[key] = true
			}
			if !hasRoute(node, key, via, !r.Backup) {
				kind := "active route"
				if r.Backup {
					kind = "backup route"
				}
				problems = append(problems, fmt.Sprintf("%s (%s): lab shows %s via %s, the plan has none", d.Device, d.MapsTo, kind+" to "+key, via))
			}
		}
		for _, p := range plan.SitePrefixes {
			if ownsPrefix(node, p) {
				continue
			}
			for _, r := range node.Routes {
				if r.Active && r.Prefix == p.String() && !seen[r.Prefix] {
					problems = append(problems, fmt.Sprintf("%s (%s): the plan routes %s via %s, the lab shows no such route", d.Device, d.MapsTo, r.Prefix, r.Via))
				}
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// hasRoute reports whether the node has a route to the prefix via the next hop, active or a
// backup as asked. A backup must not be active.
func hasRoute(n planNode, prefix, via string, active bool) bool {
	for _, r := range n.Routes {
		if r.Prefix != prefix || r.Via != via {
			continue
		}
		if active && r.Active {
			return true
		}
		if !active && !r.Active {
			return true
		}
	}
	return false
}

// ownsPrefix reports whether the node advertises the prefix as its own.
func ownsPrefix(n planNode, p netip.Prefix) bool {
	return slices.Contains(n.Advertise, p.String())
}

// ComparePaths checks every observed traceroute against the plan's path between the same
// nodes. The lab's hop addresses become nodes through the resolver; the final hop, a host,
// becomes the node of its site; consecutive repeats are merged; the starting host's node is
// put in front. The plan's path must equal that sequence.
func ComparePaths(obs Observation, state string, plan Plan) []string {
	var problems []string
	for _, d := range obs.Devices {
		for _, t := range d.Traceroutes {
			if t.State != state {
				continue
			}
			src, ok := obs.Mapping.Hosts[t.From]
			if !ok {
				src = d.MapsTo
			}
			var want []string
			want = append(want, src)
			bad := false
			for i, hop := range t.Hops {
				node, ok := plan.Resolver.Node(hop)
				if !ok && i == len(t.Hops)-1 {
					node, ok = plan.Resolver.SiteNode(hop, plan.SiteNodes)
				}
				if !ok {
					problems = append(problems, fmt.Sprintf("%s traceroute %s -> %s: hop %s is unmapped", d.Device, t.From, t.To, hop))
					bad = true
					break
				}
				if want[len(want)-1] != node {
					want = append(want, node)
				}
			}
			if bad {
				continue
			}
			dst := want[len(want)-1]
			got, err := plan.Path(src, dst)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s traceroute %s -> %s: %v", d.Device, t.From, t.To, err))
				continue
			}
			if !slices.Equal(got, want) {
				problems = append(problems, fmt.Sprintf("%s traceroute %s -> %s: lab path %v, plan path %v", d.Device, t.From, t.To, want, got))
			}
		}
	}
	return problems
}
