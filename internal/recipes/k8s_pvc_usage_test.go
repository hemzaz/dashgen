package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sPVCUsage_Match(t *testing.T) {
	r := NewK8sPVCUsage()

	// Positive: the descriptor this recipe keys on.
	posAvail := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kubelet_volume_stats_available_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(posAvail) {
		t.Errorf("expected match on kubelet_volume_stats_available_bytes")
	}

	// Negative: capacity metric alone must NOT match (Match keys only on available).
	negCapacity := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kubelet_volume_stats_capacity_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negCapacity) {
		t.Errorf("expected no match on kubelet_volume_stats_capacity_bytes")
	}

	// Negative: look-alike from cAdvisor filesystem family.
	negContainerFS := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "container_fs_limit_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negContainerFS) {
		t.Errorf("expected no match on container_fs_limit_bytes")
	}
}

func TestK8sPVCUsage_BuildPanels(t *testing.T) {
	r := NewK8sPVCUsage()

	both := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "kubelet_volume_stats_available_bytes",
					Labels: []string{"namespace", "persistentvolumeclaim"},
				},
				Type: inventory.MetricTypeGauge,
			},
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "kubelet_volume_stats_capacity_bytes",
					Labels: []string{"namespace", "persistentvolumeclaim"},
				},
				Type: inventory.MetricTypeGauge,
			},
		},
	}

	panels := r.BuildPanels(both, profiles.ProfileK8s)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	if len(panels[0].Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(panels[0].Queries))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.HasPrefix(strings.TrimSpace(expr), "1 -") {
		t.Errorf("expected expression to start with '1 -', got %q", expr)
	}
	if !strings.Contains(expr, "kubelet_volume_stats_available_bytes") {
		t.Errorf("expression missing kubelet_volume_stats_available_bytes: %q", expr)
	}
	if !strings.Contains(expr, "kubelet_volume_stats_capacity_bytes") {
		t.Errorf("expression missing kubelet_volume_stats_capacity_bytes: %q", expr)
	}

	// Missing capacity metric → 0 panels.
	onlyAvail := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "kubelet_volume_stats_available_bytes",
					Labels: []string{"namespace", "persistentvolumeclaim"},
				},
				Type: inventory.MetricTypeGauge,
			},
		},
	}
	if got := r.BuildPanels(onlyAvail, profiles.ProfileK8s); len(got) != 0 {
		t.Errorf("expected 0 panels when capacity metric absent, got %d", len(got))
	}

	// Wrong profile → 0 panels.
	if got := r.BuildPanels(both, profiles.ProfileInfra); len(got) != 0 {
		t.Errorf("expected 0 panels for non-k8s profile, got %d", len(got))
	}
}
