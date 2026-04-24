package recipes

// k8s_deployment_availability recipe
//
// Operator question:
//   How many replicas are available versus desired for each deployment,
//   and what fraction of desired capacity is currently serving traffic?
//
// Canonical signals:
//   - kube_deployment_status_replicas_available (gauge, kube-state-metrics)
//     Labels: namespace, deployment
//   - kube_deployment_spec_replicas (gauge, kube-state-metrics)
//     Labels: namespace, deployment
//   Both are required. If spec_replicas is absent from the snapshot the recipe
//   emits no panels, because a lone availability count has no baseline.
//
// Aggregation shape:
//   Three query candidates in a single panel:
//     1. sum by (namespace, deployment) (kube_deployment_spec_replicas)
//        — desired replica count
//     2. sum by (namespace, deployment) (kube_deployment_status_replicas_available)
//        — available replica count
//     3. ratio of available / desired
//        — fraction in [0,1]; useful for alerting thresholds
//   "sum by" is appropriate here because a deployment may be observed by
//   multiple kube-state-metrics shards in large clusters; summing deduplicates
//   any double-counting that would arise from count or last_over_time.
//
// Confidence: 0.90
//   The primary signal (kube_deployment_status_replicas_available) is an exact
//   metric-name match to a well-known kube-state-metrics series. The pair
//   requirement with spec_replicas prevents false positives from incomplete
//   scrape configurations.
//
// Known look-alikes that must NOT match:
//   - kube_deployment_status_replicas (total, not available; different name)
//   - kube_deployment_status_replicas_unavailable (inverse of available)
//   - kube_replicaset_status_replicas_available (ReplicaSet, not Deployment)

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type k8sDeploymentAvailabilityRecipe struct{}

// NewK8sDeploymentAvailability returns the k8s_deployment_availability recipe.
func NewK8sDeploymentAvailability() Recipe { return &k8sDeploymentAvailabilityRecipe{} }

func (k8sDeploymentAvailabilityRecipe) Name() string    { return "k8s_deployment_availability" }
func (k8sDeploymentAvailabilityRecipe) Section() string { return "workloads" }

func (r k8sDeploymentAvailabilityRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "kube_deployment_status_replicas_available"
}

func (r k8sDeploymentAvailabilityRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}

	// Verify both required metrics are present in the snapshot.
	var hasAvailable, hasSpec bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "kube_deployment_status_replicas_available":
			hasAvailable = true
		case "kube_deployment_spec_replicas":
			hasSpec = true
		}
	}
	if !hasAvailable || !hasSpec {
		return nil
	}

	const (
		metricAvailable = "kube_deployment_status_replicas_available"
		metricSpec      = "kube_deployment_spec_replicas"
		groupBy         = "namespace, deployment"
	)

	desiredExpr := fmt.Sprintf("sum by (%s) (%s)", groupBy, metricSpec)
	availableExpr := fmt.Sprintf("sum by (%s) (%s)", groupBy, metricAvailable)
	ratioExpr := fmt.Sprintf(
		"sum by (%s) (%s) / sum by (%s) (%s)",
		groupBy, metricAvailable, groupBy, metricSpec,
	)

	return []ir.Panel{{
		Title: "Deployment availability",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "short",
		Queries: []ir.QueryCandidate{
			{
				Expr:         desiredExpr,
				LegendFormat: "desired {{namespace}} {{deployment}}",
				Unit:         "short",
			},
			{
				Expr:         availableExpr,
				LegendFormat: "available {{namespace}} {{deployment}}",
				Unit:         "short",
			},
			{
				Expr:         ratioExpr,
				LegendFormat: "ratio {{namespace}} {{deployment}}",
				Unit:         "short",
			},
		},
		Confidence: 0.90,
		Rationale: fmt.Sprintf(
			"kube-state-metrics pair %q + %q; desired, available, and availability ratio per namespace+deployment.",
			metricSpec, metricAvailable,
		),
	}}
}
