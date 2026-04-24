package recipes

// k8sNodeConditionsRecipe answers: are any nodes not Ready or reporting
// memory/disk/pid pressure?
//
// Canonical signals:
//   - Metric name: kube_node_status_condition (exact match)
//   - Type: gauge, valued 0 or 1
//   - Relevant labels: node, condition, status
//
// Aggregation shape:
//   max by (node, condition) (kube_node_status_condition{condition="<X>", status="true"})
//
// max is used rather than sum because kube-state-metrics emits multiple series
// per (node, condition) combination — one row each for status=true/false/unknown.
// We care only about the true row; max across the set collapses duplicates
// without inflating counts.
//
// Four candidates are emitted in a single panel, one per condition:
// NotReady, MemoryPressure, DiskPressure, PIDPressure. The order is fixed and
// deterministic so golden files and integration tests are stable.
//
// Confidence: 0.90 — exact metric name match; no look-alike risk from other
// kube-state-metrics gauges because none share this name.
//
// Known look-alikes that must NOT match:
//   - kube_pod_status_phase: different metric family, different grouping
//   - Any gauge not named kube_node_status_condition

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// k8sNodeConditionsRecipe emits a single panel showing the four key node
// conditions sourced from kube-state-metrics' kube_node_status_condition gauge.
type k8sNodeConditionsRecipe struct{}

// NewK8sNodeConditions returns the k8s_node_conditions recipe.
func NewK8sNodeConditions() Recipe { return &k8sNodeConditionsRecipe{} }

func (k8sNodeConditionsRecipe) Name() string    { return "k8s_node_conditions" }
func (k8sNodeConditionsRecipe) Section() string { return "resources" }

func (r k8sNodeConditionsRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "kube_node_status_condition"
}

// nodeConditions is the fixed, deterministic order required by the spec.
var nodeConditions = []string{"NotReady", "MemoryPressure", "DiskPressure", "PIDPressure"}

func (r k8sNodeConditionsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		queries := make([]ir.QueryCandidate, 0, len(nodeConditions))
		for _, cond := range nodeConditions {
			expr := fmt.Sprintf(
				`max by (node, condition) (%s{condition="%s", status="true"})`,
				m.Descriptor.Name, cond,
			)
			queries = append(queries, ir.QueryCandidate{
				Expr:         expr,
				LegendFormat: "{{node}} {{condition}}",
				Unit:         "",
			})
		}
		return []ir.Panel{{
			Title:      "Node conditions",
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "",
			Queries:    queries,
			Confidence: 0.90,
			Rationale: fmt.Sprintf(
				"kube-state-metrics gauge %q; max by node+condition for each of %v shows which nodes are unhealthy.",
				m.Descriptor.Name, nodeConditions,
			),
		}}
	}
	return nil
}
