package recipes

// serviceClientHTTPRecipe emits a requests-per-second panel for outbound HTTP
// client counters.
//
// Operator question: how fast is this service making outbound HTTP calls, and
// to which upstream targets?
//
// Canonical signals:
//   - Metric type: counter.
//   - Name contains "client" (case-insensitive). This is the deciding guard
//     that separates outbound client counters from inbound server counters
//     (which lack "client" in their names).
//   - Has at least one HTTP-status label: "status_code" or "code".
//
// Aggregation shape: sum by (job, instance, <url/host/target/upstream>) of
// rate(<metric>[5m]). The host/target/upstream/url group is drawn from
// whichever labels actually exist on the descriptor via safeGroupLabels so no
// empty label references appear in queries.
//
// Confidence: 0.75. Lower than service_http_rate (0.85) because the
// "client" name-heuristic is softer than a classifier-assigned trait. There is
// a real risk of false positives from non-HTTP client counters whose authors
// happen to also emit a "code" label for non-HTTP reasons; that risk is
// accepted and documented in the discrimination test.
//
// Known look-alikes that must NOT match:
//   - http_requests_total (no "client" in name) — belongs to service_http_rate.
//   - cache_client_hits_total (has "client" but no HTTP-status label).
//   - alertmanager_notifications_total{integration=...} with a bare "code"
//     label — lacks "client" in name so it is also excluded.

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type serviceClientHTTPRecipe struct{}

// NewServiceClientHTTP returns the service_client_http recipe.
func NewServiceClientHTTP() Recipe { return &serviceClientHTTPRecipe{} }

func (serviceClientHTTPRecipe) Name() string    { return "service_client_http" }
func (serviceClientHTTPRecipe) Section() string { return "traffic" }

// Match requires:
//  1. MetricTypeCounter
//  2. Name contains "client" (case-insensitive) — guards against inbound
//     server counters that also carry HTTP-status labels.
//  3. Has at least one of {status_code, code} — confirms the counter is
//     HTTP-shaped rather than a generic client call counter.
func (r serviceClientHTTPRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	if !strings.Contains(strings.ToLower(m.Descriptor.Name), "client") {
		return false
	}
	return statusLabelOf(m) != ""
}

func (r serviceClientHTTPRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m, "host", "target", "upstream", "url")
		expr := fmt.Sprintf("sum by (%s) (rate(%s[%s]))", strings.Join(group, ", "), m.Descriptor.Name, defaultRateWindow)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("Outbound HTTP call rate: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "reqps",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "reqps",
			}},
			Confidence: 0.75,
			Rationale: fmt.Sprintf(
				"Counter %q has \"client\" in name and an HTTP-status label; rate over %s grouped by %s.",
				m.Descriptor.Name, defaultRateWindow, strings.Join(group, ", "),
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
