package recipes

// infra_interrupts — per-CPU hardware interrupt rate from node_exporter
//
// Operator question:
//
//	Which CPUs and interrupt types are under the heaviest IRQ load?
//	A spike in interrupt rate often precedes latency regressions and can
//	indicate NIC queue imbalance, storage IRQ pressure, or scheduling skew.
//
// Canonical signal:
//
//	node_interrupts_total — counter, labels: instance, cpu, type.
//	Emitted by node_exporter; one series per (instance, cpu, interrupt-type).
//
// Aggregation shape:
//
//	sum by (instance, cpu) (rate(node_interrupts_total[5m]))
//	Collapses interrupt types so the panel shows per-CPU interrupt throughput.
//
// Confidence: 0.80 — exact metric name match; no name-prefix ambiguity.
//
// Known look-alikes that must NOT match:
//
//	node_softirqs_total               — software interrupts, distinct signal.
//	container_cpu_usage_seconds_total — unrelated container counter.
//	process_cpu_seconds_total         — process-level counter.

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type infraInterruptsRecipe struct{}

// NewInfraInterrupts returns the infra_interrupts recipe.
func NewInfraInterrupts() Recipe { return &infraInterruptsRecipe{} }

func (infraInterruptsRecipe) Name() string    { return "infra_interrupts" }
func (infraInterruptsRecipe) Section() string { return "saturation" }

// Match returns true only for the exact counter node_interrupts_total.
// node_softirqs_total is deliberately excluded — it is a related but distinct
// signal that warrants its own recipe.
func (r infraInterruptsRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "node_interrupts_total"
}

func (r infraInterruptsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	var found bool
	for _, m := range inv.Metrics {
		if m.Descriptor.Name == "node_interrupts_total" {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	expr := fmt.Sprintf(
		"sum by (instance, cpu) (rate(node_interrupts_total[%s]))",
		defaultRateWindow,
	)
	return []ir.Panel{{
		Title: "Hardware interrupt rate by CPU",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "ops",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} cpu{{cpu}}",
			Unit:         "ops",
		}},
		Confidence: 0.80,
		Rationale: fmt.Sprintf(
			"node_exporter counter node_interrupts_total; rate over %s summed per instance+cpu.",
			defaultRateWindow,
		),
	}}
}
