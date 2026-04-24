package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// infraCPURecipe emits a node-level CPU utilization panel from
// node_cpu_seconds_total. The idle-mode rate subtracted from 1 gives the
// utilization ratio.
type infraCPURecipe struct{}

// NewInfraCPU returns the infra_cpu recipe.
func NewInfraCPU() Recipe { return &infraCPURecipe{} }

func (infraCPURecipe) Name() string    { return "infra_cpu" }
func (infraCPURecipe) Section() string { return "cpu" }

// Match keys on the well-known node_exporter CPU counter by name. Label
// presence is not a match prerequisite — see service_http_rate for the
// same pattern.
func (r infraCPURecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	return m.Descriptor.Name == "node_cpu_seconds_total"
}

func (r infraCPURecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		expr := fmt.Sprintf(
			`1 - avg by (instance) (rate(%s{mode="idle"}[%s]))`,
			m.Descriptor.Name, defaultRateWindow,
		)
		return []ir.Panel{{
			Title: fmt.Sprintf("CPU utilization: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "percentunit",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: "{{instance}}",
				Unit:         "percentunit",
			}},
			Confidence: 0.8,
			Rationale: fmt.Sprintf(
				"CPU counter %q; 1 minus the idle-mode rate over %s yields CPU utilization per instance.",
				m.Descriptor.Name, defaultRateWindow,
			),
		}}
	}
	return nil
}
