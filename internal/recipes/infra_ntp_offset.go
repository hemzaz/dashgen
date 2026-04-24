package recipes

// infra_ntp_offset — NTP clock-drift panel for the infra profile.
//
// Operator question: is this host's clock drifting from the reference clock?
//
// Signals:
//   - MetricType gauge.
//   - Name exactly "node_timex_offset_seconds" — the node_exporter timex
//     subsystem reports the current kernel-estimated offset from the reference
//     clock in seconds. Name equality is intentional: this is the canonical
//     node_exporter metric for NTP offset and the name is unambiguous.
//
// Grouping: {instance} — NTP offset is a per-host property; job is not
// relevant here because every node_exporter instance reports its own clock
// state independently.
//
// Aggregation: max by (instance) rather than avg. When multiple scrape
// targets share an instance label (rare but possible with federation), max
// surfaces the worst-drifting sample. avg would mask pathological drift.
//
// Confidence 0.90. Exact metric-name match with no ambiguity; this sits in
// the 0.90–0.95 tier for exact-name recipes.
//
// Known look-alikes that must NOT match:
//   - node_ntp_stratum: a different node_exporter gauge (NTP stratum level,
//     not offset); name does not match.
//   - Any gauge whose name merely contains the substring "offset" but is not
//     exactly "node_timex_offset_seconds".
//   - A counter named "node_timex_offset_seconds" (wrong type).

import (
	"fmt"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type infraNTPOffsetRecipe struct{}

// NewInfraNTPOffset returns the infra_ntp_offset recipe.
func NewInfraNTPOffset() Recipe { return &infraNTPOffsetRecipe{} }

func (infraNTPOffsetRecipe) Name() string    { return "infra_ntp_offset" }
func (infraNTPOffsetRecipe) Section() string { return "overview" }

// Match accepts only the exact gauge "node_timex_offset_seconds".
func (r infraNTPOffsetRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeGauge && m.Descriptor.Name == "node_timex_offset_seconds"
}

func (r infraNTPOffsetRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m, "instance")
		expr := fmt.Sprintf("max by (%s) (%s)", strings.Join(group, ", "), m.Descriptor.Name)
		panels = append(panels, ir.Panel{
			Title: "NTP offset",
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "s",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "s",
			}},
			Confidence: 0.90,
			Rationale: fmt.Sprintf(
				"Gauge %q aggregated with max by (%s); max rather than avg because it surfaces "+
					"the worst-drifting sample when multiple targets share an instance label.",
				m.Descriptor.Name, strings.Join(group, ", "),
			),
		})
	}
	return panels
}
