package grafana

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"dashgen/internal/ir"
)

// fixtureDashboard returns a deterministic IR covering the interesting
// render cases: multiple rows, multiple panels per row, panel kinds (stat
// and timeseries), accepted / accept_with_warning / refused queries, and a
// fully refused panel.
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
						UID:        "aaaaaaaa11112222",
						Title:      "Requests per second",
						Kind:       ir.PanelKindTimeSeries,
						Unit:       "reqps",
						Confidence: 0.92,
						Verdict:    ir.VerdictAccept,
						Rationale:  "primary RED traffic signal",
						Queries: []ir.QueryCandidate{
							{
								Expr:         "sum(rate(http_requests_total[5m]))",
								LegendFormat: "rps",
								Unit:         "reqps",
								Verdict:      ir.VerdictAccept,
							},
						},
					},
					{
						UID:        "bbbbbbbb33334444",
						Title:      "Requests by method",
						Kind:       ir.PanelKindTimeSeries,
						Unit:       "reqps",
						Confidence: 0.70,
						Warnings:   []string{"high_cardinality_group"},
						Verdict:    ir.VerdictAcceptWithWarning,
						Rationale:  "method grouping has moderate cardinality",
						Queries: []ir.QueryCandidate{
							{
								Expr:         "sum by (method) (rate(http_requests_total[5m]))",
								LegendFormat: "{{method}}",
								Unit:         "reqps",
								Verdict:      ir.VerdictAcceptWithWarning,
								WarningCodes: []string{"high_cardinality_group"},
							},
							{
								Expr:          "sum by (user_id) (rate(http_requests_total[5m]))",
								LegendFormat:  "{{user_id}}",
								Unit:          "reqps",
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
						UID:        "cccccccc55556666",
						Title:      "Error rate",
						Kind:       ir.PanelKindStat,
						Unit:       "percentunit",
						Confidence: 0.88,
						Verdict:    ir.VerdictAccept,
						Rationale:  "5xx share of total traffic",
						Queries: []ir.QueryCandidate{
							{
								Expr:         `sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))`,
								LegendFormat: "error %",
								Unit:         "percentunit",
								Verdict:      ir.VerdictAccept,
							},
						},
					},
					{
						UID:        "dddddddd77778888",
						Title:      "Errors by trace_id",
						Kind:       ir.PanelKindTimeSeries,
						Unit:       "short",
						Confidence: 0.20,
						Verdict:    ir.VerdictRefuse,
						Rationale:  "grouping by trace_id refused",
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
		Warnings: []string{"selector_wildcard: one metric selector had no label filter"},
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

func TestRenderGoldenInvariants(t *testing.T) {
	d := fixtureDashboard()
	out, err := Render(d)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if got, ok := parsed["schemaVersion"].(float64); !ok || int(got) != schemaVersion {
		t.Errorf("schemaVersion = %v, want %d", parsed["schemaVersion"], schemaVersion)
	}
	if got, _ := parsed["title"].(string); got != "Payments Service" {
		t.Errorf("title = %q, want %q", got, "Payments Service")
	}
	if got, _ := parsed["uid"].(string); got != "d1d2d3d4" {
		t.Errorf("uid = %q, want %q", got, "d1d2d3d4")
	}
	if got, _ := parsed["editable"].(bool); got {
		t.Errorf("editable = true, want false")
	}

	tags, _ := parsed["tags"].([]any)
	if len(tags) != 2 || tags[0] != "dashgen" || tags[1] != "service" {
		t.Errorf("tags = %v, want [dashgen service]", tags)
	}

	templating, _ := parsed["templating"].(map[string]any)
	list, _ := templating["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("templating.list len = %d, want 1", len(list))
	}
	ds, _ := list[0].(map[string]any)
	if ds["name"] != "datasource" || ds["type"] != "datasource" || ds["query"] != "prometheus" {
		t.Errorf("templating entry = %v, expected $datasource prometheus entry", ds)
	}

	panels, _ := parsed["panels"].([]any)
	// 2 rows + 4 data panels = 6 entries.
	if len(panels) != 6 {
		t.Fatalf("panels len = %d, want 6", len(panels))
	}

	// First panel must be the "traffic" row panel.
	first, _ := panels[0].(map[string]any)
	if first["type"] != "row" || first["title"] != "traffic" {
		t.Errorf("first panel = %v, expected traffic row", first)
	}

	// Second panel must be a timeseries and carry one target (the one
	// accept query for "Requests per second").
	second, _ := panels[1].(map[string]any)
	if second["type"] != "timeseries" {
		t.Errorf("second panel type = %v, want timeseries", second["type"])
	}
	targets, _ := second["targets"].([]any)
	if len(targets) != 1 {
		t.Errorf("second panel targets = %d, want 1", len(targets))
	}

	// Third panel (Requests by method) has one accepted + one refused
	// query, so exactly one target and a non-empty description.
	third, _ := panels[2].(map[string]any)
	thirdTargets, _ := third["targets"].([]any)
	if len(thirdTargets) != 1 {
		t.Errorf("third panel targets = %d, want 1 (refused dropped)", len(thirdTargets))
	}
	if desc, _ := third["description"].(string); desc == "" {
		t.Errorf("third panel description should mention omitted queries")
	}

	// Fifth panel is the stat.
	fifth, _ := panels[4].(map[string]any)
	if fifth["type"] != "stat" {
		t.Errorf("fifth panel type = %v, want stat", fifth["type"])
	}

	// Sixth panel is the fully-refused "Errors by trace_id" — no targets.
	sixth, _ := panels[5].(map[string]any)
	sixthTargets, _ := sixth["targets"].([]any)
	if len(sixthTargets) != 0 {
		t.Errorf("refused panel targets = %d, want 0", len(sixthTargets))
	}
}

func TestPanelIDStable(t *testing.T) {
	a := panelIntID("aaaaaaaa11112222")
	b := panelIntID("aaaaaaaa11112222")
	if a != b {
		t.Fatalf("panelIntID not stable: %d vs %d", a, b)
	}
	if a == 0 {
		t.Fatalf("panelIntID returned 0")
	}
	if panelIntID("aaaaaaaa11112222") == panelIntID("bbbbbbbb33334444") {
		t.Fatalf("panelIntID collided on distinct inputs")
	}
}

func TestPanelIDNonZeroForShortUID(t *testing.T) {
	// UIDs shorter than 8 chars fall through the prefix parse; the
	// fallback must still yield a non-zero id.
	if got := panelIntID("x"); got == 0 {
		t.Fatalf("panelIntID for short uid returned 0")
	}
}

func TestRefID(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA"}
	for in, want := range cases {
		if got := refID(in); got != want {
			t.Errorf("refID(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderNilDashboard(t *testing.T) {
	if _, err := Render(nil); err == nil {
		t.Fatalf("expected error for nil dashboard")
	}
}

// TestPanelIDsUniqueAcrossAllGoldens guards against the pre-fix
// regression where panelIntID's int32 modulo produced cross-UID
// collisions in every committed golden (e.g. service-basic id
// 895859167 mapped from both `admin_actions_total` and
// `http_requests_total`). With the wider 53-bit-safe space + full-UID
// FNV fold this test must stay green for every committed golden.
//
// Grafana relies on per-dashboard panel.id uniqueness for deep links
// and panel-level annotations, so a regression here is a real
// correctness bug — not a cosmetic concern.
func TestPanelIDsUniqueAcrossAllGoldens(t *testing.T) {
	t.Parallel()
	goldens := []string{
		"../../../testdata/goldens/service-basic/dashboard.json",
		"../../../testdata/goldens/service-realistic/dashboard.json",
		"../../../testdata/goldens/infra-basic/dashboard.json",
		"../../../testdata/goldens/infra-realistic/dashboard.json",
		"../../../testdata/goldens/k8s-basic/dashboard.json",
		"../../../testdata/goldens/k8s-realistic/dashboard.json",
	}
	for _, g := range goldens {
		g := g
		t.Run(g, func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(g)
			if err != nil {
				t.Fatalf("read %s: %v", g, err)
			}
			var doc struct {
				Panels []struct {
					ID    int64  `json:"id"`
					Type  string `json:"type"`
					Title string `json:"title"`
				} `json:"panels"`
			}
			if err := json.Unmarshal(body, &doc); err != nil {
				t.Fatalf("parse %s: %v", g, err)
			}
			seen := map[int64]string{}
			for _, p := range doc.Panels {
				if p.Type == "row" {
					continue
				}
				if prev, dup := seen[p.ID]; dup {
					t.Errorf("collision in %s: id=%d shared by %q and %q",
						g, p.ID, prev, p.Title)
				}
				seen[p.ID] = p.Title
			}
		})
	}
}
