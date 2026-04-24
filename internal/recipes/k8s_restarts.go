package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// k8sRestartsRecipe emits a container-restart-rate panel from the
// kube_pod_container_status_restarts_total counter. A 15m window is used
// because restarts are infrequent and the default 5m rate window produces
// noisy near-zero values.
type k8sRestartsRecipe struct{}

// NewK8sRestarts returns the k8s_restarts recipe.
func NewK8sRestarts() Recipe { return &k8sRestartsRecipe{} }

func (k8sRestartsRecipe) Name() string    { return "k8s_restarts" }
func (k8sRestartsRecipe) Section() string { return "workloads" }

// restartsRateWindow is intentionally longer than defaultRateWindow because
// restart counters increment rarely; 5m regularly yields a flat zero series.
const restartsRateWindow = "15m"

func (r k8sRestartsRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	return m.Descriptor.Name == "kube_pod_container_status_restarts_total"
}

func (r k8sRestartsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		expr := fmt.Sprintf(
			"sum by (namespace, pod, container) (rate(%s[%s]))",
			m.Descriptor.Name, restartsRateWindow,
		)
		return []ir.Panel{{
			Title: fmt.Sprintf("Container restart rate: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "short",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: "{{namespace}} {{pod}} {{container}}",
				Unit:         "short",
			}},
			Confidence: 0.8,
			Rationale: fmt.Sprintf(
				"kube-state-metrics counter %q; rate over %s grouped by namespace+pod+container.",
				m.Descriptor.Name, restartsRateWindow,
			),
		}}
	}
	return nil
}
