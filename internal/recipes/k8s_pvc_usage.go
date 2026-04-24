package recipes

// k8s_pvc_usage — answers: are any persistent volumes running out of space?
//
// Operator question:
//
//	Which PVCs are near capacity, and by how much?
//
// Canonical signals:
//   - kubelet_volume_stats_available_bytes (gauge) — bytes available on the PV.
//   - kubelet_volume_stats_capacity_bytes  (gauge) — total capacity of the PV.
//     Both are emitted by the kubelet for every bound PVC; labels always include
//     namespace and persistentvolumeclaim.
//
// Aggregation shape:
//
//	1 - (sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_available_bytes)
//	     / sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_capacity_bytes))
//
//	The `1 -` inversion converts the "fraction available" into "fraction used",
//	which is the intuitive saturation view an operator wants to alert on.
//	sum-by rather than a bare division is defensive: if multiple targets
//	report for the same PVC (unlikely but possible in multi-kubelet setups)
//	we still get a sensible single-series result.
//
// Confidence: 0.85 — both metric names are stable kubelet API surface and the
// label set is unambiguous; the slight discount from 0.90 reflects that the
// pair-presence check is required (a snapshot with only one half should not emit).
//
// Known look-alikes that must NOT match:
//   - kubelet_volume_stats_used_bytes — a different signal from the same family;
//     Match keys only on available_bytes so used_bytes never triggers this recipe.
//   - container_fs_* — cAdvisor filesystem metrics that carry similar label names
//     but a completely different metric-name prefix.

import (
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type k8sPVCUsageRecipe struct{}

// NewK8sPVCUsage returns the k8s_pvc_usage recipe.
func NewK8sPVCUsage() Recipe { return &k8sPVCUsageRecipe{} }

func (k8sPVCUsageRecipe) Name() string    { return "k8s_pvc_usage" }
func (k8sPVCUsageRecipe) Section() string { return "resources" }

// Match returns true only for kubelet_volume_stats_available_bytes.
// The capacity counterpart is checked in BuildPanels, not here.
func (r k8sPVCUsageRecipe) Match(m ClassifiedMetricView) bool {
	return m.Descriptor.Name == "kubelet_volume_stats_available_bytes"
}

func (r k8sPVCUsageRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	var hasAvail, hasCapacity bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "kubelet_volume_stats_available_bytes":
			hasAvail = true
		case "kubelet_volume_stats_capacity_bytes":
			hasCapacity = true
		}
	}
	if !hasAvail || !hasCapacity {
		return nil
	}
	const expr = `1 - (sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_available_bytes) / sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_capacity_bytes))`
	return []ir.Panel{{
		Title: "PVC used ratio",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "percentunit",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{namespace}} {{persistentvolumeclaim}}",
			Unit:         "percentunit",
		}},
		Confidence: 0.85,
		Rationale:  "kubelet PVC gauges: 1 - (available / capacity) yields used ratio per namespace+PVC.",
	}}
}
