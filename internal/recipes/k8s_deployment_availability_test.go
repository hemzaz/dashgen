package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sDeploymentAvailability_Match(t *testing.T) {
	r := NewK8sDeploymentAvailability()

	// Positive: the primary signal triggers a match.
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_deployment_status_replicas_available"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on kube_deployment_status_replicas_available")
	}

	// Negative: spec_replicas alone must not match (Match is on status_replicas_available only).
	negSpec := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_deployment_spec_replicas"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negSpec) {
		t.Errorf("expected no match on kube_deployment_spec_replicas")
	}

	// Negative: look-alike total replicas (not available).
	negTotal := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_deployment_status_replicas"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negTotal) {
		t.Errorf("expected no match on kube_deployment_status_replicas")
	}

	// Negative: unavailable variant.
	negUnavailable := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_deployment_status_replicas_unavailable"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negUnavailable) {
		t.Errorf("expected no match on kube_deployment_status_replicas_unavailable")
	}

	// Negative: ReplicaSet look-alike.
	negRS := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_replicaset_status_replicas_available"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negRS) {
		t.Errorf("expected no match on kube_replicaset_status_replicas_available")
	}
}

func TestK8sDeploymentAvailability_BuildPanels(t *testing.T) {
	r := NewK8sDeploymentAvailability()

	labels := []string{"namespace", "deployment"}

	both := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "kube_deployment_status_replicas_available",
					Labels: labels,
				},
				Type: inventory.MetricTypeGauge,
			},
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "kube_deployment_spec_replicas",
					Labels: labels,
				},
				Type: inventory.MetricTypeGauge,
			},
		},
	}

	t.Run("both metrics present yields 1 panel with 3 queries", func(t *testing.T) {
		panels := r.BuildPanels(both, profiles.ProfileK8s)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if len(panels[0].Queries) != 3 {
			t.Fatalf("expected 3 query candidates, got %d", len(panels[0].Queries))
		}
		q0 := panels[0].Queries[0].Expr
		q1 := panels[0].Queries[1].Expr
		q2 := panels[0].Queries[2].Expr

		// Query 0 — desired: contains spec_replicas and grouping.
		if !strings.Contains(q0, "kube_deployment_spec_replicas") {
			t.Errorf("query 0 should reference spec_replicas, got %q", q0)
		}
		if !strings.Contains(q0, "namespace, deployment") {
			t.Errorf("query 0 should group by namespace+deployment, got %q", q0)
		}

		// Query 1 — available: contains status_replicas_available and grouping.
		if !strings.Contains(q1, "kube_deployment_status_replicas_available") {
			t.Errorf("query 1 should reference status_replicas_available, got %q", q1)
		}
		if !strings.Contains(q1, "namespace, deployment") {
			t.Errorf("query 1 should group by namespace+deployment, got %q", q1)
		}

		// Query 2 — ratio: contains both metrics and a division operator.
		if !strings.Contains(q2, "kube_deployment_status_replicas_available") {
			t.Errorf("query 2 should reference status_replicas_available, got %q", q2)
		}
		if !strings.Contains(q2, "kube_deployment_spec_replicas") {
			t.Errorf("query 2 should reference spec_replicas, got %q", q2)
		}
		if !strings.Contains(q2, "/") {
			t.Errorf("query 2 should contain division operator, got %q", q2)
		}
	})

	t.Run("only status_replicas_available present yields 0 panels", func(t *testing.T) {
		onlyAvailable := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "kube_deployment_status_replicas_available",
						Labels: labels,
					},
					Type: inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(onlyAvailable, profiles.ProfileK8s)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when spec_replicas absent, got %d", len(panels))
		}
	})

	t.Run("non-k8s profile yields 0 panels", func(t *testing.T) {
		panels := r.BuildPanels(both, profiles.ProfileService)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels for non-k8s profile, got %d", len(panels))
		}
	})
}
