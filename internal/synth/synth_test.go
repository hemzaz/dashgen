package synth

import (
	"reflect"
	"testing"

	"dashgen/internal/classify"
	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
	"dashgen/internal/recipes"
)

// fixtureInventory builds a ClassifiedInventory with three known metric
// shapes: an HTTP rate counter, a latency histogram, and a process CPU
// counter.
func fixtureInventory() *classify.ClassifiedInventory {
	inv := &inventory.MetricInventory{Metrics: []inventory.MetricDescriptor{
		{
			Name:   "http_requests_total",
			Type:   inventory.MetricTypeCounter,
			Labels: []string{"job", "method", "route", "status_code"},
		},
		{
			Name:   "http_request_duration_seconds_bucket",
			Labels: []string{"job", "le", "route"},
		},
		{Name: "http_request_duration_seconds_sum"},
		{Name: "http_request_duration_seconds_count"},
		{
			Name:   "process_cpu_seconds_total",
			Type:   inventory.MetricTypeCounter,
			Labels: []string{"instance", "job"},
		},
	}}
	inv.Sort()
	return classify.Classify(inv)
}

func TestSynthesize_DeterministicAcrossRuns(t *testing.T) {
	reg := recipes.NewServiceRegistry()
	a := Synthesize(fixtureInventory(), profiles.ProfileService, reg)
	b := Synthesize(fixtureInventory(), profiles.ProfileService, reg)
	if a.UID != b.UID {
		t.Fatalf("dashboard UID mismatch across runs: %q vs %q", a.UID, b.UID)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("dashboards not bit-identical across runs")
	}
}

func TestSynthesize_RowsInProfileSectionOrder(t *testing.T) {
	reg := recipes.NewServiceRegistry()
	d := Synthesize(fixtureInventory(), profiles.ProfileService, reg)
	// The fixture should produce traffic, errors, latency, saturation.
	// overview has no recipes so it is omitted (SPECS Rule 5).
	wantTitles := []string{"traffic", "errors", "latency", "saturation"}
	if len(d.Rows) != len(wantTitles) {
		t.Fatalf("row count = %d, want %d (%v)", len(d.Rows), len(wantTitles), rowTitles(d.Rows))
	}
	for i, want := range wantTitles {
		if d.Rows[i].Title != want {
			t.Errorf("row[%d] title = %q, want %q", i, d.Rows[i].Title, want)
		}
	}
}

func TestSynthesize_PanelsHaveStableUIDs(t *testing.T) {
	reg := recipes.NewServiceRegistry()
	d := Synthesize(fixtureInventory(), profiles.ProfileService, reg)
	seen := map[string]bool{}
	for _, r := range d.Rows {
		for _, p := range r.Panels {
			if p.UID == "" {
				t.Errorf("panel %q has empty UID", p.Title)
			}
			if seen[p.UID] {
				t.Errorf("duplicate panel UID %q", p.UID)
			}
			seen[p.UID] = true
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no panels emitted for fixture; synth is likely broken")
	}
}

func TestSynthesize_EmitsDatasourceVariable(t *testing.T) {
	reg := recipes.NewServiceRegistry()
	d := Synthesize(fixtureInventory(), profiles.ProfileService, reg)
	if len(d.Variables) != 1 || d.Variables[0].Name != "datasource" {
		t.Fatalf("expected single datasource variable, got %+v", d.Variables)
	}
}

func TestSynthesize_OmitsEmptySections(t *testing.T) {
	// Only a plain counter without HTTP traits -> no recipes match.
	inv := &inventory.MetricInventory{Metrics: []inventory.MetricDescriptor{
		{Name: "random_total", Type: inventory.MetricTypeCounter},
	}}
	inv.Sort()
	classified := classify.Classify(inv)
	d := Synthesize(classified, profiles.ProfileService, recipes.NewServiceRegistry())
	if len(d.Rows) != 0 {
		t.Errorf("expected no rows for empty synthesis, got %d (%v)", len(d.Rows), rowTitles(d.Rows))
	}
}

func TestSynthesize_NilRegistrySafe(t *testing.T) {
	d := Synthesize(fixtureInventory(), profiles.ProfileService, nil)
	if d == nil {
		t.Fatalf("expected non-nil dashboard on nil registry")
	}
	if len(d.Rows) != 0 {
		t.Errorf("expected no rows with nil registry, got %d", len(d.Rows))
	}
}

func rowTitles(rows []ir.Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}
