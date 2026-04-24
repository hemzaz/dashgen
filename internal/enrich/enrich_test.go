package enrich

import (
	"context"
	"testing"
)

// TestNoopEnricher_ImplementsInterface is a compile-time assertion that
// NoopEnricher satisfies the Enricher interface.
var _ Enricher = NoopEnricher{}

func TestNoopEnricher_ReturnsEmptyOutputs(t *testing.T) {
	e := NewNoopEnricher()
	ctx := context.Background()

	t.Run("Describe", func(t *testing.T) {
		d := e.Describe()
		if d.Provider != "noop" {
			t.Errorf("Describe().Provider = %q, want %q", d.Provider, "noop")
		}
		if !d.Offline {
			t.Error("Describe().Offline = false, want true")
		}
	})

	t.Run("ClassifyUnknown", func(t *testing.T) {
		in := ClassifyInput{
			Metrics: []MetricBrief{
				{Name: "some_metric_total", Type: "counter", Labels: []string{"job"}},
			},
		}
		out, err := e.ClassifyUnknown(ctx, in)
		if err != nil {
			t.Fatalf("ClassifyUnknown returned error: %v", err)
		}
		if len(out.Hints) != 0 {
			t.Errorf("ClassifyUnknown Hints length = %d, want 0", len(out.Hints))
		}
	})

	t.Run("EnrichTitles", func(t *testing.T) {
		in := TitleInput{
			Requests: []PanelTitleRequest{
				{
					PanelUID:        "uid-1",
					MechanicalTitle: "Request rate: api_http_requests_total",
					MetricName:      "api_http_requests_total",
					Section:         "traffic",
					Rationale:       "Shows the per-second request rate.",
				},
			},
		}
		out, err := e.EnrichTitles(ctx, in)
		if err != nil {
			t.Fatalf("EnrichTitles returned error: %v", err)
		}
		if len(out.Proposals) != 0 {
			t.Errorf("EnrichTitles Proposals length = %d, want 0", len(out.Proposals))
		}
	})

	t.Run("EnrichRationale", func(t *testing.T) {
		in := RationaleInput{
			Requests: []PanelRationaleRequest{
				{
					PanelUID:        "uid-1",
					MechanicalTitle: "Request rate: api_http_requests_total",
					MetricName:      "api_http_requests_total",
					Section:         "traffic",
					Rationale:       "Shows the per-second request rate.",
					QueryExprs:      []string{`rate(api_http_requests_total[5m])`},
				},
			},
		}
		out, err := e.EnrichRationale(ctx, in)
		if err != nil {
			t.Fatalf("EnrichRationale returned error: %v", err)
		}
		if len(out.Proposals) != 0 {
			t.Errorf("EnrichRationale Proposals length = %d, want 0", len(out.Proposals))
		}
	})
}
