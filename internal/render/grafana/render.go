// Package grafana renders an IR dashboard to Grafana JSON.
//
// The output targets Grafana dashboard schema version 39 (Grafana 10.x). The
// renderer is a dumb translator: it consumes the IR and produces byte-stable
// JSON. It does not know about Prometheus, classification, recipes, or
// validation.
//
// Determinism contract: identical IR inputs must produce byte-identical JSON
// across runs. Field ordering is enforced by explicit struct tags, and the
// panel and target ordering follow IR slice order exactly. Map iteration is
// never used in this package.
package grafana

import (
	"encoding/json"
	"fmt"
	"strings"

	"dashgen/internal/ir"
)

// schemaVersion pins the Grafana dashboard schema version this renderer
// targets. 39 corresponds to Grafana 10.x and is the lowest reasonable
// version that carries the modern `timeseries` panel type as first-class.
const schemaVersion = 39

// panelIDSpace is the modulus used to fold a PanelUID into a stable
// non-zero integer panel ID. It's the largest prime under 2^53, which
// is JavaScript's Number.MAX_SAFE_INTEGER — Grafana's frontend
// stringifies panel IDs through JS, and 53 bits keeps the value safe
// from precision loss. The previous value (2^31 - 1) produced
// observable cross-UID collisions in every committed golden because
// the SHA-256[:16] prefix folded down to int32 had only ~30 effective
// bits of identity. The wider space + full-UID FNV fold below
// eliminates those collisions.
const panelIDSpace uint64 = 9007199254740881 // largest prime < 2^53

// gridWidth and gridHeight describe the uniform panel geometry used in v0.1.
// Grafana dashboards are 24 units wide; two panels per row.
const (
	gridWidth  = 12
	gridHeight = 8
)

// rowHeight is the height Grafana reserves for a row header panel.
const rowHeight = 1

// Render produces Grafana dashboard JSON for the given IR.
func Render(d *ir.Dashboard) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("grafana: nil dashboard")
	}

	doc := buildDashboard(d)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("grafana: marshal dashboard: %w", err)
	}
	return out, nil
}

// dashboardDoc is the top-level Grafana document. Field order here is the
// JSON field order emitted by encoding/json; do not reorder.
type dashboardDoc struct {
	Annotations          annotations   `json:"annotations"`
	Editable             bool          `json:"editable"`
	FiscalYearStartMonth int           `json:"fiscalYearStartMonth"`
	GraphTooltip         int           `json:"graphTooltip"`
	ID                   *int          `json:"id"`
	Links                []any         `json:"links"`
	LiveNow              bool          `json:"liveNow"`
	Panels               []panelDoc    `json:"panels"`
	Refresh              string        `json:"refresh"`
	SchemaVersion        int           `json:"schemaVersion"`
	Style                string        `json:"style"`
	Tags                 []string      `json:"tags"`
	Templating           templatingDoc `json:"templating"`
	Time                 timeRange     `json:"time"`
	TimePicker           timePicker    `json:"timepicker"`
	Timezone             string        `json:"timezone"`
	Title                string        `json:"title"`
	UID                  string        `json:"uid"`
	Version              int           `json:"version"`
	WeekStart            string        `json:"weekStart"`
}

type annotations struct {
	List []any `json:"list"`
}

type templatingDoc struct {
	List []templateVar `json:"list"`
}

type templateVar struct {
	Current     map[string]any `json:"current"`
	Hide        int            `json:"hide"`
	Label       string         `json:"label,omitempty"`
	Name        string         `json:"name"`
	Options     []any          `json:"options"`
	Query       string         `json:"query"`
	SkipURLSync bool           `json:"skipUrlSync"`
	Type        string         `json:"type"`
}

type timeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type timePicker struct{}

// panelDoc is the common shape used for both row panels and data panels.
// Fields that do not apply to row panels (targets, fieldConfig, unit) are
// omitted via `omitempty`. "Collapsed" and "panels" only apply to rows.
type panelDoc struct {
	ID          int            `json:"id"`
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Datasource  *datasourceRef `json:"datasource,omitempty"`
	FieldConfig *fieldConfig   `json:"fieldConfig,omitempty"`
	GridPos     gridPos        `json:"gridPos"`
	Options     map[string]any `json:"options,omitempty"`
	Targets     []targetDoc    `json:"targets,omitempty"`
	Collapsed   *bool          `json:"collapsed,omitempty"`
	Panels      []panelDoc     `json:"panels,omitempty"`
}

type datasourceRef struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

type fieldConfig struct {
	Defaults  fieldDefaults `json:"defaults"`
	Overrides []any         `json:"overrides"`
}

type fieldDefaults struct {
	Unit string `json:"unit"`
}

type gridPos struct {
	H int `json:"h"`
	W int `json:"w"`
	X int `json:"x"`
	Y int `json:"y"`
}

type targetDoc struct {
	Datasource   datasourceRef `json:"datasource"`
	Expr         string        `json:"expr"`
	LegendFormat string        `json:"legendFormat"`
	RefID        string        `json:"refId"`
}

// buildDashboard assembles the full dashboard document from the IR.
func buildDashboard(d *ir.Dashboard) dashboardDoc {
	panels := buildPanels(d)

	return dashboardDoc{
		Annotations:          annotations{List: []any{}},
		Editable:             false,
		FiscalYearStartMonth: 0,
		GraphTooltip:         0,
		ID:                   nil,
		Links:                []any{},
		LiveNow:              false,
		Panels:               panels,
		Refresh:              "",
		SchemaVersion:        schemaVersion,
		Style:                "dark",
		Tags:                 []string{"dashgen", d.Profile},
		Templating:           buildTemplating(),
		Time:                 timeRange{From: "now-6h", To: "now"},
		TimePicker:           timePicker{},
		Timezone:             "",
		Title:                d.Title,
		UID:                  d.UID,
		Version:              1,
		WeekStart:            "",
	}
}

// buildTemplating emits the single `$datasource` variable we support in
// v0.1. The IR's Variables slice is intentionally ignored for field-for-
// field control of the Grafana shape; the IR carries names but not the
// full Grafana variable schema.
func buildTemplating() templatingDoc {
	return templatingDoc{
		List: []templateVar{
			{
				Current:     map[string]any{},
				Hide:        0,
				Name:        "datasource",
				Options:     []any{},
				Query:       "prometheus",
				SkipURLSync: false,
				Type:        "datasource",
			},
		},
	}
}

// buildPanels lays out rows and their member panels in row-major order.
// Each row is emitted as a Grafana row panel, followed by its data panels.
// Data panels alternate between x=0 and x=12, advancing y by gridHeight
// every two panels. Rows advance y by rowHeight.
func buildPanels(d *ir.Dashboard) []panelDoc {
	out := make([]panelDoc, 0)
	y := 0
	for _, row := range d.Rows {
		collapsed := false
		rowPanel := panelDoc{
			ID:        rowID(row.Title, y),
			Type:      "row",
			Title:     row.Title,
			Collapsed: &collapsed,
			GridPos:   gridPos{H: rowHeight, W: 24, X: 0, Y: y},
			Panels:    []panelDoc{},
		}
		out = append(out, rowPanel)
		y += rowHeight

		rowY := y
		for idx, p := range row.Panels {
			x := 0
			if idx%2 == 1 {
				x = gridWidth
			}
			py := rowY + (idx/2)*gridHeight
			out = append(out, buildPanel(p, x, py))
		}
		if len(row.Panels) > 0 {
			// Advance y past the last row of panels (rounded up).
			rows := (len(row.Panels) + 1) / 2
			y = rowY + rows*gridHeight
		}
	}
	return out
}

// buildPanel converts a single IR panel into a Grafana panel document.
// Only Accept and AcceptWithWarning queries are emitted as targets.
// Refused queries are surfaced in the panel description so reviewers can
// see what was dropped without digging into the rationale file.
func buildPanel(p ir.Panel, x, y int) panelDoc {
	targets := make([]targetDoc, 0, len(p.Queries))
	refused := make([]string, 0)
	refIdx := 0
	for _, q := range p.Queries {
		if q.Verdict == ir.VerdictRefuse {
			refused = append(refused, q.Expr)
			continue
		}
		targets = append(targets, targetDoc{
			Datasource:   datasourceRef{Type: "prometheus", UID: "$datasource"},
			Expr:         q.Expr,
			LegendFormat: q.LegendFormat,
			RefID:        refID(refIdx),
		})
		refIdx++
	}

	description := ""
	if len(refused) > 0 {
		description = "Omitted queries: " + strings.Join(refused, "; ")
	}

	return panelDoc{
		ID:          panelIntID(p.UID),
		Type:        panelType(p.Kind),
		Title:       p.Title,
		Description: description,
		Datasource:  &datasourceRef{Type: "prometheus", UID: "$datasource"},
		FieldConfig: &fieldConfig{
			Defaults:  fieldDefaults{Unit: p.Unit},
			Overrides: []any{},
		},
		GridPos: gridPos{H: gridHeight, W: gridWidth, X: x, Y: y},
		Targets: targets,
	}
}

// panelType maps IR panel kinds onto Grafana panel type strings.
func panelType(k ir.PanelKind) string {
	switch k {
	case ir.PanelKindStat:
		return "stat"
	case ir.PanelKindGraph, ir.PanelKindTimeSeries:
		return "timeseries"
	default:
		return "timeseries"
	}
}

// panelIntID derives a stable non-zero integer panel ID from a PanelUID.
// It folds the entire UID through an FNV-1a-style mix so every byte
// influences the output, then reduces modulo panelIDSpace. Grafana
// treats id=0 as unset, so we substitute 1 in that pathological case.
//
// Compared to the v0.1 scheme (parse first 8 hex chars, modulo 2^31-1),
// this widens the effective ID space from ~31 bits to ~53 bits and
// uses every byte of the UID, eliminating cross-UID collisions
// observed in every committed golden.
func panelIntID(uid string) int {
	id := int(fnv1a(uid) % panelIDSpace)
	if id == 0 {
		id = 1
	}
	return id
}

// rowID derives a stable integer ID for a row panel from its title and
// y position. Using y keeps distinct rows distinct even when titles
// repeat. Same fold + modulo scheme as panelIntID.
func rowID(title string, y int) int {
	acc := fnv1a(title)
	acc ^= uint64(y)
	acc *= 1099511628211
	id := int(acc % panelIDSpace)
	if id == 0 {
		id = 1
	}
	return id
}

// fnv1a folds s through the FNV-1a 64-bit hash. Pure function; the
// constants are the standard FNV offset basis and prime so a future
// reader can verify the implementation against any FNV reference.
func fnv1a(s string) uint64 {
	var acc uint64 = 1469598103934665603 // FNV offset basis
	for i := 0; i < len(s); i++ {
		acc ^= uint64(s[i])
		acc *= 1099511628211 // FNV prime
	}
	return acc
}

// refID returns Grafana's A/B/C-style reference id for target index i.
// For i >= 26 we fall through to double letters (AA, AB, ...); v0.1 never
// emits that many targets per panel in practice.
func refID(i int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if i < 26 {
		return string(letters[i])
	}
	return string(letters[i/26-1]) + string(letters[i%26])
}
