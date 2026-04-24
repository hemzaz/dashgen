package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sEtcdCommit_Match(t *testing.T) {
	r := NewK8sEtcdCommit()
	cases := []struct {
		name    string
		metric  string
		typ     inventory.MetricType
		wantHit bool
	}{
		{
			name:    "exact_name_histogram_matches",
			metric:  "etcd_disk_backend_commit_duration_seconds",
			typ:     inventory.MetricTypeHistogram,
			wantHit: true,
		},
		{
			// Different name — WAL fsync latency is a distinct etcd signal that
			// must not be claimed by this recipe.
			name:    "wrong_name_rejected",
			metric:  "etcd_disk_wal_fsync_duration_seconds",
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		{
			// Correct name but wrong type — must not match.
			name:    "counter_same_name_rejected",
			metric:  "etcd_disk_backend_commit_duration_seconds",
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metric},
				Type:       tc.typ,
				Traits:     []string{"latency_histogram"},
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(name=%q, type=%s) = %v, want %v", tc.metric, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestK8sEtcdCommit_BuildPanels(t *testing.T) {
	r := NewK8sEtcdCommit()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "etcd_disk_backend_commit_duration_seconds",
				Labels: []string{"instance", "job", "le"},
			},
			Type:   inventory.MetricTypeHistogram,
			Traits: []string{"latency_histogram"},
		}},
	}

	t.Run("k8s_profile_produces_panel", func(t *testing.T) {
		panels := r.BuildPanels(inv, profiles.ProfileK8s)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		panel := panels[0]
		if len(panel.Queries) != 3 {
			t.Fatalf("expected 3 quantile queries, got %d", len(panel.Queries))
		}
		for i, q := range panel.Queries {
			if !strings.Contains(q.Expr, "histogram_quantile") {
				t.Errorf("query[%d] must use histogram_quantile, got %q", i, q.Expr)
			}
			if !strings.Contains(q.Expr, "etcd_disk_backend_commit_duration_seconds_bucket") {
				t.Errorf("query[%d] must reference _bucket series, got %q", i, q.Expr)
			}
			if !strings.Contains(q.Expr, "le") {
				t.Errorf("query[%d] must group by le, got %q", i, q.Expr)
			}
		}
		// Percentiles in low-to-high order.
		wants := []string{"0.50", "0.95", "0.99"}
		for i, want := range wants {
			if !strings.Contains(panel.Queries[i].Expr, "histogram_quantile("+want) {
				t.Errorf("query[%d] expected quantile %s, got %q", i, want, panel.Queries[i].Expr)
			}
		}
		// Legend format should carry pN prefix.
		legendPrefixes := []string{"p50", "p95", "p99"}
		for i, prefix := range legendPrefixes {
			if !strings.HasPrefix(panel.Queries[i].LegendFormat, prefix) {
				t.Errorf("query[%d] legend %q should start with %q", i, panel.Queries[i].LegendFormat, prefix)
			}
		}
	})

	t.Run("wrong_profile_produces_no_panels", func(t *testing.T) {
		for _, prof := range []profiles.Profile{profiles.ProfileService, profiles.ProfileInfra} {
			panels := r.BuildPanels(inv, prof)
			if len(panels) != 0 {
				t.Errorf("profile %q: expected 0 panels, got %d", prof, len(panels))
			}
		}
	})
}
