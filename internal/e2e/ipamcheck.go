package e2e

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
)

// ipamFile is what the checker reads of ipam.json. Like the oracle it uses no netctl
// package, so it checks the saved file rather than the library's idea of it.
type ipamFile struct {
	Supernet string `json:"supernet"`
	Pools    []struct {
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	} `json:"pools"`
	Allocations []struct {
		Prefix string `json:"prefix"`
		Pool   string `json:"pool"`
		Owner  string `json:"owner"`
		State  string `json:"state"`
	} `json:"allocations"`
}

// IPAMCounts are the totals the checker works out for itself, in addresses.
type IPAMCounts struct {
	// Total is the size of the supernet.
	Total uint64
	// Allocated is the size of the allocated blocks.
	Allocated uint64
	// Reserved is the size of the reserved blocks.
	Reserved uint64
	// Quarantined is the size of the quarantined blocks.
	Quarantined uint64
	// Free is what is left.
	Free uint64
}

// size is the number of addresses in a prefix.
func size(p netip.Prefix) uint64 { return uint64(1) << (p.Addr().BitLen() - p.Bits()) }

// CheckIPAM reads an ipam.json and verifies, independently of netctl, that every allocation
// lies inside the supernet and inside its pool, that no two allocations overlap, and that
// the sizes add up to the supernet. It returns the problems found (none when the file is
// sound) and the counts it worked out.
//
// Example:
//
//	problems, counts := e2e.CheckIPAM(out.Files["ipam.json"])
func CheckIPAM(data []byte) ([]string, IPAMCounts) {
	var f ipamFile
	var counts IPAMCounts
	if err := json.Unmarshal(data, &f); err != nil {
		return []string{"not JSON: " + err.Error()}, counts
	}
	super, err := netip.ParsePrefix(f.Supernet)
	if err != nil {
		return []string{"bad supernet " + f.Supernet}, counts
	}
	pools := map[string]netip.Prefix{}
	for _, p := range f.Pools {
		if prefix, err := netip.ParsePrefix(p.Prefix); err == nil {
			pools[p.Name] = prefix
		}
	}
	var problems []string
	type block struct {
		prefix netip.Prefix
		owner  string
	}
	var blocks []block
	counts.Total = size(super)
	for _, a := range f.Allocations {
		prefix, err := netip.ParsePrefix(a.Prefix)
		if err != nil {
			problems = append(problems, "bad prefix "+a.Prefix)
			continue
		}
		if !contains(super, prefix) {
			problems = append(problems, fmt.Sprintf("%s (%s) is outside the supernet %s", a.Prefix, a.Owner, super))
		}
		if pool, ok := pools[a.Pool]; !ok || !contains(pool, prefix) {
			problems = append(problems, fmt.Sprintf("%s (%s) is outside its pool %q", a.Prefix, a.Owner, a.Pool))
		}
		switch a.State {
		case "allocated":
			counts.Allocated += size(prefix)
		case "reserved":
			counts.Reserved += size(prefix)
		case "quarantined":
			counts.Quarantined += size(prefix)
		default:
			problems = append(problems, fmt.Sprintf("%s has unknown state %q", a.Prefix, a.State))
		}
		blocks = append(blocks, block{prefix, a.Owner})
	}
	slices.SortFunc(blocks, func(a, b block) int {
		if c := a.prefix.Addr().Compare(b.prefix.Addr()); c != 0 {
			return c
		}
		return a.prefix.Bits() - b.prefix.Bits()
	})
	for i := 0; i < len(blocks); i++ {
		_, end := span(blocks[i].prefix)
		for j := i + 1; j < len(blocks); j++ {
			if start, _ := span(blocks[j].prefix); start >= end {
				break
			}
			problems = append(problems, fmt.Sprintf("%s (%s) overlaps %s (%s)", blocks[i].prefix, blocks[i].owner, blocks[j].prefix, blocks[j].owner))
		}
	}
	used := counts.Allocated + counts.Reserved + counts.Quarantined
	if used > counts.Total {
		problems = append(problems, fmt.Sprintf("allocations hold %d addresses, the supernet only %d", used, counts.Total))
	} else {
		counts.Free = counts.Total - used
	}
	return problems, counts
}

// contains reports whether outer holds all of inner.
func contains(outer, inner netip.Prefix) bool {
	return outer.Bits() <= inner.Bits() && outer.Contains(inner.Addr())
}

// span returns the first and one-past-last address of an IPv4 prefix as numbers, which is
// all the address plan uses.
func span(p netip.Prefix) (uint64, uint64) {
	a := p.Masked().Addr().As4()
	start := uint64(a[0])<<24 | uint64(a[1])<<16 | uint64(a[2])<<8 | uint64(a[3])
	return start, start + size(p)
}

// ExpectIPAMOK fails the test unless the checker finds the plan's ipam.json sound and its
// counts agree with what the ipam package reports for the same file.
func (w *W) ExpectIPAMOK(o *Out) {
	w.T.Helper()
	problems, counts := CheckIPAM(o.Files["ipam.json"])
	if len(problems) > 0 {
		w.Fatalf("%s: ipam.json: %v", o.Name, problems)
	}
	rep := w.Accounting(o)
	if rep.Total != counts.Total || rep.Allocated != counts.Allocated || rep.Reserved != counts.Reserved || rep.Quarantined != counts.Quarantined || rep.Free != counts.Free {
		w.Fatalf("%s: the checker counts %+v, ipam reports total %d allocated %d reserved %d quarantined %d free %d", o.Name, counts, rep.Total, rep.Allocated, rep.Reserved, rep.Quarantined, rep.Free)
	}
	if len(rep.Problems) > 0 {
		w.Fatalf("%s: ipam reports problems: %v", o.Name, rep.Problems)
	}
}
