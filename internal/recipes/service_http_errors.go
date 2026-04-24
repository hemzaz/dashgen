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

// Match requires a counter with a status_code label specifically — the
// broader service_http trait is not enough because we need the label to
// build the 5xx filter.
func (r serviceHTTPErrorsRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeCounter && m.HasLabel("status_code")
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
		group := safeGroupLabels(m, "route", "handler")
		expr := fmt.Sprintf(
			`sum by (%s) (rate(%s{status_code=~"5.."}[%s]))`,
			strings.Join(group, ", "), m.Descriptor.Name, defaultRateWindow,
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
				"Counter %q has a status_code label; filtering to 5.. gives a 5xx error rate.",
				m.Descriptor.Name,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
