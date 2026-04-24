// Package recipes — infra_load recipe.
//
// Operator question: is the kernel's runqueue saturated on this host?
// Load averages above the number of logical CPUs indicate a saturated
// scheduler; a rising 15-minute average with a flat 1-minute average
// suggests the congestion is resolving.
//
// Signals: gauges node_load1, node_load5, node_load15. All three are
// canonical node_exporter gauges published unconditionally. One panel is
// emitted per matched metric (up to three), each showing avg by (instance).
//
// Aggregation rationale: avg by (instance) is preferred over sum so that
// multi-sample scrapes (unusual but possible) do not artificially double the
// value. A single host reports exactly one sample per scrape window.
//
// Confidence 0.90: exact metric name match on a well-known node_exporter
// gauge family with no known look-alikes. The bare name "node_load" does not
// exist in practice; similarly, node_memory_* and node_cpu_* gauges share
// the node_ prefix but have structurally different names and must not match.
//
// Known look-alikes that must NOT match:
//   - "node_load" (bare) — not exported by node_exporter; no-op rejection.
//   - "node_memory_MemAvailable_bytes" — different family, different section.
//   - Any counter whose name starts with node_load (none exist in practice,
//     but a counter type is rejected by the Match gate).
package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// loadWindows maps each node_load* metric name to its human-readable window.
var loadWindows = map[string]string{
	"node_load1":  "1",
	"node_load5":  "5",
	"node_load15": "15",
}

// infraLoadRecipe emits one panel per matched load-average gauge, showing the
// average runqueue length over 1, 5, and 15-minute windows per host.
type infraLoadRecipe struct{}

// NewInfraLoad returns the infra_load recipe.
func NewInfraLoad() Recipe { return &infraLoadRecipe{} }

func (infraLoadRecipe) Name() string    { return "infra_load" }
func (infraLoadRecipe) Section() string { return "cpu" }

// Match returns true for any of the three node_exporter load-average gauges.
// Counters with the same names are rejected by the type check.
func (r infraLoadRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	_, ok := loadWindows[m.Descriptor.Name]
	return ok
}

func (r infraLoadRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		window := loadWindows[m.Descriptor.Name]
		expr := fmt.Sprintf("avg by (instance) (%s)", m.Descriptor.Name)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("Load %sm: %s", window, m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: "{{instance}}",
				Unit:         "",
			}},
			Confidence: 0.90,
			Rationale: fmt.Sprintf(
				"node_exporter load gauge %q; avg by (instance) gives the %s-minute runqueue average per host.",
				m.Descriptor.Name, window,
			),
		})
	}
	return panels
}
