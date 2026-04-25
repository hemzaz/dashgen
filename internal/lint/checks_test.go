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
	for _, want := range []string{"banned-label", "empty-panel"} {
		if !codes[want] {
			t.Errorf("seed check %q not registered; got codes %v", want, codes)
		}
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
