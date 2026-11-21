package e2e

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/vectorihub/netctl/internal/ptimport"
	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/topology"
)

// ForPacketTracer adapts a plan to the importer's comparison: its rendered files, a resolver
// over its address plan, the node each site's hosts stand for (the first node of the site),
// the site blocks, and the oracle's path between two nodes.
//
// Example:
//
//	problems := ptimport.CompareRoutes(obs, "", out.ForPacketTracer(topo, obs.Mapping))
func (o *Out) ForPacketTracer(topo *topology.Topology, mapping ptimport.Mapping) ptimport.Plan {
	siteNodes := map[string]string{}
	for _, n := range topo.Nodes {
		if _, ok := siteNodes[string(n.Site)]; !ok {
			siteNodes[string(n.Site)] = string(n.ID)
		}
	}
	var sites []netip.Prefix
	for _, a := range o.Result.IPAM.Allocations {
		if a.Owner.Kind == ipam.KindSite && a.State == ipam.Allocated {
			sites = append(sites, a.Prefix)
		}
	}
	return ptimport.Plan{
		Files:        o.Files,
		Resolver:     ptimport.NewResolver(o.Result.IPAM, mapping),
		SiteNodes:    siteNodes,
		SitePrefixes: sites,
		Path: func(from, to string) ([]string, error) {
			pairs, err := o.Oracle()
			if err != nil {
				return nil, err
			}
			pair, ok := pairs[[2]string{from, to}]
			if !ok {
				return nil, fmt.Errorf("no path between %s and %s", from, to)
			}
			if !pair.Forward.Delivered {
				return nil, fmt.Errorf("%s", strings.TrimSpace(pair.Forward.Problem))
			}
			return pair.Forward.Hops, nil
		},
	}
}
