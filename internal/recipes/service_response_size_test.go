package recipes

import (
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceResponseSize_Match(t *testing.T) {
	r := NewServiceResponseSize()

	// Positive: bucket suffix with method+handler labels.
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   "http_response_size_bytes_bucket",
			Labels: []string{"method", "handler", "status_code", "le"},
		},
		Type: inventory.MetricTypeHistogram,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on http_response_size_bytes_bucket with method+handler")
	}

	// Negative: sibling recipe (_request_size_bytes) must NOT match.
	reqSize := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   "http_request_size_bytes_bucket",
			Labels: []string{"method", "handler", "le"},
		},
		Type: inventory.MetricTypeHistogram,
	}
	if r.Match(reqSize) {
		t.Errorf("must NOT match http_request_size_bytes_bucket (sibling recipe)")
	}

	// Negative: gRPC sent bytes — wrong naming pattern.
	grpcSent := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   "grpc_server_msg_sent_bytes",
			Labels: []string{"grpc_method"},
		},
		Type: inventory.MetricTypeHistogram,
	}
	if r.Match(grpcSent) {
		t.Errorf("must NOT match grpc_server_msg_sent_bytes")
	}

	// Negative: unrelated histogram (latency, not size).
	latency := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   "http_request_duration_seconds_bucket",
			Labels: []string{"method", "handler", "le"},
		},
		Type: inventory.MetricTypeHistogram,
	}
	if r.Match(latency) {
		t.Errorf("must NOT match http_request_duration_seconds_bucket")
	}
}

func TestServiceResponseSize_BuildPanels(t *testing.T) {
	r := NewServiceResponseSize()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "http_response_size_bytes_bucket",
				Labels: []string{"job", "le", "method", "handler"},
			},
			Type: inventory.MetricTypeHistogram,
		}},
	}

	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	if len(panels[0].Queries) != 2 {
		t.Fatalf("expected 2 quantile queries (p50/p95), got %d", len(panels[0].Queries))
	}

	wants := []string{"0.50", "0.95"}
	for i, want := range wants {
		expr := panels[0].Queries[i].Expr
		if !strings.Contains(expr, "histogram_quantile("+want) {
			t.Errorf("query %d expected quantile %s, got %q", i, want, expr)
		}
		if !strings.Contains(expr, "le") {
			t.Errorf("query %d must group by le, got %q", i, expr)
		}
		p := parser.NewParser(parser.Options{})
		if _, err := p.ParseExpr(expr); err != nil {
			t.Errorf("query %d expr is not valid PromQL: %v\nexpr: %s", i, err, expr)
		}
	}

	// Non-service profiles must return nil.
	if got := r.BuildPanels(inv, profiles.ProfileInfra); got != nil {
		t.Errorf("expected nil for non-service profile, got %d panels", len(got))
	}
}
