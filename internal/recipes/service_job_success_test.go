package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceJobSuccess_Match(t *testing.T) {
	r := NewServiceJobSuccess()

	tests := []struct {
		name      string
		metric    string
		mtype     inventory.MetricType
		wantMatch bool
	}{
		{
			name:      "succeeded counter with failed pair matches",
			metric:    "worker_jobs_succeeded_total",
			mtype:     inventory.MetricTypeCounter,
			wantMatch: true,
		},
		{
			name:      "success counter with failure pair matches",
			metric:    "email_jobs_success_total",
			mtype:     inventory.MetricTypeCounter,
			wantMatch: true,
		},
		{
			name:      "failed counter alone does not match",
			metric:    "worker_jobs_failed_total",
			mtype:     inventory.MetricTypeCounter,
			wantMatch: false,
		},
		{
			name:      "failure counter alone does not match",
			metric:    "email_jobs_failure_total",
			mtype:     inventory.MetricTypeCounter,
			wantMatch: false,
		},
		{
			name:      "shape-B completed counter does not match",
			metric:    "jobs_completed_total",
			mtype:     inventory.MetricTypeCounter,
			wantMatch: false,
		},
		{
			name:      "succeeded suffix as gauge does not match",
			metric:    "worker_jobs_succeeded_total",
			mtype:     inventory.MetricTypeGauge,
			wantMatch: false,
		},
		{
			name:      "unrelated counter does not match",
			metric:    "something_else_total",
			mtype:     inventory.MetricTypeCounter,
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metric},
				Type:       tc.mtype,
			}
			got := r.Match(m)
			if got != tc.wantMatch {
				t.Errorf("Match(%q) = %v, want %v", tc.metric, got, tc.wantMatch)
			}
		})
	}
}

func TestServiceJobSuccess_BuildPanels(t *testing.T) {
	r := NewServiceJobSuccess()

	t.Run("both counters present yields 1 panel with 2 queries", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "worker_jobs_succeeded_total",
						Labels: []string{"instance", "job", "queue"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "worker_jobs_failed_total",
						Labels: []string{"instance", "job", "queue"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		panel := panels[0]
		if len(panel.Queries) != 2 {
			t.Fatalf("expected 2 query candidates, got %d", len(panel.Queries))
		}
		if !strings.Contains(panel.Title, "worker") {
			t.Errorf("panel title missing prefix, got %q", panel.Title)
		}
		if panel.Unit != "cps" {
			t.Errorf("expected unit cps, got %q", panel.Unit)
		}
		// Query 0: success rate
		if !strings.Contains(panel.Queries[0].Expr, "worker_jobs_succeeded_total") {
			t.Errorf("query[0] missing success metric, got %q", panel.Queries[0].Expr)
		}
		// Query 1: failure rate
		if !strings.Contains(panel.Queries[1].Expr, "worker_jobs_failed_total") {
			t.Errorf("query[1] missing failure metric, got %q", panel.Queries[1].Expr)
		}
	})

	t.Run("success/failure variant yields 1 panel with 2 queries", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "email_jobs_success_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "email_jobs_failure_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if len(panels[0].Queries) != 2 {
			t.Fatalf("expected 2 query candidates, got %d", len(panels[0].Queries))
		}
		if !strings.Contains(panels[0].Queries[0].Expr, "email_jobs_success_total") {
			t.Errorf("query[0] missing success metric, got %q", panels[0].Queries[0].Expr)
		}
		if !strings.Contains(panels[0].Queries[1].Expr, "email_jobs_failure_total") {
			t.Errorf("query[1] missing failure metric, got %q", panels[0].Queries[1].Expr)
		}
	})

	t.Run("only success counter present yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "worker_jobs_succeeded_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when failure counter absent, got %d", len(panels))
		}
	})

	t.Run("two distinct families yields 2 panels in deterministic order", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "worker_jobs_succeeded_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "email_jobs_success_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "worker_jobs_failed_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "email_jobs_failure_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 2 {
			t.Fatalf("expected 2 panels for two families, got %d", len(panels))
		}
		// Sorted by prefix: "email" < "worker"
		if !strings.Contains(panels[0].Title, "email") {
			t.Errorf("expected first panel for 'email' family, got %q", panels[0].Title)
		}
		if !strings.Contains(panels[1].Title, "worker") {
			t.Errorf("expected second panel for 'worker' family, got %q", panels[1].Title)
		}
	})

	t.Run("wrong profile yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "worker_jobs_succeeded_total"},
					Type:       inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "worker_jobs_failed_total"},
					Type:       inventory.MetricTypeCounter,
				},
			},
		}
		for _, prof := range []profiles.Profile{profiles.ProfileInfra, profiles.ProfileK8s} {
			if got := r.BuildPanels(inv, prof); len(got) != 0 {
				t.Errorf("expected 0 panels for profile %v, got %d", prof, len(got))
			}
		}
	})
}
