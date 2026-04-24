package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// memoryMetricNames is the closed set of memory metrics we recognize. Both
// the process-level and container-level names are accepted; anything else
// is refused by omission.
var memoryMetricNames = []string{
	"process_resident_memory_bytes",
	"container_memory_usage_bytes",
	"container_memory_working_set_bytes",
}

type serviceMemoryRecipe struct{}

// NewServiceMemory returns the service_memory recipe.
func NewServiceMemory() Recipe { return &serviceMemoryRecipe{} }

func (serviceMemoryRecipe) Name() string    { return "service_memory" }
func (serviceMemoryRecipe) Section() string { return "saturation" }

func (r serviceMemoryRecipe) Match(m ClassifiedMetricView) bool {
	// Memory metrics may be typed as gauge (common) or left unknown when
	// metadata is missing; accept either as long as the name matches one
	// of the approved metrics.
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	for _, n := range memoryMetricNames {
		if m.Descriptor.Name == n {
			return true
		}
	}
	return false
}

func (r serviceMemoryRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m)
		expr := fmt.Sprintf("sum by (%s) (%s)", strings.Join(group, ", "), m.Descriptor.Name)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("Memory: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "bytes",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "bytes",
			}},
			Confidence: 0.8,
			Rationale: fmt.Sprintf(
				"Memory gauge %q summed by %s.",
				m.Descriptor.Name, strings.Join(group, ", "),
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
