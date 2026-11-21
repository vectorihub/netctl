package plan

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/vectorihub/netctl/ipam"
)

// diff lists what changed in the address plan between two snapshots: new allocations,
// allocations given back (quarantined) and quarantines that ended. An address never moves:
// the registry gives an owner the block it has, so a change of address is a release plus an
// allocation, which is how a rename shows up. The list is sorted by prefix, so it reads the same however the
// plan was made.
func diff(before, after ipam.Snapshot) []Change {
	held := map[netip.Prefix]ipam.Allocation{}
	for _, a := range before.Allocations {
		held[a.Prefix] = a
	}
	now := map[netip.Prefix]ipam.Allocation{}
	var out []Change
	for _, a := range after.Allocations {
		now[a.Prefix] = a
		was, had := held[a.Prefix]
		switch {
		case !had:
			out = append(out, Change{Kind: "allocated", Subject: a.Owner.String(), Prefix: a.Prefix})
		case was.State != ipam.Quarantined && a.State == ipam.Quarantined:
			out = append(out, Change{Kind: "released", Subject: a.Owner.String(), Prefix: a.Prefix})
		}
	}
	for _, a := range before.Allocations {
		if _, still := now[a.Prefix]; !still && a.State == ipam.Quarantined {
			out = append(out, Change{Kind: "expired", Subject: a.Owner.String(), Prefix: a.Prefix})
		}
	}
	slices.SortFunc(out, func(a, b Change) int {
		if c := a.Prefix.Addr().Compare(b.Prefix.Addr()); c != 0 {
			return c
		}
		if a.Prefix.Bits() != b.Prefix.Bits() {
			return a.Prefix.Bits() - b.Prefix.Bits()
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	return out
}

// renames explains the case that looks like a rename. netctl cannot tell a renamed machine
// from one that left while another joined, and it must never move an address silently, so it
// treats both as a removal plus an addition. When a plan does both, this says so: for each
// removed machine, in sorted order, paired with the added machine in sorted order, it records
// that the old address is quarantined and the new name has its own.
func renames(changes []Change, quarantine time.Duration) []Decision {
	var gone, joined []Change
	for _, c := range changes {
		if !strings.HasPrefix(c.Subject, "node:") {
			continue
		}
		switch c.Kind {
		case "released":
			gone = append(gone, c)
		case "allocated":
			joined = append(joined, c)
		}
	}
	slices.SortFunc(gone, func(a, b Change) int { return strings.Compare(a.Subject, b.Subject) })
	slices.SortFunc(joined, func(a, b Change) int { return strings.Compare(a.Subject, b.Subject) })
	var out []Decision
	for i := 0; i < len(gone) && i < len(joined); i++ {
		out = append(out, Decision{
			Kind: "AddressNotCarried",
			Reason: fmt.Sprintf("%s left and %s joined in the same plan; they are two machines to netctl, so %s is quarantined for %s and %s has its own address %s. Keep the machine's ID to keep its address",
				gone[i].Subject, joined[i].Subject, gone[i].Prefix, quarantine, joined[i].Subject, joined[i].Prefix),
		})
	}
	return out
}
