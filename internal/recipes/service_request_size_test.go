package recipes

import (
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceRequestSize_Match(t *testing.T) {
	r := NewServiceRequestSize()

	// Positive: bucket form with both HTTP-shape labels.
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   "http_request_size_bytes_bucket",
			Labels: []string{"job", "method", "handler", "le"},
		},
		Type: inventory.MetricTypeHistogram,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on %s with method+handler labels", pos.Descriptor.Name)
	}

	negatives := []struct {
		name   string
		labels []string
		reason string
	}{
		{
			// gRPC byte histogram: correct suffix family but no HTTP-shape label.
			"grpc_server_msg_received_bytes",
			[]string{"job", "grpc_method", "grpc_service"},
			"no method or handler label — must not match",
		},
		{
			// DB byte histogram: _bucket suffix but no HTTP-shape label.
			"db_query_bytes_bucket",
			[]string{"job", "query_type"},
			"no method or handler label — must not match",
		},
		{
			// HTTP latency histogram: has HTTP-shape labels but wrong name suffix.
			"http_request_duration_seconds_bucket",
			[]string{"job", "method", "handler", "le"},
			"name does not end in _request_size_bytes — must not match",
		},
	}
	for _, tc := range negatives {
		m := ClassifiedMetricView{
			Descriptor: inventory.MetricDescriptor{
				Name:   tc.name,
				Labels: tc.labels,
			},
			Type: inventory.MetricTypeHistogram,
		}
		if r.Match(m) {
			t.Errorf("unexpected match on %s: %s", tc.name, tc.reason)
		}
	}
}

func TestServiceRequestSize_BuildPanels(t *testing.T) {
	r := NewServiceRequestSize()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "http_request_size_bytes_bucket",
				Labels: []string{"job", "method", "handler", "le"},
			},
			Type: inventory.MetricTypeHistogram,
		}},
	}

	t.Run("service profile produces 2 candidates with le in group", func(t *testing.T) {
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		p := panels[0]
		if len(p.Queries) != 2 {
			t.Fatalf("expected 2 query candidates (p50/p95), got %d", len(p.Queries))
		}
		wants := []string{"0.50", "0.95"}
		pql := parser.NewParser(parser.Options{})
		for i, want := range wants {
			expr := p.Queries[i].Expr
			if !strings.Contains(expr, "histogram_quantile("+want) {
				t.Errorf("query %d: expected quantile %s in %q", i, want, expr)
			}
			if !strings.Contains(expr, "le") {
				t.Errorf("query %d: must group by le, got %q", i, expr)
			}
			if _, err := pql.ParseExpr(expr); err != nil {
				t.Errorf("query %d: PromQL parse error: %v\nexpr: %s", i, err, expr)
			}
		}
	})

	t.Run("non-service profile returns nil", func(t *testing.T) {
		for _, prof := range []profiles.Profile{profiles.ProfileInfra, profiles.ProfileK8s} {
			panels := r.BuildPanels(inv, prof)
			if panels != nil {
				t.Errorf("profile %v: expected nil, got %v", prof, panels)
			}
		}
	})
}
