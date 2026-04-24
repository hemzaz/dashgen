// k8s_apiserver_latency — p50/p95/p99 kube-apiserver request latency.
//
// Operator question: how long are apiserver requests taking, broken down by
// verb and resource? Spikes here indicate control-plane pressure that affects
// every workload on the cluster.
//
// Signals:
//   - MetricType histogram.
//   - Name exactly "apiserver_request_duration_seconds" — the canonical
//     histogram exported by kube-apiserver since Kubernetes 1.13. We match
//     by name equality rather than trait alone because the trait fires on any
//     "duration" histogram; name equality gives the precision needed for a
//     control-plane recipe.
//   - TraitLatencyHistogram presence is expected from the classifier but is
//     not required here; the name equality guard is sufficient and tighter.
//
// Grouping: {verb, resource, le} — apiserver latency is most actionable when
// broken down by verb (GET/LIST/POST/…) and resource (pods, nodes, …). The
// le label is required by histogram_quantile.
//
// Query shape: p50/p95/p99 via histogram_quantile on the _bucket series.
// The metadata API returns the bare base name; _bucket is appended when the
// name doesn't already carry the suffix (consistent with service_http_latency
// and service_grpc_latency).
//
// Confidence 0.90 — exact metric name match warrants the high-specificity
// band (0.90–0.95) per §1.1.
//
// Known look-alikes that must NOT match:
//   - "http_request_duration_seconds" — a common app-level histogram; the
//     name equality guard excludes it explicitly.
//   - Any non-histogram metric of the same name (e.g. a mistakenly exposed
//     counter) — the type guard rejects it.

package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

const apiserverMetricName = "apiserver_request_duration_seconds"

type k8sApiserverLatencyRecipe struct{}

// NewK8sApiserverLatency returns the k8s_apiserver_latency recipe.
func NewK8sApiserverLatency() Recipe { return &k8sApiserverLatencyRecipe{} }

func (k8sApiserverLatencyRecipe) Name() string    { return "k8s_apiserver_latency" }
func (k8sApiserverLatencyRecipe) Section() string { return "resources" }

// Match requires a histogram whose name is exactly "apiserver_request_duration_seconds".
// Name equality is used for specificity: the TraitLatencyHistogram alone would
// match unrelated duration histograms. The type guard rejects non-histograms
// that happen to share the name.
func (r k8sApiserverLatencyRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeHistogram &&
		m.Descriptor.Name == apiserverMetricName
}

func (r k8sApiserverLatencyRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
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
		// Group by verb and resource; safeGroupLabels adds job/instance when
		// present. le is required by histogram_quantile.
		group := safeGroupLabels(m, "verb", "resource")
		group = ensureLabel(group, "le")
		// Prometheus metadata returns the bare base name; queryable series is _bucket.
		queryName := m.Descriptor.Name
		if !strings.HasSuffix(queryName, "_bucket") {
			queryName += "_bucket"
		}
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
			Title:      "Apiserver request latency (p50/p95/p99)",
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "s",
			Queries:    queries,
			Confidence: 0.90,
			Rationale: fmt.Sprintf(
				"kube-apiserver latency histogram %q; p50/p95/p99 via histogram_quantile over %s grouped by verb, resource, le.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
