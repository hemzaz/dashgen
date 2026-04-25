// k8s_scheduler_latency — p50/p95/p99 kube-scheduler scheduling attempt latency.
//
// Operator question: how long does the kube-scheduler take to attempt
// scheduling a pod, broken down by result? Spikes here indicate scheduling
// pressure — the cluster may be undersized or have misconfigured affinity rules.
//
// Signals:
//   - MetricType histogram.
//   - Name exactly "scheduler_scheduling_attempt_duration_seconds" — the
//     canonical histogram exported by kube-scheduler. We match by name
//     equality rather than trait alone because a generic latency_histogram
//     trait would also fire on unrelated histograms (apiserver, etcd, etc.).
//     Both the bare base name and the _bucket suffix are accepted since
//     Prometheus metadata may return either form.
//   - Label "result" must be present; it carries the scheduling outcome
//     (scheduled, unschedulable, error) which is the key breakdown dimension.
//
// Grouping: {result, le} — le is required by histogram_quantile; result gives
// the scheduling outcome breakdown. job/instance are added when present via
// safeGroupLabels.
//
// Query shape: p50/p95/p99 via histogram_quantile on the _bucket series.
//
// Confidence 0.85 — exact metric name match; slightly below apiserver (0.90)
// because the scheduler is less universally deployed than the apiserver.
//
// Known look-alikes that must NOT match:
//   - "apiserver_request_duration_seconds" — kube-apiserver latency histogram;
//     the name equality guard excludes it.
//   - "etcd_disk_backend_commit_duration_seconds" — etcd internal histogram;
//     the name equality guard excludes it.

package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

const schedulerMetricName = "scheduler_scheduling_attempt_duration_seconds"

type k8sSchedulerLatencyRecipe struct{}

// NewK8sSchedulerLatency returns the k8s_scheduler_latency recipe.
func NewK8sSchedulerLatency() Recipe { return &k8sSchedulerLatencyRecipe{} }

func (k8sSchedulerLatencyRecipe) Name() string    { return "k8s_scheduler_latency" }
func (k8sSchedulerLatencyRecipe) Section() string { return "resources" }

// Match requires a histogram whose base name is exactly
// "scheduler_scheduling_attempt_duration_seconds". Both the bare base name
// and the _bucket suffix are accepted because Prometheus metadata may return
// either form. The type guard rejects non-histograms with the same name.
func (r k8sSchedulerLatencyRecipe) Match(m ClassifiedMetricView) bool {
	baseName := strings.TrimSuffix(m.Descriptor.Name, "_bucket")
	return m.Type == inventory.MetricTypeHistogram &&
		baseName == schedulerMetricName
}

func (r k8sSchedulerLatencyRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	percentiles := []struct {
		quantile float64
		legend   string
	}{
		{0.50, "p50"},
		{0.95, "p95"},
		{0.99, "p99"},
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		// Group by result (scheduling outcome); safeGroupLabels adds job/instance
		// when present. le is required by histogram_quantile.
		group := safeGroupLabels(m, "result")
		group = ensureLabel(group, "le")
		// Prometheus metadata returns the bare base name; queryable series is _bucket.
		queryName := strings.TrimSuffix(m.Descriptor.Name, "_bucket") + "_bucket"
		queries := make([]ir.QueryCandidate, 0, len(percentiles))
		for _, pct := range percentiles {
			expr := fmt.Sprintf(
				"histogram_quantile(%.2f, sum by (%s) (rate(%s[%s])))",
				pct.quantile, strings.Join(group, ", "), queryName, defaultRateWindow,
			)
			queries = append(queries, ir.QueryCandidate{
				Expr:         expr,
				LegendFormat: pct.legend + " " + legendFor(without(group, "le")),
				Unit:         "s",
			})
		}
		panels = append(panels, ir.Panel{
			Title:      "Scheduler scheduling attempt latency (p50/p95/p99)",
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "s",
			Queries:    queries,
			Confidence: 0.85,
			Rationale: fmt.Sprintf(
				"kube-scheduler scheduling attempt latency histogram %q; p50/p95/p99 via histogram_quantile over %s grouped by result, le.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
