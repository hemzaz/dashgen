package enrich

import (
	"strings"
	"testing"
)

func TestValidateBriefs_PureNamesOK(t *testing.T) {
	briefs := []MetricBrief{
		{
			Name:   "api_http_requests_total",
			Type:   "counter",
			Labels: []string{"job", "method", "status"},
		},
		{
			Name:   "node_cpu_seconds_total",
			Type:   "counter",
			Labels: []string{"cpu", "instance", "mode"},
		},
	}
	if err := ValidateBriefs(briefs); err != nil {
		t.Fatalf("expected nil error for label-name-only briefs, got: %v", err)
	}
}

func TestValidateBriefs_KeyValueRejected(t *testing.T) {
	cases := []struct {
		name   string
		briefs []MetricBrief
	}{
		{
			name: "bare key=value",
			briefs: []MetricBrief{
				{Name: "api_http_requests_total", Labels: []string{"job", "pod=checkout"}},
			},
		},
		{
			name: "quoted prometheus matcher form",
			briefs: []MetricBrief{
				{Name: "api_http_requests_total", Labels: []string{`pod="checkout"`}},
			},
		},
		{
			name: "value leak in second brief",
			briefs: []MetricBrief{
				{Name: "clean_metric", Labels: []string{"job"}},
				{Name: "leaky_metric", Labels: []string{"namespace=prod"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBriefs(tc.briefs)
			if err == nil {
				t.Fatal("expected non-nil error for value-shaped label, got nil")
			}
		})
	}
}

func TestValidateBriefs_EmptyOK(t *testing.T) {
	if err := ValidateBriefs(nil); err != nil {
		t.Fatalf("nil briefs should be valid, got: %v", err)
	}
	if err := ValidateBriefs([]MetricBrief{}); err != nil {
		t.Fatalf("empty briefs should be valid, got: %v", err)
	}
	// Brief with no labels is also valid.
	if err := ValidateBriefs([]MetricBrief{{Name: "no_labels_metric"}}); err != nil {
		t.Fatalf("brief with no labels should be valid, got: %v", err)
	}
}

func TestValidateBriefs_NamesWithUnderscoreOK(t *testing.T) {
	briefs := []MetricBrief{
		{
			Name: "kube_pod_status",
			Labels: []string{
				"request_id",
				"kube_pod_status_phase",
				"http_status_code",
				"k8s_node_name",
			},
		},
	}
	if err := ValidateBriefs(briefs); err != nil {
		t.Fatalf("underscore-bearing label names should be valid, got: %v", err)
	}
}

func TestValidateBriefs_ErrorNamesOffender(t *testing.T) {
	briefs := []MetricBrief{
		{Name: "clean_metric", Labels: []string{"job"}},
		{Name: "leaky_metric", Labels: []string{"job", `pod="checkout-7c9"`}},
	}
	err := ValidateBriefs(briefs)
	if err == nil {
		t.Fatal("expected non-nil error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "leaky_metric") {
		t.Errorf("error message %q must include offending metric name %q", msg, "leaky_metric")
	}
	if !strings.Contains(msg, `pod="checkout-7c9"`) {
		t.Errorf("error message %q must include the offending label string", msg)
	}
}
