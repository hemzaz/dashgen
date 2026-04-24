package recipes

// service_goroutines — goroutine count saturation panel for the service profile.
//
// Operator question: is the process leaking goroutines?
//
// Signals:
//   - MetricType gauge.
//   - Name exactly "go_goroutines" — the canonical Go runtime exporter metric.
//     Name equality is intentional: this is one of the few recipes that
//     identifies a metric by its exact name because the Go runtime exporter is
//     the sole well-known source and the name is unambiguous.
//
// Grouping: {instance, job} via safeGroupLabels — both labels are always
// present on the canonical Go runtime exporter.
//
// Aggregation: max by (instance, job) rather than sum. Each Go process reports
// its own goroutine count independently. Summing across instances would produce
// a misleadingly large number (e.g., 5 instances × 1 000 goroutines = 5 000)
// that does not reflect any single process's health. max surfaces the worst
// individual instance, which is the signal an operator actually cares about
// when checking for leaks.
//
// Confidence 0.90. Exact metric-name match with no ambiguity; the highest
// confidence tier (0.90-0.95) is appropriate here.
//
// Known look-alikes that must NOT match:
//   - go_threads: different Go runtime gauge; name does not match.
//   - process_resident_memory_bytes: unrelated gauge.
//   - Any counter that happens to share the "go_" prefix.

import (
	"fmt"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type serviceGoroutinesRecipe struct{}

// NewServiceGoroutines returns the service_goroutines recipe.
func NewServiceGoroutines() Recipe { return &serviceGoroutinesRecipe{} }

func (serviceGoroutinesRecipe) Name() string    { return "service_goroutines" }
func (serviceGoroutinesRecipe) Section() string { return "saturation" }

// Match accepts only the exact gauge "go_goroutines".
func (r serviceGoroutinesRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeGauge && m.Descriptor.Name == "go_goroutines"
}

func (r serviceGoroutinesRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m)
		expr := fmt.Sprintf("max by (%s) (%s)", strings.Join(group, ", "), m.Descriptor.Name)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("Goroutines: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "",
			}},
			Confidence: 0.90,
			Rationale: fmt.Sprintf(
				"Gauge %q aggregated with max by (%s); max rather than sum because each "+
					"process reports its own count — summing across instances would misleadingly inflate the number.",
				m.Descriptor.Name, strings.Join(group, ", "),
			),
		})
	}
	return panels
}
