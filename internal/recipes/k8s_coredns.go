// k8s_coredns — CoreDNS request-latency distribution and request rate.
//
// Operator question: how long are CoreDNS requests taking, and at what rate
// are queries arriving? CoreDNS is the cluster DNS server for every Kubernetes
// workload; latency spikes here slow service-discovery for all pods.
//
// Signals (paired):
//   - MetricType histogram: "coredns_dns_request_duration_seconds" — canonical
//     CoreDNS latency histogram exported since CoreDNS 1.3. Labels: server,
//     zone, type. We match by exact base name (with or without the _bucket
//     suffix that Prometheus may expose).
//   - MetricType counter (or unknown): "coredns_dns_requests_total" — query
//     volume counter. Labels: server, zone.
//
// Match strategy: NAME-ANCHORED. Exact name equality is used for both signals
// so that similar-looking metrics (kube_dns_*, coredns_health_*,
// apiserver_request_duration_seconds_bucket) are excluded.
//
// Panels:
//   - Latency (histogram present): p95 via histogram_quantile, grouped by
//     server, zone, le. One panel per histogram signal.
//   - Rate (counter present): per-second rate, grouped by server, zone.
//
// Confidence 0.85 — exact name match with a well-known CoreDNS export warrants
// the high-specificity band but is fractionally below 0.90 because CoreDNS
// versions and custom builds may omit either signal.
//
// Known look-alikes that must NOT match:
//   - "kube_dns_responses_total" — legacy kube-dns counter; different name.
//   - "coredns_health_request_duration_seconds_bucket" — health-check latency,
//     not DNS query latency; name equality excludes it.
//   - "apiserver_request_duration_seconds_bucket" — control-plane histogram;
//     a different component entirely.

package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

const (
	coreDNSLatencyMetric = "coredns_dns_request_duration_seconds"
	coreDNSRateMetric    = "coredns_dns_requests_total"
)

type k8sCoreDNSRecipe struct{}

// NewK8sCoreDNS returns the k8s_coredns recipe.
func NewK8sCoreDNS() Recipe { return &k8sCoreDNSRecipe{} }

func (k8sCoreDNSRecipe) Name() string    { return "k8s_coredns" }
func (k8sCoreDNSRecipe) Section() string { return "latency" }

// Match returns true for the CoreDNS latency histogram or the request-rate
// counter. Name equality is used for both to avoid claiming unrelated metrics.
func (r k8sCoreDNSRecipe) Match(m ClassifiedMetricView) bool {
	// Histogram: accept bare base name or the _bucket suffix variant.
	if m.Type == inventory.MetricTypeHistogram {
		return m.Descriptor.Name == coreDNSLatencyMetric ||
			m.Descriptor.Name == coreDNSLatencyMetric+"_bucket"
	}
	// Counter or unknown: exact name for the request-rate signal.
	if m.Type == inventory.MetricTypeCounter || m.Type == inventory.MetricTypeUnknown {
		return m.Descriptor.Name == coreDNSRateMetric
	}
	return false
}

func (r k8sCoreDNSRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	var panels []ir.Panel

	// Latency panel — p95 via histogram_quantile.
	for _, m := range inv.Metrics {
		if m.Type != inventory.MetricTypeHistogram {
			continue
		}
		if m.Descriptor.Name != coreDNSLatencyMetric && m.Descriptor.Name != coreDNSLatencyMetric+"_bucket" {
			continue
		}
		group := safeGroupLabels(m, "server", "zone")
		group = ensureLabel(group, "le")
		queryName := m.Descriptor.Name
		if !strings.HasSuffix(queryName, "_bucket") {
			queryName += "_bucket"
		}
		expr := fmt.Sprintf(
			"histogram_quantile(0.95, sum by (%s) (rate(%s[%s])))",
			strings.Join(group, ", "), queryName, defaultRateWindow,
		)
		panels = append(panels, ir.Panel{
			Title: "CoreDNS request latency (p95)",
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "s",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: "p95 " + legendFor(without(group, "le")),
				Unit:         "s",
			}},
			Confidence: 0.85,
			Rationale: fmt.Sprintf(
				"CoreDNS request latency histogram %q; p95 via histogram_quantile over %s grouped by server, zone, le.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
		break // one histogram signal
	}

	// Rate panel — per-second DNS request rate.
	for _, m := range inv.Metrics {
		if m.Descriptor.Name != coreDNSRateMetric {
			continue
		}
		if m.Type != inventory.MetricTypeCounter && m.Type != inventory.MetricTypeUnknown {
			continue
		}
		group := safeGroupLabels(m, "server", "zone")
		expr := fmt.Sprintf(
			"sum by (%s) (rate(%s[%s]))",
			strings.Join(group, ", "), coreDNSRateMetric, defaultRateWindow,
		)
		panels = append(panels, ir.Panel{
			Title: "CoreDNS request rate",
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "reqps",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "reqps",
			}},
			Confidence: 0.85,
			Rationale: fmt.Sprintf(
				"CoreDNS request rate counter %q; per-second rate over %s grouped by server, zone.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
		break // one rate signal
	}

	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
