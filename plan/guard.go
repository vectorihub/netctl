package plan

import (
	"github.com/vectorihub/netctl/types"
)

// guard decides what to do with a plan the validator refused. A refused plan is "worse"
// than the plan in force when it fails validation and the plan in force passed. Then the
// plan in force stays: the result carries its configs, tables and ID, a FailoverRejected
// decision with the errors, and the new validation report so the caller can see why. If
// nothing was in force, or what was in force failed too, there is nothing to keep, and the
// result holds no configs. Either way the error is VALIDATION_FAILED and a caller that
// writes files writes none.
func guard(prev *Result, next Result) (Result, error) {
	err := next.Validation.Err()
	if prev == nil || !prev.Validation.OK {
		return next, err
	}
	next.Decisions = append(next.Decisions, Decision{
		Kind:     "FailoverRejected",
		Reason:   "the new plan fails validation; keeping " + prev.PlanID,
		Findings: next.Validation.Errors(),
	})
	next.PlanID, next.Configs, next.Tables, next.Unreachable = prev.PlanID, prev.Configs, prev.Tables, prev.Unreachable
	return next, types.Errf(types.ErrValidationFailed, "plan", "the new plan was rejected and %s stays in force: %v", prev.PlanID, err)
}
