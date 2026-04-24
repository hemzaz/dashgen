package recipes

// k8s_hpa_scaling recipe
//
// Operator question:
//   Are Horizontal Pod Autoscalers hitting their min/max ceilings, oscillating
//   between replica counts, or stuck at a boundary?
//
// Canonical signals:
//   - kube_horizontalpodautoscaler_status_current_replicas (gauge, kube-state-metrics)
//     Labels: namespace, horizontalpodautoscaler
//   - kube_horizontalpodautoscaler_status_desired_replicas (gauge, kube-state-metrics)
//     Labels: namespace, horizontalpodautoscaler
//   Both are required. If desired_replicas is absent from the snapshot the recipe
//   emits no panels, because a lone current count has no autoscaling baseline.
//
// Aggregation shape:
//   Two query candidates in a single panel:
//     1. sum by (namespace, horizontalpodautoscaler) (kube_horizontalpodautoscaler_status_current_replicas)
//        — current (actual) replica count
//     2. sum by (namespace, horizontalpodautoscaler) (kube_horizontalpodautoscaler_status_desired_replicas)
//        — desired replica count as computed by the HPA controller
//   Side-by-side display of current vs. desired reveals oscillation visually.
//   A ratio is less meaningful here because current==desired is the normal steady
//   state, and the interesting signal is divergence duration/amplitude, not a
//   fraction. "sum by" deduplicates across multiple kube-state-metrics shards.
//
// Confidence: 0.90
//   The primary signal (kube_horizontalpodautoscaler_status_current_replicas) is an
//   exact metric-name match to a well-known kube-state-metrics series. The pair
//   requirement with desired_replicas prevents false positives from incomplete scrape
//   configurations.
//
// Known look-alikes that must NOT match:
//   - kube_horizontalpodautoscaler_status_desired_replicas (desired alone, not current)
//   - kube_deployment_spec_replicas (deployment resource, different controller)
//   - any counter with the same base name (type guard prevents this)

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type k8sHPAScalingRecipe struct{}

// NewK8sHPAScaling returns the k8s_hpa_scaling recipe.
func NewK8sHPAScaling() Recipe { return &k8sHPAScalingRecipe{} }

func (k8sHPAScalingRecipe) Name() string    { return "k8s_hpa_scaling" }
func (k8sHPAScalingRecipe) Section() string { return "workloads" }

func (r k8sHPAScalingRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "kube_horizontalpodautoscaler_status_current_replicas"
}

func (r k8sHPAScalingRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}

	// Verify both required metrics are present in the snapshot.
	var hasCurrent, hasDesired bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "kube_horizontalpodautoscaler_status_current_replicas":
			hasCurrent = true
		case "kube_horizontalpodautoscaler_status_desired_replicas":
			hasDesired = true
		}
	}
	if !hasCurrent || !hasDesired {
		return nil
	}

	const (
		metricCurrent = "kube_horizontalpodautoscaler_status_current_replicas"
		metricDesired = "kube_horizontalpodautoscaler_status_desired_replicas"
		groupBy       = "namespace, horizontalpodautoscaler"
	)

	currentExpr := fmt.Sprintf("sum by (%s) (%s)", groupBy, metricCurrent)
	desiredExpr := fmt.Sprintf("sum by (%s) (%s)", groupBy, metricDesired)

	return []ir.Panel{{
		Title: "HPA scaling (current vs desired)",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "short",
		Queries: []ir.QueryCandidate{
			{
				Expr:         currentExpr,
				LegendFormat: "current {{namespace}} {{horizontalpodautoscaler}}",
				Unit:         "short",
			},
			{
				Expr:         desiredExpr,
				LegendFormat: "desired {{namespace}} {{horizontalpodautoscaler}}",
				Unit:         "short",
			},
		},
		Confidence: 0.90,
		Rationale: fmt.Sprintf(
			"kube-state-metrics pair %q + %q; current and desired replica counts per namespace+HPA.",
			metricCurrent, metricDesired,
		),
	}}
}
