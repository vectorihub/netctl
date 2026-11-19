package ipam

import (
	"net/netip"
	"time"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// DefaultQuarantine is the recommended quarantine period (R8, ADR 0004). New
// takes the period as an argument; callers pass this unless they have a reason
// not to.
const DefaultQuarantine = 24 * time.Hour

// Release gives owner's prefix back. The prefix is quarantined, not freed: it
// stays taken until the quarantine period (given to New) has passed on the
// injected clock, so stale routes and caches that still point at the old owner
// can never reach a new one (R8). A period of zero frees it at once.
//
// Errors:
//   - NOT_FOUND: owner does not hold exactly this prefix, or it is already
//     quarantined. Nothing changes (R9).
//   - INVALID_TOPOLOGY: owner is a site and a node or link still holds addresses
//     inside the block. The error lists every such holder (R10).
//
// Example: after Release(10.42.240.1/32, node:a) with a 24-hour quarantine, an
// Allocate for node:b gets 10.42.240.2/32; once 24 hours have passed an Allocate
// for node:c gets 10.42.240.1/32 again.
func (registry *IPAM) Release(prefix netip.Prefix, owner Owner) error {
	if err := owner.validate(); err != nil {
		return err
	}
	registry.expire()
	masked := prefix.Masked()
	for i := range registry.allocs {
		held := &registry.allocs[i]
		if held.Prefix != masked || held.Owner != owner || held.State == Quarantined {
			continue
		}
		if owner.Kind == KindSite {
			if err := registry.siteInUse(held.Prefix); err != nil {
				return err
			}
		}
		held.State = Quarantined
		held.QuarantineUntil = registry.clock.Now().Add(registry.quarantine)
		return nil
	}
	return types.Errf(types.ErrNotFound, masked.String(), "%s does not hold this prefix", owner)
}

// siteInUse returns INVALID_TOPOLOGY listing every non-site holder inside block.
func (registry *IPAM) siteInUse(block netip.Prefix) error {
	var problems types.List
	for _, inside := range registry.allocs {
		if inside.Owner.Kind != KindSite && inside.State != Quarantined && cidr.Contains(block, inside.Prefix) {
			problems.Add(types.Errf(types.ErrInvalidTopology, block.String(), "site block still contains %s (%s)", inside.Prefix, inside.Owner))
		}
	}
	return problems.Err()
}
