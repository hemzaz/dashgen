package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceTLSExpiry_Match(t *testing.T) {
	r := NewServiceTLSExpiry()

	tests := []struct {
		name      string
		metric    string
		mtype     inventory.MetricType
		wantMatch bool
	}{
		// Positives — each recognized suffix, correct gauge type
		{
			name:      "caddy tls_not_after_timestamp gauge",
			metric:    "caddy_tls_not_after_timestamp",
			mtype:     inventory.MetricTypeGauge,
			wantMatch: true,
		},
		{
			name:      "haproxy cert_expiry_timestamp_seconds gauge",
			metric:    "haproxy_cert_expiry_timestamp_seconds",
			mtype:     inventory.MetricTypeGauge,
			wantMatch: true,
		},
		{
			name:      "traefik ssl_cert_not_after gauge",
			metric:    "traefik_ssl_cert_not_after",
			mtype:     inventory.MetricTypeGauge,
			wantMatch: true,
		},
		// Negatives — wrong type or non-matching name
		{
			name:      "counter variant of matching name must not match",
			metric:    "caddy_tls_not_after_timestamp",
			mtype:     inventory.MetricTypeCounter,
			wantMatch: false,
		},
		{
			name:      "unrelated gauge must not match",
			metric:    "process_resident_memory_bytes",
			mtype:     inventory.MetricTypeGauge,
			wantMatch: false,
		},
		{
			name:      "probe_ssl_earliest_cert_expiry is NOT a suffix match",
			metric:    "probe_ssl_earliest_cert_expiry",
			mtype:     inventory.MetricTypeGauge,
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
				t.Errorf("Match(%q, type=%v) = %v, want %v", tc.metric, tc.mtype, got, tc.wantMatch)
			}
		})
	}
}

func TestServiceTLSExpiry_BuildPanels(t *testing.T) {
	r := NewServiceTLSExpiry()

	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "caddy_tls_not_after_timestamp",
				Labels: []string{"instance", "job"},
			},
			Type: inventory.MetricTypeGauge,
		}},
	}

	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}

	p := panels[0]

	if len(p.Queries) != 1 {
		t.Fatalf("expected 1 query candidate, got %d", len(p.Queries))
	}

	expr := p.Queries[0].Expr
	if !strings.Contains(expr, "time()") {
		t.Errorf("expression %q missing time()", expr)
	}
	if !strings.Contains(expr, "/ 86400") {
		t.Errorf("expression %q missing / 86400", expr)
	}
	if !strings.Contains(p.Title, "caddy_tls_not_after_timestamp") {
		t.Errorf("title %q does not contain metric name", p.Title)
	}
	if p.Unit != "d" {
		t.Errorf("expected unit %q, got %q", "d", p.Unit)
	}

	// Non-service profile must yield no panels.
	if got := r.BuildPanels(inv, profiles.ProfileInfra); len(got) != 0 {
		t.Errorf("expected 0 panels for infra profile, got %d", len(got))
	}
}
