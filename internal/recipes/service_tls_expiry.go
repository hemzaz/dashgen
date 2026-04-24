package recipes

// service_tls_expiry — TLS certificate expiry countdown
//
// # Operator question
//
// How many days until each TLS certificate expires? Alerts on short
// runway (e.g. <14 d) and dashboards showing per-service expiry give
// operators early warning before a cert renewal outage.
//
// # Canonical signals
//
// There is no single agreed-upon metric name for certificate expiry
// across the Prometheus ecosystem. Three common conventions are:
//
//   - *_tls_not_after_timestamp  — used by Caddy server
//   - *_cert_expiry_timestamp_seconds — used by blackbox_exporter
//   - *_ssl_cert_not_after       — used by Traefik
//
// All three encode the same information: a Unix epoch timestamp (seconds)
// at which the certificate becomes invalid. Any gauge metric whose name
// ends with one of these suffixes is accepted.
//
// # Aggregation shape and time() arithmetic
//
// The raw metric value is a Unix timestamp. To express "days remaining"
// in an operator-friendly unit the query subtracts the current time:
//
//	(<metric>) - time()
//
// This yields seconds until expiry (negative after expiry). Dividing by
// 86400 (seconds/day) converts to days:
//
//	(<metric> - time()) / 86400
//
// No sum-by is needed because cert-expiry gauges report per-certificate
// facts, not aggregable resource consumption. The grouping is kept to
// {instance, job} so panels remain bounded while still being
// per-service.
//
// # Confidence: 0.80
//
// Three distinct suffix patterns give high confidence that a matching
// metric is genuinely a certificate expiry timestamp. The value 0.80 is
// appropriate (strong name-pattern match) rather than 0.90+ because we
// are matching on suffix, not exact name, and the gauge-type check is
// the only structural guard.
//
// # Known look-alikes that must NOT match
//
//   - probe_ssl_earliest_cert_expiry (blackbox_exporter) — ends in
//     _cert_expiry, not _cert_expiry_timestamp_seconds; would need its
//     own recipe if it appears at scale.
//   - Any counter variant of the above names — counters do not carry
//     a timestamp epoch value and must be rejected.

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// tlsExpirySuffixes is the closed set of name suffixes that identify a
// cert-expiry Unix-timestamp gauge. Listed longest-first so a greedy
// scan can short-circuit on the most specific match.
var tlsExpirySuffixes = []string{
	"_cert_expiry_timestamp_seconds", // blackbox_exporter canonical
	"_tls_not_after_timestamp",       // Caddy
	"_ssl_cert_not_after",            // Traefik
}

type serviceTLSExpiryRecipe struct{}

// NewServiceTLSExpiry returns the service_tls_expiry recipe.
func NewServiceTLSExpiry() Recipe { return &serviceTLSExpiryRecipe{} }

func (serviceTLSExpiryRecipe) Name() string    { return "service_tls_expiry" }
func (serviceTLSExpiryRecipe) Section() string { return "saturation" }

func (r serviceTLSExpiryRecipe) Match(m ClassifiedMetricView) bool {
	// Cert-expiry metrics must be gauge type. Counters with similar names
	// (hypothetically incremented each time a cert is renewed) must not match.
	if m.Type != inventory.MetricTypeGauge {
		return false
	}
	for _, suffix := range tlsExpirySuffixes {
		if strings.HasSuffix(m.Descriptor.Name, suffix) {
			return true
		}
	}
	return false
}

func (r serviceTLSExpiryRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m)
		// Subtract time() to get seconds-until-expiry, then divide by 86400
		// to convert to days. This is an instant-vector arithmetic expression;
		// no rate() is needed because the metric is already a scalar timestamp.
		expr := fmt.Sprintf("(%s - time()) / 86400", m.Descriptor.Name)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("TLS cert days to expiry: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "d",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "d",
			}},
			Confidence: 0.80,
			Rationale: fmt.Sprintf(
				"Cert-expiry timestamp gauge %q; (metric - time()) / 86400 yields days until expiry.",
				m.Descriptor.Name,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
