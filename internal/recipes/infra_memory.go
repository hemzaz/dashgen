package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// infraMemoryRecipe emits a memory-used-ratio panel computed from the
// node_memory_MemAvailable / node_memory_MemTotal gauges.
type infraMemoryRecipe struct{}

// NewInfraMemory returns the infra_memory recipe.
func NewInfraMemory() Recipe { return &infraMemoryRecipe{} }

func (infraMemoryRecipe) Name() string    { return "infra_memory" }
func (infraMemoryRecipe) Section() string { return "memory" }

// Match on either half of the pair: the panel is emitted once, keyed off
// the Available gauge, but we still want classify-time visibility on Total.
func (r infraMemoryRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	switch m.Descriptor.Name {
	case "node_memory_MemAvailable_bytes", "node_memory_MemTotal_bytes":
		return true
	}
	return false
}

func (r infraMemoryRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	// Only emit a panel when both metrics are present. This avoids an
	// expression that would refer to a metric the target does not publish.
	var hasAvail, hasTotal bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "node_memory_MemAvailable_bytes":
			hasAvail = true
		case "node_memory_MemTotal_bytes":
			hasTotal = true
		}
	}
	if !hasAvail || !hasTotal {
		return nil
	}
	expr := "1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)"
	return []ir.Panel{{
		Title: "Memory used ratio",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "percentunit",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}}",
			Unit:         "percentunit",
		}},
		Confidence: 0.8,
		Rationale: fmt.Sprintf(
			"node_exporter memory gauges: 1 - (MemAvailable / MemTotal) yields used ratio per instance.",
		),
	}}
}
