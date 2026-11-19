package ipam

import (
	"fmt"
	"net/netip"

	"github.com/vectorihub/netctl/cidr"
)

// Accounting proves where every address in the supernet is: allocated,
// reserved, quarantined or free, each exactly once.
//
// It adds up the four kinds, finds the free space with cidr.Subtract, and
// checks that allocated + reserved + quarantined + free equals the size of the
// supernet. Anything that does not hold goes into Problems as text. On a
// registry built by New and changed only through its methods, Problems is
// always empty.
//
// Example: on a fresh registry over 10.42.0.0/16 Free is 65,536, everything else is 0 and
// FreeBlocks is [10.42.0.0/16].
func (registry *IPAM) Accounting() Report {
	registry.expire()
	report := Report{
		Supernet:   registry.supernet,
		Total:      cidr.Size(registry.supernet),
		FreeBlocks: []netip.Prefix{},
		Pools:      make([]PoolUsage, len(registry.pools)),
		Problems:   []string{},
	}
	for i, pool := range registry.pools {
		report.Pools[i] = PoolUsage{Name: pool.Name, Prefix: pool.Prefix, Total: cidr.Size(pool.Prefix)}
	}
	problem := func(format string, args ...any) {
		report.Problems = append(report.Problems, fmt.Sprintf(format, args...))
	}

	used := make([]netip.Prefix, 0, len(registry.allocs))
	var reach *Allocation // the allocation seen so far that extends furthest
	for i := range registry.allocs {
		held := &registry.allocs[i]
		size := cidr.Size(held.Prefix)

		// Sorted by start, so an overlap is with whichever earlier block reaches furthest.
		if reach != nil && cidr.Overlaps(reach.Prefix, held.Prefix) {
			problem("overlap: %s (%s) and %s (%s)", reach.Prefix, reach.Owner, held.Prefix, held.Owner)
		}
		if reach == nil || lastAddress(held.Prefix) > lastAddress(reach.Prefix) {
			reach = held
		}
		if !cidr.Contains(registry.supernet, held.Prefix) {
			problem("%s (%s) is outside the supernet %s", held.Prefix, held.Owner, registry.supernet)
		}

		pool := registry.poolIndex(held.Prefix)
		if pool < 0 {
			problem("%s (%s) is outside every pool", held.Prefix, held.Owner)
		}
		switch held.State {
		case Allocated:
			report.Allocated += size
			if pool >= 0 {
				report.Pools[pool].Allocated += size
			}
		case Reserved:
			report.Reserved += size
			if pool >= 0 {
				report.Pools[pool].Reserved += size
			}
		case Quarantined:
			report.Quarantined += size
			if pool >= 0 {
				report.Pools[pool].Quarantined += size
			}
		default:
			problem("%s (%s) has unknown state %q", held.Prefix, held.Owner, held.State)
		}
		used = append(used, held.Prefix)
	}

	report.FreeBlocks = cidr.Subtract(registry.supernet, used)
	for _, block := range report.FreeBlocks {
		report.Free += cidr.Size(block)
	}
	for i := range report.Pools {
		usage := &report.Pools[i]
		taken := usage.Allocated + usage.Reserved + usage.Quarantined
		if taken > usage.Total {
			problem("pool %s holds %d addresses but has only %d", usage.Name, taken, usage.Total)
			continue
		}
		usage.Free = usage.Total - taken
	}
	if sum := report.Allocated + report.Reserved + report.Quarantined + report.Free; sum != report.Total {
		problem("accounting does not add up: allocated %d + reserved %d + quarantined %d + free %d = %d, supernet has %d",
			report.Allocated, report.Reserved, report.Quarantined, report.Free, sum, report.Total)
	}
	return report
}

// lastAddress returns the number of prefix's last address, for comparing how far
// two blocks reach.
func lastAddress(prefix netip.Prefix) uint64 {
	return addrNumber(prefix.Masked().Addr()) + cidr.Size(prefix) - 1
}

// poolIndex returns the index of the pool containing prefix, or -1.
func (registry *IPAM) poolIndex(prefix netip.Prefix) int {
	for i := range registry.pools {
		if cidr.Contains(registry.pools[i].Prefix, prefix) {
			return i
		}
	}
	return -1
}
