package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestInfraInterrupts_Match(t *testing.T) {
	r := NewInfraInterrupts()

	// Positive: exact counter signal.
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_interrupts_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if !r.Match(pos) {
		t.Error("expected match on node_interrupts_total (Counter)")
	}

	// Positive: Unknown type is also accepted.
	posUnknown := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_interrupts_total"},
		Type:       inventory.MetricTypeUnknown,
	}
	if !r.Match(posUnknown) {
		t.Error("expected match on node_interrupts_total (Unknown type)")
	}

	// Negative: container CPU counter — unrelated.
	negContainer := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "container_cpu_usage_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(negContainer) {
		t.Error("expected no match on container_cpu_usage_seconds_total")
	}

	// Negative: process CPU counter — unrelated.
	negProcess := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "process_cpu_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(negProcess) {
		t.Error("expected no match on process_cpu_seconds_total")
	}

	// Negative: softirqs — similar name, distinct signal.
	negSoftirq := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_softirqs_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(negSoftirq) {
		t.Error("expected no match on node_softirqs_total (distinct softirq signal)")
	}
}

func TestInfraInterrupts_BuildPanels(t *testing.T) {
	r := NewInfraInterrupts()

	t.Run("infra profile with signal emits one panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_interrupts_total",
						Labels: []string{"instance", "cpu", "type"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if len(panels[0].Queries) != 1 {
			t.Fatalf("expected 1 query, got %d", len(panels[0].Queries))
		}
		expr := panels[0].Queries[0].Expr
		if !strings.Contains(expr, "rate(") {
			t.Errorf("expected rate() in expression, got %q", expr)
		}
		if !strings.Contains(expr, "sum by") {
			t.Errorf("expected sum by in expression, got %q", expr)
		}
		if !strings.Contains(expr, "[5m]") {
			t.Errorf("expected [5m] window in expression, got %q", expr)
		}
		// Verify the expression is valid PromQL.
		if _, err := parser.NewParser(parser.Options{}).ParseExpr(expr); err != nil {
			t.Errorf("expression %q does not parse: %v", expr, err)
		}
	})

	t.Run("non-infra profile yields nil", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_interrupts_total"},
					Type:       inventory.MetricTypeCounter,
				},
			},
		}
		if panels := r.BuildPanels(inv, profiles.ProfileService); panels != nil {
			t.Errorf("expected nil for non-infra profile, got %v", panels)
		}
	})

	t.Run("missing signal yields nil", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_softirqs_total"},
					Type:       inventory.MetricTypeCounter,
				},
			},
		}
		if panels := r.BuildPanels(inv, profiles.ProfileInfra); panels != nil {
			t.Errorf("expected nil when signal absent, got %v", panels)
		}
	})
}
