// Package recipes contains all panel-generation recipes for dashgen.
//
// k8s_oom_kills — OOM kills panel
//
// Operator question: Are pods being OOMKilled?
//
// Canonical signal: kube_pod_container_status_terminated_reason (gauge,
// kube-state-metrics). The metric is keyed by the "reason" label; this recipe
// filters to reason="OOMKilled" and sums the gauge by (namespace, pod).
// A value > 0 means at least one container in the pod is currently sitting in
// an OOMKilled terminated state.
//
// Aggregation shape: sum by (namespace, pod) counts the OOMKilled containers
// per pod namespace-pair, collapsing the container dimension. This is the
// coarsest granularity that still lets an operator identify which workload to
// investigate.
//
// Confidence 0.90: the metric name is exact and the reason label is canonical
// kube-state-metrics vocabulary — no look-alike risk from other exporters.
//
// Known look-alikes that must NOT match:
//   - kube_pod_container_status_ready (different suffix, different semantics)
//   - kube_pod_container_status_restarts_total (counter, not this gauge)
package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type k8sOOMKillsRecipe struct{}

// NewK8sOOMKills returns the k8s_oom_kills recipe.
func NewK8sOOMKills() Recipe { return &k8sOOMKillsRecipe{} }

func (k8sOOMKillsRecipe) Name() string    { return "k8s_oom_kills" }
func (k8sOOMKillsRecipe) Section() string { return "pods" }

func (r k8sOOMKillsRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "kube_pod_container_status_terminated_reason"
}

func (r k8sOOMKillsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		expr := fmt.Sprintf(
			`sum by (namespace, pod) (%s{reason="OOMKilled"})`,
			m.Descriptor.Name,
		)
		return []ir.Panel{{
			Title: "OOM kills",
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "short",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: "{{namespace}} {{pod}}",
				Unit:         "short",
			}},
			Confidence: 0.90,
			Rationale: fmt.Sprintf(
				"kube-state-metrics gauge %q filtered to reason=OOMKilled; sum by namespace+pod counts OOM-terminated containers per pod.",
				m.Descriptor.Name,
			),
		}}
	}
	return nil
}
