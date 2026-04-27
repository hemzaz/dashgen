package recipes

// yaml_recipes_harness_test.go — parameterized test that walks
// internal/recipes/data/**/*.yaml and validates each recipe against a
// sibling <recipe>.testdata.json fixture (T1A.6).
//
// Phase 1A: data/ is empty; the test trivially passes with zero entries.
// Phase 1B onward: each migrated recipe gains a companion fixture that
// drives the assertions below.
//
// This harness is designed to be wrapped by `dashgen recipe test` (Phase 4B)
// — a thin CLI that calls into the same logic. Types are unexported for now;
// promote to production code in Phase 4B.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// =============================================================================
// Fixture schema types (see testdata/README.md for the full schema doc)
// =============================================================================

// testMetricEntry is one row in positive_metrics or negative_metrics.
type testMetricEntry struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Labels []string `json:"labels"`
	Traits []string `json:"traits"`
}

// expectedPanel is one assertion row in expected_panels.
type expectedPanel struct {
	TitleContains string `json:"title_contains"`
	QueryContains string `json:"query_contains"`
	Kind          string `json:"kind"`
}

// recipeTestData is the full decoded companion fixture.
type recipeTestData struct {
	PositiveMetrics []testMetricEntry `json:"positive_metrics"`
	NegativeMetrics []testMetricEntry `json:"negative_metrics"`
	ExpectedPanels  []expectedPanel   `json:"expected_panels"`
}

// =============================================================================
// Helpers
// =============================================================================

// makeViewFromEntry builds a ClassifiedMetricView from a testMetricEntry.
func makeViewFromEntry(e testMetricEntry) ClassifiedMetricView {
	return ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   e.Name,
			Type:   inventory.MetricType(e.Type),
			Labels: e.Labels,
		},
		Type:   inventory.MetricType(e.Type),
		Traits: e.Traits,
	}
}

// makeHarnessSnapshot builds a synthetic ClassifiedInventorySnapshot from views.
func makeHarnessSnapshot(views []ClassifiedMetricView) ClassifiedInventorySnapshot {
	descs := make([]inventory.MetricDescriptor, len(views))
	for i, v := range views {
		descs[i] = v.Descriptor
	}
	return ClassifiedInventorySnapshot{
		Inventory: &inventory.MetricInventory{Metrics: descs},
		Metrics:   views,
	}
}

// panelMatchesExpected returns true if panel satisfies all non-empty constraints in ep.
func panelMatchesExpected(panel ir.Panel, ep expectedPanel) bool {
	if ep.TitleContains != "" && !strings.Contains(panel.Title, ep.TitleContains) {
		return false
	}
	if ep.Kind != "" && string(panel.Kind) != ep.Kind {
		return false
	}
	if ep.QueryContains != "" {
		found := false
		for _, q := range panel.Queries {
			if strings.Contains(q.Expr, ep.QueryContains) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// anySatisfies returns true if at least one panel satisfies all ep constraints.
func anySatisfies(panels []ir.Panel, ep expectedPanel) bool {
	for _, p := range panels {
		if panelMatchesExpected(p, ep) {
			return true
		}
	}
	return false
}

// =============================================================================
// TestRecipesYAML_MatchAndBuild
// =============================================================================

// TestRecipesYAML_MatchAndBuild walks internal/recipes/data/**/*.yaml in
// deterministic (sorted) order and for each YAML recipe:
//
//  1. Loads + validates it via the loader (CUE schema check).
//  2. Constructs a YAMLRecipe (pre-parses templates; warms matcher cache).
//  3. Reads the sibling <recipe>.testdata.json, if present.
//  4. Asserts positive_metrics all Match; negative_metrics all do NOT match.
//  5. Asserts at least one produced panel satisfies each expected_panel row.
//
// Phase 1A: data/ is empty → passes trivially with zero iterations.
func TestRecipesYAML_MatchAndBuild(t *testing.T) {
	const dataDir = "data"

	// Collect all *.yaml paths under data/ in deterministic order.
	var yamlPaths []string
	err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Tolerate missing data/ — treated as empty.
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".yaml") {
			yamlPaths = append(yamlPaths, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", dataDir, err)
	}

	// Sort for determinism: two runs must process recipes in the same order.
	sort.Strings(yamlPaths)

	if len(yamlPaths) == 0 {
		t.Logf("internal/recipes/data/ is empty — no YAML recipes to test (Phase 1A; Phase 1B populates this directory)")
		return
	}

	for _, yamlPath := range yamlPaths {
		yamlPath := yamlPath // capture loop variable
		recipeName := strings.TrimSuffix(filepath.Base(yamlPath), ".yaml")

		t.Run(recipeName, func(t *testing.T) {
			ctx := context.Background()

			// 1. Load and validate the YAML recipe via the loader.
			loaded, err := LoadFile(ctx, LoaderConfig{}, yamlPath, SourceUser)
			if err != nil {
				t.Fatalf("LoadFile(%s): %v", yamlPath, err)
			}

			// 2. Construct YAMLRecipe (pre-parses templates, warms matcher).
			recipe, err := NewYAMLRecipe(loaded)
			if err != nil {
				t.Fatalf("NewYAMLRecipe(%s): %v", recipeName, err)
			}

			// 3. Read companion testdata.json — skip with a log if absent.
			tdPath := strings.TrimSuffix(yamlPath, ".yaml") + ".testdata.json"
			tdBytes, err := os.ReadFile(tdPath)
			if os.IsNotExist(err) {
				t.Logf("no companion fixture for %s — add %s to enable assertions (see testdata/README.md)", recipeName, tdPath)
				return
			}
			if err != nil {
				t.Fatalf("reading %s: %v", tdPath, err)
			}

			var td recipeTestData
			if err := json.Unmarshal(tdBytes, &td); err != nil {
				t.Fatalf("json.Unmarshal %s: %v", tdPath, err)
			}

			// 4a. Assert positive metrics — all must match.
			for _, pm := range td.PositiveMetrics {
				view := makeViewFromEntry(pm)
				if !recipe.Match(view) {
					t.Errorf("POSITIVE MISS: recipe %q did NOT match metric %q (type=%s traits=%v) — expected a match",
						recipe.Name(), pm.Name, pm.Type, pm.Traits)
				}
			}

			// 4b. Assert negative metrics (look-alike negation) — none must match.
			for _, nm := range td.NegativeMetrics {
				view := makeViewFromEntry(nm)
				if recipe.Match(view) {
					t.Errorf("NEGATIVE HIT (look-alike regression): recipe %q incorrectly matched metric %q (type=%s traits=%v) — expected NO match",
						recipe.Name(), nm.Name, nm.Type, nm.Traits)
				}
			}

			// 5. Assert expected panels.
			if len(td.ExpectedPanels) == 0 {
				return
			}
			positiveViews := make([]ClassifiedMetricView, len(td.PositiveMetrics))
			for i, pm := range td.PositiveMetrics {
				positiveViews[i] = makeViewFromEntry(pm)
			}
			snap := makeHarnessSnapshot(positiveViews)
			profile := profiles.Profile(loaded.Spec.Metadata.Profile)
			panels := recipe.BuildPanels(snap, profile)

			for _, ep := range td.ExpectedPanels {
				if !anySatisfies(panels, ep) {
					t.Errorf("expected_panels: no panel satisfies {title_contains:%q, query_contains:%q, kind:%q} among %d panel(s) for recipe %q",
						ep.TitleContains, ep.QueryContains, ep.Kind, len(panels), recipe.Name())
					for i, p := range panels {
						exprs := make([]string, len(p.Queries))
						for j, q := range p.Queries {
							exprs[j] = q.Expr
						}
						t.Logf("  panel[%d]: title=%q kind=%q queries=%v", i, p.Title, p.Kind, exprs)
					}
				}
			}
		})
	}
}

// =============================================================================
// TestExampleTestdataRoundtrip
// =============================================================================

// TestExampleTestdataRoundtrip verifies that testdata/example.testdata.json
// round-trips cleanly through json.Unmarshal and satisfies the schema
// minimums documented in testdata/README.md.
func TestExampleTestdataRoundtrip(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "example.testdata.json"))
	if err != nil {
		t.Fatalf("reading example.testdata.json: %v", err)
	}

	var td recipeTestData
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if len(td.PositiveMetrics) < 2 {
		t.Errorf("example.testdata.json: want ≥2 positive_metrics, got %d", len(td.PositiveMetrics))
	}
	if len(td.NegativeMetrics) < 2 {
		t.Errorf("example.testdata.json: want ≥2 negative_metrics, got %d", len(td.NegativeMetrics))
	}
	if len(td.ExpectedPanels) < 1 {
		t.Errorf("example.testdata.json: want ≥1 expected_panels, got %d", len(td.ExpectedPanels))
	}
	if len(td.PositiveMetrics) > 0 {
		pm := td.PositiveMetrics[0]
		if pm.Name == "" {
			t.Error("positive_metrics[0].name must be non-empty")
		}
		if pm.Type == "" {
			t.Error("positive_metrics[0].type must be non-empty")
		}
	}
}
