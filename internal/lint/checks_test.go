package lint

import (
	"reflect"
	"testing"
)

// TestCheckBannedLabel covers the seed safety check: any banned-label
// reference in PromQL must refuse, label-name boundaries must hold so
// "user_id_extra" is not treated as "user_id", and rows must be skipped.
func TestCheckBannedLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		input    Input
		wantHit  bool
		wantWord string
	}{
		{
			name: "matcher_with_user_id_refuses",
			input: Input{Panels: []Panel{{
				ID: 1, Type: "timeseries", Title: "x",
				Targets: []Target{{Expr: `sum by (job) (rate(http_requests_total{user_id="abc"}[5m]))`}},
			}}},
			wantHit:  true,
			wantWord: "user_id",
		},
		{
			name: "grouping_with_trace_id_refuses",
			input: Input{Panels: []Panel{{
				ID: 2, Type: "timeseries", Title: "y",
				Targets: []Target{{Expr: `sum by (instance, trace_id) (foo)`}},
			}}},
			wantHit:  true,
			wantWord: "trace_id",
		},
		{
			name: "user_id_extra_is_not_user_id",
			input: Input{Panels: []Panel{{
				ID: 3, Type: "timeseries", Title: "z",
				Targets: []Target{{Expr: `sum by (job) (rate(http_user_id_extra{}[5m]))`}},
			}}},
			wantHit: false,
		},
		{
			name: "no_banned_label_is_clean",
			input: Input{Panels: []Panel{{
				ID: 4, Type: "timeseries", Title: "ok",
				Targets: []Target{{Expr: `sum by (job, instance) (rate(http_requests_total[5m]))`}},
			}}},
			wantHit: false,
		},
		{
			name: "row_is_skipped",
			input: Input{Panels: []Panel{{
				ID: 5, Type: "row", Title: "traffic",
				Targets: []Target{{Expr: `whatever{user_id="x"}`}},
			}}},
			wantHit: false,
		},
		{
			name: "session_id_in_label_join_refuses",
			input: Input{Panels: []Panel{{
				ID: 6, Type: "timeseries", Title: "w",
				Targets: []Target{{Expr: `count by (session_id) (foo)`}},
			}}},
			wantHit:  true,
			wantWord: "session_id",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkBannedLabel{}.Run(&tc.input)
			if tc.wantHit {
				if len(got) != 1 {
					t.Fatalf("got %d issues; want 1", len(got))
				}
				if got[0].Code != "banned-label" {
					t.Errorf("Code = %q; want banned-label", got[0].Code)
				}
				if got[0].Severity != SeverityRefuse {
					t.Errorf("Severity = %q; want refuse", got[0].Severity)
				}
				if !reflect.DeepEqual(got[0].PanelID, tc.input.Panels[0].ID) {
					t.Errorf("PanelID = %d; want %d", got[0].PanelID, tc.input.Panels[0].ID)
				}
				if got[0].Message == "" || !contains(got[0].Message, tc.wantWord) {
					t.Errorf("Message must mention %q; got %q", tc.wantWord, got[0].Message)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("got %d issues; want 0\n%+v", len(got), got)
			}
		})
	}
}

// TestCheckEmptyPanel covers the second seed check.
func TestCheckEmptyPanel(t *testing.T) {
	t.Parallel()
	in := Input{Panels: []Panel{
		{ID: 100, Type: "row", Title: "traffic"},
		{ID: 101, Type: "timeseries", Title: "good", Targets: []Target{{Expr: `up`}}},
		{ID: 102, Type: "timeseries", Title: "empty"},
	}}
	got := checkEmptyPanel{}.Run(&in)
	if len(got) != 1 {
		t.Fatalf("got %d issues; want 1\n%+v", len(got), got)
	}
	if got[0].PanelID != 102 || got[0].Code != "empty-panel" || got[0].Severity != SeverityRefuse {
		t.Errorf("unexpected issue: %+v", got[0])
	}
}

// TestRunAll_DeterministicOrdering proves issues sort by (Code, PanelID,
// Message) regardless of registration order or check internals.
func TestRunAll_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	in := &Input{Panels: []Panel{
		{ID: 200, Type: "timeseries", Title: "a", Targets: []Target{{Expr: `up{user_id="x"}`}}},
		{ID: 100, Type: "timeseries", Title: "b"}, // empty
		{ID: 150, Type: "timeseries", Title: "c", Targets: []Target{{Expr: `up{trace_id="y"}`}}},
	}}
	got := RunAll(in)
	wantOrder := []struct {
		code    string
		panelID int64
	}{
		{"banned-label", 150}, // banned-label sorts before empty-panel; among banned-label, lower id first
		{"banned-label", 200},
		{"empty-panel", 100},
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d issues; want %d\n%+v", len(got), len(wantOrder), got)
	}
	for i, want := range wantOrder {
		if got[i].Code != want.code || got[i].PanelID != want.panelID {
			t.Errorf("issue %d: got (%s, %d); want (%s, %d)", i, got[i].Code, got[i].PanelID, want.code, want.panelID)
		}
	}
}

// TestHasRefusal covers the small helper used by the CLI exit-code path.
func TestHasRefusal(t *testing.T) {
	t.Parallel()
	if HasRefusal(nil) {
		t.Errorf("HasRefusal(nil) = true; want false")
	}
	warnOnly := []Issue{{Code: "x", Severity: SeverityWarn}}
	if HasRefusal(warnOnly) {
		t.Errorf("HasRefusal(warn-only) = true; want false")
	}
	withRefuse := []Issue{{Code: "x", Severity: SeverityWarn}, {Code: "y", Severity: SeverityRefuse}}
	if !HasRefusal(withRefuse) {
		t.Errorf("HasRefusal(with-refuse) = false; want true")
	}
}

// TestCheckList_BuiltinsRegistered confirms init() registered the seed
// checks so the orchestrator picks them up automatically.
func TestCheckList_BuiltinsRegistered(t *testing.T) {
	t.Parallel()
	got := CheckList()
	codes := map[string]bool{}
	for _, c := range got {
		codes[c.Code()] = true
	}
	for _, want := range []string{
		"banned-label", "empty-panel", "duplicate-panel", "without-grouping",
		"missing-rationale-row", "rate-on-gauge", "suspicious-units",
	} {
		if !codes[want] {
			t.Errorf("seed check %q not registered; got codes %v", want, codes)
		}
	}
}

// TestCheckDuplicatePanel covers the duplicate-panel check. The check
// keys on (title, primary-target.expr) so it catches the operator-
// visible "panel shipped twice" mistake without false-positiving on
// renderer modulo-collisions across distinct UIDs.
func TestCheckDuplicatePanel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		input     Input
		wantCount int // expected issues
	}{
		{
			name: "different_titles_same_expr_clean",
			input: Input{Panels: []Panel{
				{ID: 1, Type: "timeseries", Title: "Request rate: a", Targets: []Target{{Expr: `sum by (job) (rate(http_requests_total[5m]))`}}},
				{ID: 2, Type: "timeseries", Title: "Request rate: b", Targets: []Target{{Expr: `sum by (job) (rate(http_requests_total[5m]))`}}},
			}},
			wantCount: 0,
		},
		{
			name: "same_title_different_expr_clean",
			input: Input{Panels: []Panel{
				{ID: 3, Type: "timeseries", Title: "shared", Targets: []Target{{Expr: "up"}}},
				{ID: 4, Type: "timeseries", Title: "shared", Targets: []Target{{Expr: "down"}}},
			}},
			wantCount: 0,
		},
		{
			name: "same_title_and_expr_both_flagged",
			input: Input{Panels: []Panel{
				{ID: 10, Type: "timeseries", Title: "duped", Targets: []Target{{Expr: "rate(foo[5m])"}}},
				{ID: 11, Type: "timeseries", Title: "different", Targets: []Target{{Expr: "rate(foo[5m])"}}},
				{ID: 12, Type: "timeseries", Title: "duped", Targets: []Target{{Expr: "rate(foo[5m])"}}},
			}},
			wantCount: 2,
		},
		{
			name: "row_with_same_title_skipped",
			input: Input{Panels: []Panel{
				{ID: 1, Type: "row", Title: "traffic"},
				{ID: 2, Type: "row", Title: "traffic"},
				{ID: 3, Type: "timeseries", Title: "ok", Targets: []Target{{Expr: "up"}}},
			}},
			wantCount: 0,
		},
		{
			name: "two_empty_panels_same_title_flagged",
			input: Input{Panels: []Panel{
				{ID: 100, Type: "timeseries", Title: "empty"},
				{ID: 101, Type: "timeseries", Title: "empty"},
			}},
			wantCount: 2, // primary expr "" matches; both flagged
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkDuplicatePanel{}.Run(&tc.input)
			if len(got) != tc.wantCount {
				t.Fatalf("got %d issues; want %d\n%+v", len(got), tc.wantCount, got)
			}
			for _, iss := range got {
				if iss.Code != "duplicate-panel" {
					t.Errorf("Code = %q; want duplicate-panel", iss.Code)
				}
				if iss.Severity != SeverityRefuse {
					t.Errorf("Severity = %q; want refuse", iss.Severity)
				}
			}
		})
	}
}

// TestCheckWithoutGrouping covers the without-grouping check, including
// identifier-boundary handling so a label name containing the substring
// "without" does not trip it.
func TestCheckWithoutGrouping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   Input
		wantHit bool
	}{
		{
			name: "without_paren_refuses",
			input: Input{Panels: []Panel{{
				ID: 1, Type: "timeseries", Title: "x",
				Targets: []Target{{Expr: `sum without (instance) (rate(http_requests_total[5m]))`}},
			}}},
			wantHit: true,
		},
		{
			name: "without_paren_no_space_refuses",
			input: Input{Panels: []Panel{{
				ID: 2, Type: "timeseries", Title: "y",
				Targets: []Target{{Expr: `sum without(instance) (foo)`}},
			}}},
			wantHit: true,
		},
		{
			name: "by_grouping_is_clean",
			input: Input{Panels: []Panel{{
				ID: 3, Type: "timeseries", Title: "z",
				Targets: []Target{{Expr: `sum by (instance, job) (rate(http_requests_total[5m]))`}},
			}}},
			wantHit: false,
		},
		{
			name: "label_named_without_user_does_not_match",
			input: Input{Panels: []Panel{{
				ID: 4, Type: "timeseries", Title: "edge",
				Targets: []Target{{Expr: `sum by (job) (rate(some_metric{request_without_user="foo"}[5m]))`}},
			}}},
			wantHit: false,
		},
		{
			name: "row_skipped",
			input: Input{Panels: []Panel{{
				ID: 5, Type: "row", Title: "traffic",
				Targets: []Target{{Expr: `sum without (job) (foo)`}},
			}}},
			wantHit: false,
		},
		{
			name: "without_followed_by_non_paren_does_not_match",
			input: Input{Panels: []Panel{{
				ID: 6, Type: "timeseries", Title: "nope",
				Targets: []Target{{Expr: `something_without_paren_after`}},
			}}},
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkWithoutGrouping{}.Run(&tc.input)
			if tc.wantHit {
				if len(got) != 1 {
					t.Fatalf("got %d issues; want 1\n%+v", len(got), got)
				}
				if got[0].Code != "without-grouping" || got[0].Severity != SeverityRefuse {
					t.Errorf("unexpected issue: %+v", got[0])
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("got %d issues; want 0\n%+v", len(got), got)
			}
		})
	}
}

// TestRegister_PanicsOnNil and TestRegister_PanicsOnEmptyCode mirror the
// enrich-factory hardening: malformed registrations fail at init() time.
func TestRegister_PanicsOnNil(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(nil) did not panic")
		}
	}()
	Register(nil)
}

type emptyCodeCheck struct{}

func (emptyCodeCheck) Code() string         { return "" }
func (emptyCodeCheck) Run(_ *Input) []Issue { return nil }

func TestRegister_PanicsOnEmptyCode(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(emptyCodeCheck) did not panic")
		}
	}()
	Register(emptyCodeCheck{})
}

// TestCheckMissingRationaleRow covers the warn-severity check for
// panels whose titles are absent from rationale.md.
func TestCheckMissingRationaleRow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		input     Input
		wantCount int
	}{
		{
			name: "empty_rationale_disables_check",
			input: Input{
				Panels:    []Panel{{ID: 1, Type: "timeseries", Title: "anything"}},
				Rationale: "",
			},
			wantCount: 0,
		},
		{
			name: "title_present_in_rationale_clean",
			input: Input{
				Panels:    []Panel{{ID: 1, Type: "timeseries", Title: "Request rate: foo"}},
				Rationale: "### traffic\n\n- **Request rate: foo** — counter rate.\n",
			},
			wantCount: 0,
		},
		{
			name: "title_missing_warns",
			input: Input{
				Panels: []Panel{
					{ID: 1, Type: "timeseries", Title: "Request rate: foo"},
					{ID: 2, Type: "timeseries", Title: "Stranger"},
				},
				Rationale: "### traffic\n\n- **Request rate: foo** — counter rate.\n",
			},
			wantCount: 1,
		},
		{
			name: "row_panels_skipped_even_when_missing",
			input: Input{
				Panels: []Panel{
					{ID: 1, Type: "row", Title: "traffic"},
					{ID: 2, Type: "timeseries", Title: "Stranger"},
				},
				Rationale: "no panels mentioned at all",
			},
			wantCount: 1, // only the timeseries panel triggers
		},
		{
			name: "empty_title_skipped",
			input: Input{
				Panels:    []Panel{{ID: 1, Type: "timeseries", Title: ""}},
				Rationale: "no titles",
			},
			wantCount: 0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkMissingRationaleRow{}.Run(&tc.input)
			if len(got) != tc.wantCount {
				t.Fatalf("got %d issues; want %d\n%+v", len(got), tc.wantCount, got)
			}
			for _, iss := range got {
				if iss.Code != "missing-rationale-row" {
					t.Errorf("Code = %q; want missing-rationale-row", iss.Code)
				}
				if iss.Severity != SeverityWarn {
					t.Errorf("Severity = %q; want warn", iss.Severity)
				}
			}
		})
	}
}

// TestCheckRateOnGauge covers the rate-on-gauge refusal.
func TestCheckRateOnGauge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   Input
		wantHit bool
	}{
		{
			name: "rate_on_total_clean",
			input: Input{Panels: []Panel{{
				ID: 1, Type: "timeseries", Title: "rps",
				Targets: []Target{{Expr: `sum by (job) (rate(http_requests_total[5m]))`}},
			}}},
			wantHit: false,
		},
		{
			name: "rate_on_count_clean",
			input: Input{Panels: []Panel{{
				ID: 2, Type: "timeseries", Title: "rps",
				Targets: []Target{{Expr: `rate(go_gc_duration_seconds_count[1m])`}},
			}}},
			wantHit: false,
		},
		{
			name: "rate_on_gauge_refuses",
			input: Input{Panels: []Panel{{
				ID: 3, Type: "timeseries", Title: "memory rate",
				Targets: []Target{{Expr: `rate(memory_usage_bytes[5m])`}},
			}}},
			wantHit: true,
		},
		{
			name: "irate_on_gauge_refuses",
			input: Input{Panels: []Panel{{
				ID: 4, Type: "timeseries", Title: "load rate",
				Targets: []Target{{Expr: `irate(node_load1[1m])`}},
			}}},
			wantHit: true,
		},
		{
			name: "no_rate_clean",
			input: Input{Panels: []Panel{{
				ID: 5, Type: "timeseries", Title: "raw",
				Targets: []Target{{Expr: `node_load1`}},
			}}},
			wantHit: false,
		},
		{
			name: "row_skipped",
			input: Input{Panels: []Panel{{
				ID: 6, Type: "row", Title: "header",
				Targets: []Target{{Expr: `rate(memory_usage_bytes[5m])`}},
			}}},
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkRateOnGauge{}.Run(&tc.input)
			if tc.wantHit {
				if len(got) == 0 {
					t.Fatalf("expected at least one rate-on-gauge issue; got none")
				}
				if got[0].Code != "rate-on-gauge" || got[0].Severity != SeverityRefuse {
					t.Errorf("unexpected issue: %+v", got[0])
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("got %d issues; want 0\n%+v", len(got), got)
			}
		})
	}
}

// TestCheckSuspiciousUnits covers the narrow histogram-quantile-of-
// time-shaped-histogram-with-non-time-unit warning.
func TestCheckSuspiciousUnits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   Input
		wantHit bool
	}{
		{
			name: "histogram_quantile_seconds_with_seconds_unit_clean",
			input: Input{Panels: []Panel{{
				ID: 1, Type: "timeseries", Title: "p95 latency", Unit: "s",
				Targets: []Target{{Expr: `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))`}},
			}}},
			wantHit: false,
		},
		{
			name: "histogram_quantile_seconds_with_short_unit_warns",
			input: Input{Panels: []Panel{{
				ID: 2, Type: "timeseries", Title: "p95 latency", Unit: "short",
				Targets: []Target{{Expr: `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))`}},
			}}},
			wantHit: true,
		},
		{
			name: "histogram_quantile_bytes_with_bytes_unit_clean",
			input: Input{Panels: []Panel{{
				ID: 3, Type: "timeseries", Title: "p95 size", Unit: "bytes",
				Targets: []Target{{Expr: `histogram_quantile(0.95, sum by (le) (rate(http_request_size_bytes_bucket[5m])))`}},
			}}},
			wantHit: false, // bytes histogram is not time-shaped — out of scope
		},
		{
			name: "no_histogram_quantile_clean",
			input: Input{Panels: []Panel{{
				ID: 4, Type: "timeseries", Title: "rps", Unit: "reqps",
				Targets: []Target{{Expr: `sum by (job) (rate(http_requests_total[5m]))`}},
			}}},
			wantHit: false,
		},
		{
			name: "row_skipped",
			input: Input{Panels: []Panel{{
				ID: 5, Type: "row", Title: "latency", Unit: "short",
				Targets: []Target{{Expr: `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))`}},
			}}},
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkSuspiciousUnits{}.Run(&tc.input)
			if tc.wantHit {
				if len(got) != 1 {
					t.Fatalf("got %d issues; want 1\n%+v", len(got), got)
				}
				if got[0].Code != "suspicious-units" || got[0].Severity != SeverityWarn {
					t.Errorf("unexpected issue: %+v", got[0])
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("got %d issues; want 0\n%+v", len(got), got)
			}
		})
	}
}

// contains is a tiny helper used in checks_test only; avoids pulling in
// strings just for these assertions and keeps the dependency surface
// of the test file minimal.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
