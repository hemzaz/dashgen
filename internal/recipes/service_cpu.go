package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// cpuMetricNames is the closed set of CPU-utilization counters the recipe
// will turn into a rate-per-second gauge. Both the process-level metric and
// the common container metric are in scope; anything else is refused by
// omission.
var cpuMetricNames = []string{
	"process_cpu_seconds_total",
	"container_cpu_usage_seconds_total",
}

type serviceCPURecipe struct{}

// NewServiceCPU returns the service_cpu recipe.
func NewServiceCPU() Recipe { return &serviceCPURecipe{} }

func (serviceCPURecipe) Name() string    { return "service_cpu" }
func (serviceCPURecipe) Section() string { return "saturation" }

func (r serviceCPURecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	for _, n := range cpuMetricNames {
		if m.Descriptor.Name == n {
			return true
		}
	}
	return false
}

func (r serviceCPURecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m)
		expr := fmt.Sprintf("sum by (%s) (rate(%s[%s]))", strings.Join(group, ", "), m.Descriptor.Name, defaultRateWindow)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("CPU (cores used): %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			// percentunit is intentionally not used here — rate of a seconds
			// counter is CPU cores consumed, not a ratio.
			Unit: "short",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "short",
			}},
			Confidence: 0.8,
			Rationale: fmt.Sprintf(
				"CPU-seconds counter %q; rate over %s yields cores consumed.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
