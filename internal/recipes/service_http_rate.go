package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// serviceHTTPRateRecipe emits a requests-per-second panel for HTTP-style
// counters.
type serviceHTTPRateRecipe struct{}

// NewServiceHTTPRate returns the service_http_rate recipe.
func NewServiceHTTPRate() Recipe { return &serviceHTTPRateRecipe{} }

func (serviceHTTPRateRecipe) Name() string    { return "service_http_rate" }
func (serviceHTTPRateRecipe) Section() string { return "traffic" }

// Match requires the descriptor to be a counter carrying the service_http
// trait. We do not attempt to infer HTTP shape from the name alone — traits
// were already decided by classify and we trust that single source.
func (r serviceHTTPRateRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeCounter && m.HasTrait("service_http")
}

func (r serviceHTTPRateRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m, "route", "handler")
		expr := fmt.Sprintf("sum by (%s) (rate(%s[%s]))", strings.Join(group, ", "), m.Descriptor.Name, defaultRateWindow)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("Request rate: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "reqps",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "reqps",
			}},
			Confidence: 0.85,
			Rationale: fmt.Sprintf(
				"Counter %q with HTTP-shaped labels; rate over %s grouped by %s.",
				m.Descriptor.Name, defaultRateWindow, strings.Join(group, ", "),
			),
		})
	}
	// Deterministic panel order inside a recipe's own output: by metric
	// name. Synth re-sorts later, but keeping intra-recipe order stable
	// protects callers that walk panels directly.
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
