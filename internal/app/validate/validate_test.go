package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dashgen/internal/app/generate"
	"dashgen/internal/config"
	"dashgen/internal/ir"
	"dashgen/internal/prometheus"
	"dashgen/internal/safety"
	promvalidate "dashgen/internal/validate"
)

// fixtureDir is the canonical offline backend used by a subset of the
// app/validate tests (those that exercise fixture plumbing). The core
// validation table uses a fake client so verdicts do not depend on
// whether a given expression was pre-recorded.
const fixtureDir = "../../../testdata/fixtures/service-basic"

// stubClient is a prometheus.Client double that returns a fixed instant
// query result for every call. Only InstantQuery is exercised by the
// validate pipeline; the other methods panic on use to flag test drift.
type stubClient struct {
	numSeries int
}

func (s *stubClient) Metadata(context.Context) (map[string][]prometheus.MetricMetadata, error) {
	panic("stubClient.Metadata not implemented")
}
func (s *stubClient) LabelNames(context.Context, string) ([]string, error) {
	panic("stubClient.LabelNames not implemented")
}
func (s *stubClient) Series(context.Context, []string) ([]map[string]string, error) {
	panic("stubClient.Series not implemented")
}
func (s *stubClient) InstantQuery(_ context.Context, _ string) (*prometheus.QueryResult, error) {
	return &prometheus.QueryResult{ResultType: "vector", NumSeries: s.numSeries}, nil
}

func TestRun(t *testing.T) {
	t.Parallel()

	// Shared invariants for readability in the cases table.
	const (
		acceptVerdict  = string(ir.VerdictAccept)
		warningVerdict = string(ir.VerdictAcceptWithWarning)
		refuseVerdict  = string(ir.VerdictRefuse)
	)

	cases := []struct {
		name         string
		exprs        []string
		strict       bool
		client       prometheus.Client
		wantVerdicts []string
		// wantWarnings[i] is checked for entry i when present. Non-listed
		// indexes are not asserted.
		wantWarnings map[int][]string
		// wantRefusalPrefixes[i] is checked as a prefix on entry i's
		// refusal_reason when present.
		wantRefusalPrefixes map[int]string
		wantErr             error
		// wantErrAny asserts err != nil without caring about its sentinel.
		// Used for non-strict refusals which return a plain "refused: ..."
		// error (exit 1) rather than wrapping ErrStrictViolation (exit 4).
		wantErrAny bool
	}{
		{
			name: "pure_accept_scoped_and_bounded",
			// Backend returns a single series → no empty_result. Selector
			// scope contains job=checkout → no unscoped_aggregation.
			exprs:        []string{`sum by (status_code) (rate(http_requests_total{job="checkout"}[5m]))`},
			client:       &stubClient{numSeries: 1},
			wantVerdicts: []string{acceptVerdict},
		},
		{
			name: "accept_with_warning_empty_result",
			// Backend returns zero series → empty_result warning.
			exprs:        []string{`sum by (status_code) (rate(http_requests_total{job="checkout"}[5m]))`},
			client:       &stubClient{numSeries: 0},
			wantVerdicts: []string{warningVerdict},
			wantWarnings: map[int][]string{
				0: {promvalidate.WarningEmptyResult},
			},
		},
		{
			name:         "accept_with_warning_unscoped_aggregation",
			exprs:        []string{`sum by (method) (rate(http_requests_total[5m]))`},
			client:       &stubClient{numSeries: 1},
			wantVerdicts: []string{warningVerdict},
			wantWarnings: map[int][]string{
				0: {safety.WarningUnscopedAggregation},
			},
		},
		{
			name:         "refuse_parse_error",
			exprs:        []string{`sum(rate(http_requests_total[5m])`},
			client:       &stubClient{numSeries: 1},
			wantVerdicts: []string{refuseVerdict},
			wantErrAny:   true,
			wantRefusalPrefixes: map[int]string{
				0: promvalidate.ReasonParseError,
			},
		},
		{
			name:         "refuse_banned_label_matcher",
			exprs:        []string{`http_requests_total{user_id="42"}`},
			client:       &stubClient{numSeries: 1},
			wantVerdicts: []string{refuseVerdict},
			wantErrAny:   true,
			wantRefusalPrefixes: map[int]string{
				0: promvalidate.ReasonBannedLabelMatcher,
			},
		},
		{
			name:         "refuse_banned_label_grouping",
			exprs:        []string{`sum by (user_id) (rate(http_requests_total{job="admin"}[5m]))`},
			client:       &stubClient{numSeries: 1},
			wantVerdicts: []string{refuseVerdict},
			wantErrAny:   true,
			wantRefusalPrefixes: map[int]string{
				0: promvalidate.ReasonBannedLabelGrouping,
			},
		},
		{
			name:         "strict_promotes_warning_to_error",
			exprs:        []string{`sum by (method) (rate(http_requests_total[5m]))`},
			client:       &stubClient{numSeries: 1},
			strict:       true,
			wantVerdicts: []string{warningVerdict},
			wantErr:      generate.ErrStrictViolation,
		},
		{
			name: "input_order_preserved",
			// Mixed: index 0 refuses, index 1 accepts, index 2 warns.
			// Output must reflect this input order exactly.
			exprs: []string{
				`http_requests_total{user_id="1"}`,
				`sum by (status_code) (rate(http_requests_total{job="checkout"}[5m]))`,
				`sum by (method) (rate(http_requests_total[5m]))`,
			},
			client:       &stubClient{numSeries: 1},
			wantVerdicts: []string{refuseVerdict, acceptVerdict, warningVerdict},
			wantErrAny:   true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.RunConfig{
				FixtureDir: fixtureDir, // satisfies mutual-exclusivity gate
				Exprs:      tc.exprs,
				Strict:     tc.strict,
			}
			var buf bytes.Buffer
			err := runWithClient(context.Background(), cfg, tc.client, tc.exprs, &buf)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v want wrap of %v", err, tc.wantErr)
				}
			case tc.wantErrAny:
				if err == nil {
					t.Fatalf("want non-nil error, got nil")
				}
				if errors.Is(err, generate.ErrStrictViolation) {
					t.Fatalf("non-strict refusal should not wrap ErrStrictViolation, got: %v", err)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			}

			var entries []Entry
			if jerr := json.Unmarshal(buf.Bytes(), &entries); jerr != nil {
				t.Fatalf("decode json: %v\nraw=%s", jerr, buf.String())
			}
			if len(entries) != len(tc.exprs) {
				t.Fatalf("entries=%d want %d (raw=%s)", len(entries), len(tc.exprs), buf.String())
			}
			for i, e := range entries {
				if e.Expr != tc.exprs[i] {
					t.Fatalf("entry[%d].Expr=%q want %q (order not preserved)", i, e.Expr, tc.exprs[i])
				}
				if e.Verdict != tc.wantVerdicts[i] {
					t.Fatalf("entry[%d].Verdict=%q want %q (entry=%+v)", i, e.Verdict, tc.wantVerdicts[i], e)
				}
			}
			for idx, want := range tc.wantWarnings {
				got := entries[idx].Warnings
				if len(got) != len(want) {
					t.Fatalf("entry[%d].Warnings=%v want %v", idx, got, want)
				}
				for j := range want {
					if got[j] != want[j] {
						t.Fatalf("entry[%d].Warnings=%v want %v", idx, got, want)
					}
				}
			}
			for idx, prefix := range tc.wantRefusalPrefixes {
				if !strings.HasPrefix(entries[idx].RefusalReason, prefix) {
					t.Fatalf("entry[%d].RefusalReason=%q want prefix %q", idx, entries[idx].RefusalReason, prefix)
				}
			}
		})
	}
}

// TestRun_MutualExclusionBackends exercises the input-validation layer of
// the full run() flow; the fake-client path in TestRun bypasses it.
func TestRun_MutualExclusionBackends(t *testing.T) {
	t.Parallel()
	cfg := &config.RunConfig{
		PromURL:    "http://example.invalid",
		FixtureDir: fixtureDir,
		Exprs:      []string{`up`},
	}
	err := run(context.Background(), cfg, &bytes.Buffer{})
	if !errors.Is(err, ErrValidateInput) {
		t.Fatalf("err=%v want wrap of ErrValidateInput", err)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err=%q should mention mutual exclusion", err.Error())
	}
}

func TestRun_RequiresBackend(t *testing.T) {
	t.Parallel()
	cfg := &config.RunConfig{Exprs: []string{`up`}}
	err := run(context.Background(), cfg, &bytes.Buffer{})
	if !errors.Is(err, ErrValidateInput) {
		t.Fatalf("err=%v want wrap of ErrValidateInput", err)
	}
}

func TestRun_RequiresExprs(t *testing.T) {
	t.Parallel()
	cfg := &config.RunConfig{FixtureDir: fixtureDir}
	err := run(context.Background(), cfg, &bytes.Buffer{})
	if !errors.Is(err, ErrValidateInput) {
		t.Fatalf("err=%v want wrap of ErrValidateInput", err)
	}
}

// TestRun_ExprFileCombinedWithCLI exercises the --from loader alongside
// repeated --expr values, and also confirms the fixture-backed path runs
// end-to-end.
func TestRun_ExprFileCombinedWithCLI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "exprs.txt")
	body := strings.Join([]string{
		"# comment line",
		"",
		`sum by (method) (rate(http_requests_total[5m]))`,
		`  # indented comment with leading whitespace`,
		`sum by (status_code) (rate(http_requests_total{job="checkout"}[5m]))`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	cfg := &config.RunConfig{
		FixtureDir: fixtureDir,
		Exprs:      []string{`up{job="checkout"}`},
		ExprFile:   path,
	}
	var buf bytes.Buffer
	// Strict refusals here would be propagated; warnings are tolerated
	// because --strict is false. We only assert input order is preserved.
	_ = run(context.Background(), cfg, &buf)
	var entries []Entry
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, buf.String())
	}
	if len(entries) != 3 {
		t.Fatalf("entries=%d want 3 (raw=%s)", len(entries), buf.String())
	}
	wantExprs := []string{
		`up{job="checkout"}`,
		`sum by (method) (rate(http_requests_total[5m]))`,
		`sum by (status_code) (rate(http_requests_total{job="checkout"}[5m]))`,
	}
	for i, want := range wantExprs {
		if entries[i].Expr != want {
			t.Fatalf("entry[%d].Expr=%q want %q", i, entries[i].Expr, want)
		}
	}
}
