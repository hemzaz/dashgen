package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// serviceHTTPErrorsRecipe emits a 5xx error-rate panel.
type serviceHTTPErrorsRecipe struct{}

// NewServiceHTTPErrors returns the service_http_errors recipe.
func NewServiceHTTPErrors() Recipe { return &serviceHTTPErrorsRecipe{} }

func (serviceHTTPErrorsRecipe) Name() string    { return "service_http_errors" }
func (serviceHTTPErrorsRecipe) Section() string { return "errors" }

// httpStatusLabels are the conventional HTTP-status label keys, in priority
// order. We accept either {status_code,status} (REST middleware convention)
// or {code} (Go promhttp / many Java frameworks).
var httpStatusLabels = []string{"status_code", "status", "code"}

// statusLabelOf returns the first httpStatusLabels entry the metric carries,
// or "" if none. Deterministic: priority is fixed by httpStatusLabels.
func statusLabelOf(m ClassifiedMetricView) string {
	for _, l := range httpStatusLabels {
		if m.HasLabel(l) {
			return l
		}
	}
	return ""
}

// Match requires a counter that carries a recognizable HTTP-status label.
// The broader service_http trait alone is not enough because we need a
// concrete label to build the 5xx filter.
func (r serviceHTTPErrorsRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeCounter && statusLabelOf(m) != ""
}

func (r serviceHTTPErrorsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		statusLabel := statusLabelOf(m)
		group := safeGroupLabels(m, "route", "handler")
		expr := fmt.Sprintf(
			`sum by (%s) (rate(%s{%s=~"5.."}[%s]))`,
			strings.Join(group, ", "), m.Descriptor.Name, statusLabel, defaultRateWindow,
		)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("5xx error rate: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "reqps",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "reqps",
			}},
			Confidence: 0.8,
			Rationale: fmt.Sprintf(
				"Counter %q has a %q label; filtering to 5.. gives a 5xx error rate.",
				m.Descriptor.Name, statusLabel,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
