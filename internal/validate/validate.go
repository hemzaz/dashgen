// Package validate owns the staged query validation pipeline described in
// SPECS §8: parse → selector → execute → safety → verdict.
//
// The Pipeline is deterministic: the same input always yields an identical
// ValidationResult. Pipelines are stateless apart from a shared run budget
// counter that is decremented as backend calls are spent.
package validate

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	"dashgen/internal/ir"
	"dashgen/internal/prometheus"
	"dashgen/internal/safety"
)

// Stage identifies which phase of the pipeline produced a verdict.
type Stage string

const (
	StageParse    Stage = "parse"
	StageSelector Stage = "selector"
	StageExecute  Stage = "execute"
	StageSafety   Stage = "safety"
	StageVerdict  Stage = "verdict"
)

// Refusal reasons and warning codes emitted by the pipeline.
const (
	ReasonParseError           = "parse_error"
	ReasonSelectorError        = "selector_error"
	ReasonExecutionTimeout     = "execution_timeout"
	ReasonExecutionError       = "execution_error"
	ReasonBudgetExhausted      = "budget_exhausted"
	ReasonBannedLabelGrouping  = "banned_label_in_grouping"
	ReasonBannedLabelMatcher   = "banned_label_in_matcher"
	ReasonStrictWarningUpgrade = "strict_warning_upgrade"
	WarningEmptyResult         = "empty_result"
	WarningBackendWarning      = "backend_warning"
)

// ValidationResult is what each Pipeline.Validate call returns.
type ValidationResult struct {
	Verdict       ir.Verdict
	WarningCodes  []string
	RefusalReason string
	FailedStage   Stage
}

// Options configure the Pipeline. Zero values fall back to safe defaults.
type Options struct {
	// PerQueryTimeout bounds every InstantQuery. Defaults to 3s.
	PerQueryTimeout time.Duration
	// TotalBudget caps the number of backend calls per run. Defaults to 200.
	// Once exhausted, remaining Validate calls return
	// VerdictRefuse/ReasonBudgetExhausted without hitting the backend.
	TotalBudget int
	// Strict promotes any accept_with_warning to refuse during verdict
	// composition. See SPECS §8.
	Strict bool
}

const (
	defaultPerQueryTimeout = 3 * time.Second
	defaultTotalBudget     = 200
)

// Pipeline is the concrete validation engine. Construct with New.
type Pipeline struct {
	client prometheus.Client
	policy *safety.Policy
	opts   Options
	spent  atomic.Int64 // number of backend calls used so far
}

// New wires a Pipeline. The client and policy must be non-nil.
func New(client prometheus.Client, policy *safety.Policy, opts Options) *Pipeline {
	if opts.PerQueryTimeout <= 0 {
		opts.PerQueryTimeout = defaultPerQueryTimeout
	}
	if opts.TotalBudget <= 0 {
		opts.TotalBudget = defaultTotalBudget
	}
	return &Pipeline{client: client, policy: policy, opts: opts}
}

// Validate runs the 5-stage pipeline against a single candidate.
//
// Stage boundaries are observable via ValidationResult.FailedStage:
//   - StageParse on syntax errors
//   - StageSelector on matcher or banned-label problems
//   - StageExecute on backend timeouts, errors, or budget exhaustion
//   - StageSafety on grouping refusals
//   - StageVerdict for all successful accepts or warning compositions,
//     including strict-mode refusals that promote a prior warning.
func (p *Pipeline) Validate(ctx context.Context, q *ir.QueryCandidate) *ValidationResult {
	if q == nil {
		return &ValidationResult{
			Verdict:       ir.VerdictRefuse,
			RefusalReason: ReasonParseError + ": nil candidate",
			FailedStage:   StageParse,
		}
	}

	// Stage 1: parse.
	parsed, perr := parseExpr(q.Expr)
	if perr != nil {
		return &ValidationResult{
			Verdict:       ir.VerdictRefuse,
			RefusalReason: ReasonParseError + ": " + perr.Error(),
			FailedStage:   StageParse,
		}
	}

	// Stage 2: selector sanity.
	if serr := checkSelectors(parsed, p.policy); serr != nil {
		reason := ReasonSelectorError + ": " + serr.reason
		if serr.banned {
			reason = ReasonBannedLabelMatcher + ": " + serr.reason
		}
		return &ValidationResult{
			Verdict:       ir.VerdictRefuse,
			RefusalReason: reason,
			FailedStage:   StageSelector,
		}
	}

	warnings := map[string]struct{}{}

	// Stage 3: bounded backend execution.
	execOutcome := p.runExecute(ctx, q.Expr)
	if execOutcome.refused {
		return &ValidationResult{
			Verdict:       ir.VerdictRefuse,
			RefusalReason: execOutcome.reason,
			FailedStage:   StageExecute,
		}
	}
	for _, w := range execOutcome.warnings {
		warnings[w] = struct{}{}
	}

	// Stage 4: safety and cardinality evaluation.
	safetyOutcome := evaluateSafety(parsed, p.policy)
	if safetyOutcome.refused {
		return &ValidationResult{
			Verdict:       ir.VerdictRefuse,
			RefusalReason: safetyOutcome.reason,
			FailedStage:   StageSafety,
		}
	}
	for _, w := range safetyOutcome.warnings {
		warnings[w] = struct{}{}
	}

	// Stage 5: verdict composition.
	codes := sortedKeys(warnings)
	verdict := ir.VerdictAccept
	if len(codes) > 0 {
		verdict = ir.VerdictAcceptWithWarning
	}
	if p.opts.Strict && verdict == ir.VerdictAcceptWithWarning {
		return &ValidationResult{
			Verdict:       ir.VerdictRefuse,
			RefusalReason: ReasonStrictWarningUpgrade + ": " + joinCodes(codes),
			WarningCodes:  codes,
			FailedStage:   StageVerdict,
		}
	}

	return &ValidationResult{
		Verdict:      verdict,
		WarningCodes: codes,
		FailedStage:  StageVerdict,
	}
}

// BudgetUsed returns the number of backend calls the pipeline has spent so
// far. Intended for tests and the `inspect` command; never mutated by
// callers.
func (p *Pipeline) BudgetUsed() int {
	return int(p.spent.Load())
}

// sortedKeys returns the set's members in ascending order for deterministic
// output.
func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinCodes(codes []string) string {
	switch len(codes) {
	case 0:
		return ""
	case 1:
		return codes[0]
	}
	n := len(codes) - 1
	for _, c := range codes {
		n += len(c)
	}
	buf := make([]byte, 0, n)
	for i, c := range codes {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, c...)
	}
	return string(buf)
}
