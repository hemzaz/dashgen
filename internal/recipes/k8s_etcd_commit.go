// k8s_etcd_commit — p50/p95/p99 etcd disk backend commit latency.
//
// Operator question: how long does etcd take to commit a batch to its disk
// backend (bbolt)? This is THE critical signal for k8s control-plane health.
// When p99 exceeds 25 ms for sustained periods, apiserver writes slow down
// across the entire cluster because every write path blocks on an etcd fsync.
//
// Signals:
//   - MetricType histogram.
//   - Name exactly "etcd_disk_backend_commit_duration_seconds" — the canonical
//     histogram exported by etcd since v3.1. We match by name equality rather
//     than trait alone because the trait fires on any "duration" histogram;
//     name equality gives the precision required for a control-plane recipe.
//   - TraitLatencyHistogram presence is expected from the classifier but is
//     not required here; the name equality guard is sufficient and tighter.
//
// Grouping: {instance, le} — etcd clusters are small (typically 3–5 nodes).
// There is no per-request breakdown for backend commits, so instance-level
// granularity is the finest available. le is required by histogram_quantile.
//
// Query shape: p50/p95/p99 via histogram_quantile on the _bucket series.
// The metadata API returns the bare base name; _bucket is appended when the
// name doesn't already carry the suffix (consistent with k8s_apiserver_latency
// and service_http_latency).
//
// Confidence 0.90 — exact metric name match warrants the high-specificity
// band (0.90–0.95) per §1.1.
//
// Known look-alikes that must NOT match:
//   - "etcd_disk_wal_fsync_duration_seconds" — measures WAL fsync latency, a
//     distinct signal; the name equality guard excludes it.
//   - Any non-histogram metric of the same name — the type guard rejects it.

package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

const etcdCommitMetricName = "etcd_disk_backend_commit_duration_seconds"

type k8sEtcdCommitRecipe struct{}

// NewK8sEtcdCommit returns the k8s_etcd_commit recipe.
func NewK8sEtcdCommit() Recipe { return &k8sEtcdCommitRecipe{} }

func (k8sEtcdCommitRecipe) Name() string    { return "k8s_etcd_commit" }
func (k8sEtcdCommitRecipe) Section() string { return "resources" }

// Match requires a histogram whose name is exactly "etcd_disk_backend_commit_duration_seconds".
// Name equality is used for specificity: TraitLatencyHistogram alone would
// match unrelated duration histograms. The type guard rejects non-histograms
// that happen to share the name.
func (r k8sEtcdCommitRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeHistogram &&
		m.Descriptor.Name == etcdCommitMetricName
}

func (r k8sEtcdCommitRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
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
		// Group by instance; safeGroupLabels adds job/instance when present.
		// le is required by histogram_quantile.
		group := safeGroupLabels(m)
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
			Title:      "etcd backend commit latency (p50/p95/p99)",
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "s",
			Queries:    queries,
			Confidence: 0.90,
			Rationale: fmt.Sprintf(
				"etcd disk backend commit latency histogram %q; p50/p95/p99 via histogram_quantile over %s grouped by instance, le. p99 > 25ms sustained indicates control-plane write pressure.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
