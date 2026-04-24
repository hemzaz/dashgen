package recipes

import (
	"fmt"
	"sort"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// k8sContainerResourcesRecipe emits CPU and memory panels from cAdvisor's
// container_* series. Empty container / pod labels are filtered out so the
// machine-level totals do not dominate the graph.
type k8sContainerResourcesRecipe struct{}

// NewK8sContainerResources returns the k8s_container_resources recipe.
func NewK8sContainerResources() Recipe { return &k8sContainerResourcesRecipe{} }

func (k8sContainerResourcesRecipe) Name() string    { return "k8s_container_resources" }
func (k8sContainerResourcesRecipe) Section() string { return "resources" }

func (r k8sContainerResourcesRecipe) Match(m ClassifiedMetricView) bool {
	switch m.Descriptor.Name {
	case "container_cpu_usage_seconds_total":
		return m.Type == inventory.MetricTypeCounter
	case "container_memory_working_set_bytes":
		return m.Type == inventory.MetricTypeGauge || m.Type == inventory.MetricTypeUnknown
	}
	return false
}

func (r k8sContainerResourcesRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileK8s {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		switch m.Descriptor.Name {
		case "container_cpu_usage_seconds_total":
			expr := fmt.Sprintf(
				`sum by (namespace, pod, container) (rate(%s{container!="", pod!=""}[%s]))`,
				m.Descriptor.Name, defaultRateWindow,
			)
			panels = append(panels, ir.Panel{
				Title: fmt.Sprintf("Container CPU: %s", m.Descriptor.Name),
				Kind:  ir.PanelKindTimeSeries,
				Unit:  "short",
				Queries: []ir.QueryCandidate{{
					Expr:         expr,
					LegendFormat: "{{namespace}} {{pod}} {{container}}",
					Unit:         "short",
				}},
				Confidence: 0.8,
				Rationale: fmt.Sprintf(
					"cAdvisor counter %q; rate over %s per namespace+pod+container.",
					m.Descriptor.Name, defaultRateWindow,
				),
			})
		case "container_memory_working_set_bytes":
			expr := fmt.Sprintf(
				`sum by (namespace, pod, container) (%s{container!="", pod!=""})`,
				m.Descriptor.Name,
			)
			panels = append(panels, ir.Panel{
				Title: fmt.Sprintf("Container memory: %s", m.Descriptor.Name),
				Kind:  ir.PanelKindTimeSeries,
				Unit:  "bytes",
				Queries: []ir.QueryCandidate{{
					Expr:         expr,
					LegendFormat: "{{namespace}} {{pod}} {{container}}",
					Unit:         "bytes",
				}},
				Confidence: 0.8,
				Rationale: fmt.Sprintf(
					"cAdvisor gauge %q; summed by namespace+pod+container.",
					m.Descriptor.Name,
				),
			})
		}
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
