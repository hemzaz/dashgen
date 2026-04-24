package warnings

import (
	"bytes"
	"encoding/json"
	"testing"

	"dashgen/internal/ir"
)

func fixtureDashboard() *ir.Dashboard {
	return &ir.Dashboard{
		UID:     "d1d2d3d4",
		Title:   "Payments Service",
		Profile: "service",
		Rows: []ir.Row{
			{
				Title: "traffic",
				Panels: []ir.Panel{
					{
						UID:     "aaaaaaaa11112222",
						Title:   "Requests per second",
						Kind:    ir.PanelKindTimeSeries,
						Verdict: ir.VerdictAccept,
						Queries: []ir.QueryCandidate{
							{
								Expr:    "sum(rate(http_requests_total[5m]))",
								Verdict: ir.VerdictAccept,
							},
						},
					},
					{
						UID:      "bbbbbbbb33334444",
						Title:    "Requests by method",
						Kind:     ir.PanelKindTimeSeries,
						Warnings: []string{"high_cardinality_group"},
						Verdict:  ir.VerdictAcceptWithWarning,
						Queries: []ir.QueryCandidate{
							{
								Expr:         "sum by (method) (rate(http_requests_total[5m]))",
								Verdict:      ir.VerdictAcceptWithWarning,
								WarningCodes: []string{"high_cardinality_group"},
							},
							{
								Expr:          "sum by (user_id) (rate(http_requests_total[5m]))",
								Verdict:       ir.VerdictRefuse,
								RefusalReason: "banned high-cardinality label user_id",
							},
						},
					},
				},
			},
			{
				Title: "errors",
				Panels: []ir.Panel{
					{
						UID:       "dddddddd77778888",
						Title:     "Errors by trace_id",
						Kind:      ir.PanelKindTimeSeries,
						Verdict:   ir.VerdictRefuse,
						Rationale: "grouping by trace_id refused",
						Queries: []ir.QueryCandidate{
							{
								Expr:          "sum by (trace_id) (rate(http_requests_total[5m]))",
								Verdict:       ir.VerdictRefuse,
								RefusalReason: "banned high-cardinality label trace_id",
							},
						},
					},
				},
			},
		},
	}
}

func TestRenderDeterministic(t *testing.T) {
	d := fixtureDashboard()
	a, err := Render(d)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	b, err := Render(d)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("render not byte-stable across runs")
	}
}

func TestRenderGolden(t *testing.T) {
	want := `[
  {
    "panel_uid": "dddddddd77778888",
    "section": "errors",
    "code": "panel_refused",
    "detail": "grouping by trace_id refused",
    "severity": "refuse"
  },
  {
    "panel_uid": "dddddddd77778888",
    "section": "errors",
    "code": "query_refused",
    "detail": "banned high-cardinality label trace_id [sum by (trace_id) (rate(http_requests_total[5m]))]",
    "severity": "refuse"
  },
  {
    "panel_uid": "bbbbbbbb33334444",
    "section": "traffic",
    "code": "high_cardinality_group",
    "detail": "",
    "severity": "warning"
  },
  {
    "panel_uid": "bbbbbbbb33334444",
    "section": "traffic",
    "code": "high_cardinality_group",
    "detail": "sum by (method) (rate(http_requests_total[5m]))",
    "severity": "warning"
  },
  {
    "panel_uid": "bbbbbbbb33334444",
    "section": "traffic",
    "code": "query_refused",
    "detail": "banned high-cardinality label user_id [sum by (user_id) (rate(http_requests_total[5m]))]",
    "severity": "refuse"
  }
]`

	d := fixtureDashboard()
	got, err := Render(d)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if string(got) != want {
		t.Fatalf("warnings mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderEmptyIsArray(t *testing.T) {
	d := &ir.Dashboard{UID: "x", Title: "x", Profile: "service"}
	out, err := Render(d)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if string(out) != "[]" {
		t.Errorf("empty render = %q, want %q", out, "[]")
	}
	var parsed []any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("empty output not valid JSON array: %v", err)
	}
}

func TestRenderSortOrder(t *testing.T) {
	d := fixtureDashboard()
	out, err := Render(d)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var entries []map[string]string
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i := 1; i < len(entries); i++ {
		prev, cur := entries[i-1], entries[i]
		if prev["section"] > cur["section"] {
			t.Errorf("entries not sorted by section at %d: %q > %q", i, prev["section"], cur["section"])
			continue
		}
		if prev["section"] == cur["section"] && prev["panel_uid"] > cur["panel_uid"] {
			t.Errorf("entries not sorted by panel_uid at %d", i)
			continue
		}
		if prev["section"] == cur["section"] && prev["panel_uid"] == cur["panel_uid"] && prev["code"] > cur["code"] {
			t.Errorf("entries not sorted by code at %d", i)
		}
	}
}

func TestRenderNilDashboard(t *testing.T) {
	if _, err := Render(nil); err == nil {
		t.Fatalf("expected error for nil dashboard")
	}
}
