package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sHPAScaling_Match(t *testing.T) {
	r := NewK8sHPAScaling()

	// Positive: the primary signal triggers a match.
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_horizontalpodautoscaler_status_current_replicas"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on kube_horizontalpodautoscaler_status_current_replicas")
	}

	// Negative: desired_replicas alone must not match (Match is on current_replicas only).
	negDesired := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_horizontalpodautoscaler_status_desired_replicas"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negDesired) {
		t.Errorf("expected no match on kube_horizontalpodautoscaler_status_desired_replicas")
	}

	// Negative: deployment look-alike (different resource type).
	negDeployment := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_deployment_spec_replicas"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negDeployment) {
		t.Errorf("expected no match on kube_deployment_spec_replicas")
	}

	// Negative: counter with same base name must not match (type guard).
	negCounter := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_horizontalpodautoscaler_status_current_replicas"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(negCounter) {
		t.Errorf("expected no match on counter type with hpa current_replicas name")
	}
}

func TestK8sHPAScaling_BuildPanels(t *testing.T) {
	r := NewK8sHPAScaling()

	labels := []string{"namespace", "horizontalpodautoscaler"}

	both := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "kube_horizontalpodautoscaler_status_current_replicas",
					Labels: labels,
				},
				Type: inventory.MetricTypeGauge,
			},
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "kube_horizontalpodautoscaler_status_desired_replicas",
					Labels: labels,
				},
				Type: inventory.MetricTypeGauge,
			},
		},
	}

	t.Run("both metrics present yields 1 panel with 2 queries", func(t *testing.T) {
		panels := r.BuildPanels(both, profiles.ProfileK8s)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if len(panels[0].Queries) != 2 {
			t.Fatalf("expected 2 query candidates, got %d", len(panels[0].Queries))
		}
		q0 := panels[0].Queries[0].Expr
		q1 := panels[0].Queries[1].Expr

		// Query 0 — current: contains current_replicas and grouping.
		if !strings.Contains(q0, "kube_horizontalpodautoscaler_status_current_replicas") {
			t.Errorf("query 0 should reference current_replicas, got %q", q0)
		}
		if !strings.Contains(q0, "namespace, horizontalpodautoscaler") {
			t.Errorf("query 0 should group by namespace+horizontalpodautoscaler, got %q", q0)
		}

		// Query 1 — desired: contains desired_replicas and grouping.
		if !strings.Contains(q1, "kube_horizontalpodautoscaler_status_desired_replicas") {
			t.Errorf("query 1 should reference desired_replicas, got %q", q1)
		}
		if !strings.Contains(q1, "namespace, horizontalpodautoscaler") {
			t.Errorf("query 1 should group by namespace+horizontalpodautoscaler, got %q", q1)
		}
	})

	t.Run("only current_replicas present yields 0 panels", func(t *testing.T) {
		onlyCurrent := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "kube_horizontalpodautoscaler_status_current_replicas",
						Labels: labels,
					},
					Type: inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(onlyCurrent, profiles.ProfileK8s)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when desired_replicas absent, got %d", len(panels))
		}
	})

	t.Run("non-k8s profile yields 0 panels", func(t *testing.T) {
		panels := r.BuildPanels(both, profiles.ProfileService)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels for non-k8s profile, got %d", len(panels))
		}
	})
}
