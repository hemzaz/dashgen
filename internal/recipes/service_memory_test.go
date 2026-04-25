package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceMemory_Match(t *testing.T) {
	r := NewServiceMemory()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "process_resident_memory_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on process_resident_memory_bytes")
	}
	posContainer := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "container_memory_working_set_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(posContainer) {
		t.Errorf("expected match on container_memory_working_set_bytes")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "go_memstats_alloc_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on unapproved memory metric")
	}
}

func TestServiceMemory_BuildPanels(t *testing.T) {
	r := NewServiceMemory()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "process_resident_memory_bytes",
				Labels: []string{"instance", "job"},
			},
			Type: inventory.MetricTypeGauge,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "sum by (instance, job) (process_resident_memory_bytes)") {
		t.Errorf("unexpected expression: %q", expr)
	}
	if panels[0].Unit != "bytes" {
		t.Errorf("expected bytes unit, got %q", panels[0].Unit)
	}
}

func TestNewServiceRegistry_AllRecipesRegisteredInSortedOrder(t *testing.T) {
	reg := NewServiceRegistry()
	got := reg.All()
	want := []string{
		"service_cache_hits",
		"service_client_http",
		"service_cpu",
		"service_db_pool",
		"service_db_query_latency",
		"service_gc_pause",
		"service_goroutines",
		"service_grpc_errors",
		"service_grpc_latency",
		"service_grpc_rate",
		"service_http_errors",
		"service_http_latency",
		"service_http_rate",
		"service_job_success",
		"service_kafka_consumer_lag",
		"service_memory",
		"service_request_size",
		"service_response_size",
		"service_tls_expiry",
	}
	if len(got) != len(want) {
		t.Fatalf("recipe count = %d, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name() != name {
			t.Errorf("recipe[%d] = %q, want %q", i, got[i].Name(), name)
		}
	}
}

func TestSafeGroupLabels_ExcludesBanned(t *testing.T) {
	m := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Labels: []string{"job", "user_id", "trace_id"}},
	}
	got := safeGroupLabels(m)
	for _, g := range got {
		if g == "user_id" || g == "trace_id" {
			t.Errorf("banned label %q leaked into grouping", g)
		}
	}
}
