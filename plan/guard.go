package plan

import (
	"github.com/vectorihub/netctl/types"
)

// guard decides what to do with a plan the validator refused. If a plan is in force, the
// previous result holds configs (a refusal that kept a plan also holds them, so a second
// refusal in a row keeps it too, whatever the last report said), the plan in force stays:
// the result carries its configs, tables, ID and address plan, a FailoverRejected decision
// with the errors, and the new validation report so the caller can see why. If nothing was
// in force there is nothing to keep, and the result holds no configs. Either way the error
// is VALIDATION_FAILED and a caller that writes files writes none.
func guard(prev *Result, next Result) (Result, error) {
	err := next.Validation.Err()
	if prev == nil || len(prev.Configs) == 0 {
		return next, err
	}
	next.Decisions = append(next.Decisions, Decision{
		Kind:     "FailoverRejected",
		Reason:   "the new plan fails validation; keeping " + prev.PlanID,
		Findings: next.Validation.Errors(),
	})
	// The plan in force is the whole of what stays: its configs, tables and ID, and its
	// address plan. The rejected plan's allocations and releases never happened, so a
	// caller that saves the snapshot cannot lose an address the kept configs still use.
	next.PlanID, next.Configs, next.Tables, next.Unreachable = prev.PlanID, prev.Configs, prev.Tables, prev.Unreachable
	next.IPAM, next.Changes = prev.IPAM, nil
	return next, types.Errf(types.ErrValidationFailed, "plan", "the new plan was rejected and %s stays in force: %v", prev.PlanID, err)
}
