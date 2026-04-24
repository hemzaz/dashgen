package validate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"dashgen/internal/ir"
	"dashgen/internal/prometheus"
	"dashgen/internal/safety"
)

// fakeClient is a prometheus.Client double. Only InstantQuery is
// exercised; the other methods panic if a test accidentally depends on
// them.
type fakeClient struct {
	result     *prometheus.QueryResult
	err        error
	calls      int
	blockUntil chan struct{}
	sleep      time.Duration
}

func (f *fakeClient) Metadata(context.Context) (map[string][]prometheus.MetricMetadata, error) {
	panic("fakeClient.Metadata not implemented")
}
func (f *fakeClient) LabelNames(context.Context, string) ([]string, error) {
	panic("fakeClient.LabelNames not implemented")
}
func (f *fakeClient) Series(context.Context, []string) ([]map[string]string, error) {
	panic("fakeClient.Series not implemented")
}
func (f *fakeClient) InstantQuery(ctx context.Context, _ string) (*prometheus.QueryResult, error) {
	f.calls++
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.blockUntil != nil {
		select {
		case <-f.blockUntil:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// okClient returns a single-series result with no warnings.
func okClient() *fakeClient {
	return &fakeClient{result: &prometheus.QueryResult{ResultType: "vector", NumSeries: 1}}
}

// emptyClient returns a zero-series result with no warnings.
func emptyClient() *fakeClient {
	return &fakeClient{result: &prometheus.QueryResult{ResultType: "vector", NumSeries: 0}}
}

func newPipeline(t *testing.T, client prometheus.Client, opts Options) *Pipeline {
	t.Helper()
	return New(client, safety.NewPolicy(nil), opts)
}

func TestParseStage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		expr    string
		verdict ir.Verdict
		stage   Stage
		reason  string
	}{
		{
			name:    "valid",
			expr:    `up{job="api"}`,
			verdict: ir.VerdictAccept,
			stage:   StageVerdict,
		},
		{
			name:    "empty",
			expr:    "",
			verdict: ir.VerdictRefuse,
			stage:   StageParse,
			reason:  ReasonParseError,
		},
		{
			name:    "unclosed_paren",
			expr:    `sum(rate(http_requests_total[5m])`,
			verdict: ir.VerdictRefuse,
			stage:   StageParse,
			reason:  ReasonParseError,
		},
		{
			name:    "unknown_function",
			expr:    `notafunc(http_requests_total)`,
			verdict: ir.VerdictRefuse,
			stage:   StageParse,
			reason:  ReasonParseError,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPipeline(t, okClient(), Options{})
			got := p.Validate(context.Background(), &ir.QueryCandidate{Expr: c.expr})
			if got.Verdict != c.verdict {
				t.Fatalf("verdict=%v want=%v (result=%+v)", got.Verdict, c.verdict, got)
			}
			if got.FailedStage != c.stage {
				t.Fatalf("stage=%v want=%v", got.FailedStage, c.stage)
			}
			if c.reason != "" && !strings.HasPrefix(got.RefusalReason, c.reason) {
				t.Fatalf("reason=%q want prefix %q", got.RefusalReason, c.reason)
			}
		})
	}
}

func TestSelectorStage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		expr    string
		verdict ir.Verdict
		stage   Stage
		reason  string
	}{
		{
			name:    "metric_name_only",
			expr:    `up`,
			verdict: ir.VerdictAccept,
			stage:   StageVerdict,
		},
		{
			name:    "label_matcher_only",
			expr:    `{__name__="up"}`,
			verdict: ir.VerdictAccept,
			stage:   StageVerdict,
		},
		{
			name:    "banned_label_matcher",
			expr:    `http_requests_total{user_id="42"}`,
			verdict: ir.VerdictRefuse,
			stage:   StageSelector,
			reason:  ReasonBannedLabelMatcher,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPipeline(t, okClient(), Options{})
			got := p.Validate(context.Background(), &ir.QueryCandidate{Expr: c.expr})
			if got.Verdict != c.verdict {
				t.Fatalf("verdict=%v want=%v result=%+v", got.Verdict, c.verdict, got)
			}
			if got.FailedStage != c.stage {
				t.Fatalf("stage=%v want=%v", got.FailedStage, c.stage)
			}
			if c.reason != "" && !strings.HasPrefix(got.RefusalReason, c.reason) {
				t.Fatalf("reason=%q want prefix %q", got.RefusalReason, c.reason)
			}
		})
	}
}

func TestExecuteStageSuccess(t *testing.T) {
	t.Parallel()
	client := okClient()
	p := newPipeline(t, client, Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{Expr: `up{job="api"}`})
	if got.Verdict != ir.VerdictAccept {
		t.Fatalf("verdict=%v want accept", got.Verdict)
	}
	if client.calls != 1 {
		t.Fatalf("calls=%d want 1", client.calls)
	}
	if got.WarningCodes != nil {
		t.Fatalf("warnings=%v want none", got.WarningCodes)
	}
}

func TestExecuteStageEmptyResult(t *testing.T) {
	t.Parallel()
	p := newPipeline(t, emptyClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{Expr: `up{job="api"}`})
	if got.Verdict != ir.VerdictAcceptWithWarning {
		t.Fatalf("verdict=%v want accept_with_warning", got.Verdict)
	}
	if !reflect.DeepEqual(got.WarningCodes, []string{WarningEmptyResult}) {
		t.Fatalf("warnings=%v want=[%s]", got.WarningCodes, WarningEmptyResult)
	}
}

func TestExecuteStageTimeout(t *testing.T) {
	t.Parallel()
	client := &fakeClient{sleep: 50 * time.Millisecond, result: &prometheus.QueryResult{}}
	p := newPipeline(t, client, Options{PerQueryTimeout: 5 * time.Millisecond})
	got := p.Validate(context.Background(), &ir.QueryCandidate{Expr: `up{job="api"}`})
	if got.Verdict != ir.VerdictRefuse {
		t.Fatalf("verdict=%v want refuse", got.Verdict)
	}
	if got.FailedStage != StageExecute {
		t.Fatalf("stage=%v want execute", got.FailedStage)
	}
	if !strings.HasPrefix(got.RefusalReason, ReasonExecutionTimeout) {
		t.Fatalf("reason=%q want prefix %q", got.RefusalReason, ReasonExecutionTimeout)
	}
}

func TestExecuteStageBackendError(t *testing.T) {
	t.Parallel()
	client := &fakeClient{err: errors.New("boom")}
	p := newPipeline(t, client, Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{Expr: `up{job="api"}`})
	if got.Verdict != ir.VerdictRefuse {
		t.Fatalf("verdict=%v want refuse", got.Verdict)
	}
	if got.FailedStage != StageExecute {
		t.Fatalf("stage=%v want execute", got.FailedStage)
	}
	if !strings.HasPrefix(got.RefusalReason, ReasonExecutionError) {
		t.Fatalf("reason=%q want prefix %q", got.RefusalReason, ReasonExecutionError)
	}
}

func TestExecuteStageBudgetExhausted(t *testing.T) {
	t.Parallel()
	client := okClient()
	p := newPipeline(t, client, Options{TotalBudget: 1})

	first := p.Validate(context.Background(), &ir.QueryCandidate{Expr: `up`})
	if first.Verdict != ir.VerdictAccept {
		t.Fatalf("first verdict=%v want accept", first.Verdict)
	}
	second := p.Validate(context.Background(), &ir.QueryCandidate{Expr: `up{job="api"}`})
	if second.Verdict != ir.VerdictRefuse {
		t.Fatalf("second verdict=%v want refuse", second.Verdict)
	}
	if second.RefusalReason != ReasonBudgetExhausted {
		t.Fatalf("reason=%q want %q", second.RefusalReason, ReasonBudgetExhausted)
	}
	if client.calls != 1 {
		t.Fatalf("client.calls=%d want 1 (budget should skip backend)", client.calls)
	}
	if p.BudgetUsed() != 2 {
		t.Fatalf("BudgetUsed=%d want 2 (attempts counted)", p.BudgetUsed())
	}
}

func TestSafetyStageBannedGrouping(t *testing.T) {
	t.Parallel()
	p := newPipeline(t, okClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{
		Expr: `sum by (user_id) (rate(http_requests_total{job="api"}[5m]))`,
	})
	if got.Verdict != ir.VerdictRefuse {
		t.Fatalf("verdict=%v want refuse result=%+v", got.Verdict, got)
	}
	if got.FailedStage != StageSafety {
		t.Fatalf("stage=%v want safety", got.FailedStage)
	}
	if !strings.HasPrefix(got.RefusalReason, ReasonBannedLabelGrouping) {
		t.Fatalf("reason=%q want prefix %q", got.RefusalReason, ReasonBannedLabelGrouping)
	}
}

func TestSafetyStageHighCardinalityWarning(t *testing.T) {
	t.Parallel()
	p := newPipeline(t, okClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{
		Expr: `sum by (a, b, c, d, e) (rate(http_requests_total{job="api"}[5m]))`,
	})
	if got.Verdict != ir.VerdictAcceptWithWarning {
		t.Fatalf("verdict=%v want warning", got.Verdict)
	}
	if !containsCode(got.WarningCodes, safety.WarningHighCardinalityGrouping) {
		t.Fatalf("warnings=%v want %s", got.WarningCodes, safety.WarningHighCardinalityGrouping)
	}
}

func TestSafetyStageUnscopedAggregation(t *testing.T) {
	t.Parallel()
	p := newPipeline(t, okClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{
		Expr: `sum by (method) (rate(http_requests_total[5m]))`,
	})
	if got.Verdict != ir.VerdictAcceptWithWarning {
		t.Fatalf("verdict=%v want warning", got.Verdict)
	}
	if !containsCode(got.WarningCodes, safety.WarningUnscopedAggregation) {
		t.Fatalf("warnings=%v want %s", got.WarningCodes, safety.WarningUnscopedAggregation)
	}
}

func TestSafetyStageCleanGrouping(t *testing.T) {
	t.Parallel()
	p := newPipeline(t, okClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{
		Expr: `sum by (status) (rate(http_requests_total{job="api"}[5m]))`,
	})
	if got.Verdict != ir.VerdictAccept {
		t.Fatalf("verdict=%v want accept result=%+v", got.Verdict, got)
	}
	if len(got.WarningCodes) != 0 {
		t.Fatalf("warnings=%v want none", got.WarningCodes)
	}
}

func TestVerdictPrecedenceRefuseTrumpsWarn(t *testing.T) {
	t.Parallel()
	// Empty result would warn; banned grouping must trump and refuse.
	p := newPipeline(t, emptyClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{
		Expr: `sum by (user_id) (rate(http_requests_total{job="api"}[5m]))`,
	})
	if got.Verdict != ir.VerdictRefuse {
		t.Fatalf("verdict=%v want refuse", got.Verdict)
	}
	if got.FailedStage != StageSafety {
		t.Fatalf("stage=%v want safety", got.FailedStage)
	}
}

func TestVerdictWarningSortedAndDeduped(t *testing.T) {
	t.Parallel()
	// Trigger both empty_result (execute) and unscoped_aggregation (safety).
	p := newPipeline(t, emptyClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{
		Expr: `sum by (method) (rate(http_requests_total[5m]))`,
	})
	if got.Verdict != ir.VerdictAcceptWithWarning {
		t.Fatalf("verdict=%v want warning result=%+v", got.Verdict, got)
	}
	want := []string{safety.WarningUnscopedAggregation, WarningEmptyResult}
	// Sorted ascending.
	sortedWant := []string{WarningEmptyResult, safety.WarningUnscopedAggregation}
	_ = want
	if !reflect.DeepEqual(got.WarningCodes, sortedWant) {
		t.Fatalf("warnings=%v want %v", got.WarningCodes, sortedWant)
	}
}

func TestVerdictStrictPromotesWarning(t *testing.T) {
	t.Parallel()
	p := newPipeline(t, emptyClient(), Options{Strict: true})
	got := p.Validate(context.Background(), &ir.QueryCandidate{
		Expr: `up{job="api"}`,
	})
	if got.Verdict != ir.VerdictRefuse {
		t.Fatalf("verdict=%v want refuse", got.Verdict)
	}
	if got.FailedStage != StageVerdict {
		t.Fatalf("stage=%v want verdict", got.FailedStage)
	}
	if !strings.HasPrefix(got.RefusalReason, ReasonStrictWarningUpgrade) {
		t.Fatalf("reason=%q want prefix %q", got.RefusalReason, ReasonStrictWarningUpgrade)
	}
	if !containsCode(got.WarningCodes, WarningEmptyResult) {
		t.Fatalf("warnings=%v should preserve %s for diagnostics", got.WarningCodes, WarningEmptyResult)
	}
}

func TestStability(t *testing.T) {
	t.Parallel()
	candidate := &ir.QueryCandidate{
		Expr: `sum by (method, status) (rate(http_requests_total{job="api"}[5m]))`,
	}
	p1 := newPipeline(t, okClient(), Options{})
	p2 := newPipeline(t, okClient(), Options{})
	a := p1.Validate(context.Background(), candidate)
	b := p2.Validate(context.Background(), candidate)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("nondeterministic:\n a=%+v\n b=%+v", a, b)
	}
	// And twice on the same pipeline (new run counts each attempt against
	// budget but the Verdict/WarningCodes must still match).
	p3 := newPipeline(t, okClient(), Options{})
	a3 := p3.Validate(context.Background(), candidate)
	b3 := p3.Validate(context.Background(), candidate)
	if a3.Verdict != b3.Verdict || !reflect.DeepEqual(a3.WarningCodes, b3.WarningCodes) {
		t.Fatalf("result drifted on second call: a=%+v b=%+v", a3, b3)
	}
}

func TestNilCandidateRefuses(t *testing.T) {
	t.Parallel()
	p := newPipeline(t, okClient(), Options{})
	got := p.Validate(context.Background(), nil)
	if got.Verdict != ir.VerdictRefuse {
		t.Fatalf("verdict=%v want refuse", got.Verdict)
	}
	if got.FailedStage != StageParse {
		t.Fatalf("stage=%v want parse", got.FailedStage)
	}
}

func TestSelectorBareBracesAllowed(t *testing.T) {
	t.Parallel()
	// `{method="GET"}` has no metric name and should still validate if
	// the parser accepts it (non-empty matcher). If the parser rejects
	// it outright we fail at parse, not selector, which is also correct.
	p := newPipeline(t, okClient(), Options{})
	got := p.Validate(context.Background(), &ir.QueryCandidate{Expr: `{method="GET"}`})
	if got.Verdict == ir.VerdictRefuse && got.FailedStage != StageParse {
		// A selector-stage refusal is the one outcome we guard against:
		// the parser accepted it, so our sanity check must not then
		// spuriously reject.
		t.Fatalf("unexpected selector-stage refusal: %+v", got)
	}
}

func containsCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}
