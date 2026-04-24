package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// k8sPodHealthRecipe emits a pods-by-phase panel from kube-state-metrics'
// kube_pod_status_phase gauge.
type k8sPodHealthRecipe struct{}

// NewK8sPodHealth returns the k8s_pod_health recipe.
func NewK8sPodHealth() Recipe { return &k8sPodHealthRecipe{} }

func (k8sPodHealthRecipe) Name() string    { return "k8s_pod_health" }
func (k8sPodHealthRecipe) Section() string { return "pods" }

func (r k8sPodHealthRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "kube_pod_status_phase"
}

func (r k8sPodHealthRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		expr := fmt.Sprintf("sum by (namespace, phase) (%s)", m.Descriptor.Name)
		return []ir.Panel{{
			Title: fmt.Sprintf("Pods by phase: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "short",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: "{{namespace}} {{phase}}",
				Unit:         "short",
			}},
			Confidence: 0.8,
			Rationale: fmt.Sprintf(
				"kube-state-metrics gauge %q; summed by namespace+phase gives pod counts per phase.",
				m.Descriptor.Name,
			),
		}}
	}
	return nil
}
