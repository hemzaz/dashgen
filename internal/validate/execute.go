package validate

import (
	"context"
	"errors"
)

// executeOutcome is a lightweight value returned from the execution stage.
// refused=true short-circuits to a VerdictRefuse; warnings are folded into
// the downstream warning set.
type executeOutcome struct {
	refused  bool
	reason   string
	warnings []string
}

// runExecute performs a bounded instant query. It enforces the per-query
// timeout and the per-run budget; both are deterministic policy knobs.
//
// Budget accounting: every attempted call increments the spent counter
// before the call is made. Once the counter exceeds TotalBudget, further
// candidates are refused with ReasonBudgetExhausted without touching the
// backend.
//
// Strict mode contract: transient failures (timeouts and other errors) are
// treated as refusals. SPECS §8 forbids passing weak queries on the silent
// hope that the backend recovers.
func (p *Pipeline) runExecute(ctx context.Context, expr string) executeOutcome {
	// Budget check comes first so exhausted runs never touch the backend.
	used := p.spent.Add(1)
	if int(used) > p.opts.TotalBudget {
		return executeOutcome{refused: true, reason: ReasonBudgetExhausted}
	}

	callCtx, cancel := context.WithTimeout(ctx, p.opts.PerQueryTimeout)
	defer cancel()

	result, err := p.client.InstantQuery(callCtx, expr)
	if err != nil {
		if isTimeout(callCtx, err) {
			return executeOutcome{refused: true, reason: ReasonExecutionTimeout + ": " + err.Error()}
		}
		return executeOutcome{refused: true, reason: ReasonExecutionError + ": " + err.Error()}
	}

	var warnings []string
	if result != nil {
		if result.NumSeries == 0 {
			warnings = append(warnings, WarningEmptyResult)
		}
		if len(result.Warnings) > 0 {
			warnings = append(warnings, WarningBackendWarning)
		}
	}
	return executeOutcome{warnings: warnings}
}

// isTimeout distinguishes the deadline-exceeded class of failures from
// other errors so the refusal reason is accurate.
func isTimeout(ctx context.Context, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ctx.Err() == context.DeadlineExceeded {
		return true
	}
	return false
}
