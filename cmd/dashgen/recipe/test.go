package recipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dashgen/internal/classify"
	"dashgen/internal/discover"
	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
	"dashgen/internal/recipes"
	"dashgen/internal/safety"
	"dashgen/internal/validate"
)

// Sentinel errors for exit-code mapping in main.exitCodeFor.
// Each maps to a distinct exit code per RECIPES-CLI.md §3.6.
var (
	// ErrTestLoadFailure is returned when one or more recipe files cannot
	// be loaded (file missing, schema invalid, template parse failed).
	// main.exitCodeFor maps this to exit code 1.
	ErrTestLoadFailure = errors.New("recipe test: recipe load failed")

	// ErrTestFixtureError is returned when the fixture directory is
	// missing or malformed.
	// main.exitCodeFor maps this to exit code 2.
	ErrTestFixtureError = errors.New("recipe test: fixture error")
)

// queryPreviewLen caps the rendered-query string length in non-verbose
// text output. The full expression is preserved in JSON output and in
// --verbose text output.
const queryPreviewLen = 120

type testFlags struct {
	fixtureDir string
	metric     string
	profile    string
	output     string
	verbose    bool
}

func newTestCmd() *cobra.Command {
	var f testFlags

	cmd := &cobra.Command{
		Use:   "test <file>...",
		Short: "Run recipe(s) against a fixture and report matches + panels",
		Long: `Load one or more recipe YAML files, classify a fixture inventory, and
report which metrics each recipe matched plus the panels it emits — without
writing a dashboard. Queries are scored through the same 5-stage validate
pipeline used by 'dashgen generate', backed by the fixture client.

Examples:
  dashgen recipe test mycorp_queue_depth.yaml --fixture testdata/fixtures/service-realistic
  dashgen recipe test ./recipes/*.yaml --fixture testdata/fixtures/service-realistic --output json
  dashgen recipe test foo.yaml --fixture testdata/fixtures/infra-realistic --metric node_cpu --verbose

Exit codes:
  0  success — recipe(s) loaded, fixture tested
  1  recipe load error (file missing, schema invalid, template parse failed)
  2  fixture error (fixture not found or malformed)`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTest(cmd, f, args)
		},
	}

	cmd.Flags().StringVar(&f.fixtureDir, "fixture", "",
		"fixture directory (required)")
	cmd.Flags().StringVar(&f.metric, "metric", "",
		"filter to a specific metric name (substring match against fixture metrics)")
	cmd.Flags().StringVar(&f.profile, "profile", "",
		"override the recipe's declared profile: service, infra, or k8s")
	cmd.Flags().StringVar(&f.output, "output", "text",
		"output format: text or json")
	cmd.Flags().BoolVar(&f.verbose, "verbose", false,
		"emit full rendered query strings (otherwise truncated in text output)")

	return cmd
}

// =============================================================================
// Result types — used for both text and JSON renderers.
// =============================================================================

type testRecipeResult struct {
	Recipe     string         `json:"recipe"`
	Profile    string         `json:"profile"`
	File       string         `json:"file"`
	LoadError  string         `json:"load_error,omitempty"`
	Matches    []testMatch    `json:"matches"`
	Panels     []testPanel    `json:"panels"`
	NonMatches []testNonMatch `json:"non_matches,omitempty"`
}

type testMatch struct {
	Metric string `json:"metric"`
	Type   string `json:"type"`
}

type testPanel struct {
	Title    string   `json:"title"`
	Verdict  string   `json:"verdict"`
	Query    string   `json:"query"`
	Refusal  string   `json:"refusal,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type testNonMatch struct {
	Metric string `json:"metric"`
	Reason string `json:"reason"`
}

// =============================================================================
// Run
// =============================================================================

func runTest(cmd *cobra.Command, f testFlags, files []string) error {
	if err := validateTestFlags(f); err != nil {
		return err
	}

	src, err := discover.NewFixtureSource(f.fixtureDir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTestFixtureError, err)
	}
	client := discover.NewFixtureClient(src)

	ctx := context.Background()
	raw, err := src.Discover(ctx, discover.Selector{MetricMatch: f.metric})
	if err != nil {
		return fmt.Errorf("%w: discover fixture: %w", ErrTestFixtureError, err)
	}
	inv := rawToInventory(raw)
	inv.Sort()
	classified := classify.Classify(inv)
	snapshot := buildSnapshot(classified)

	policy := safety.NewPolicy(nil)
	pipeline := validate.New(client, policy, validate.Options{})

	results := make([]testRecipeResult, 0, len(files))
	loadFailed := false
	for _, file := range files {
		res, failed := testOneFile(ctx, file, f, snapshot, pipeline)
		results = append(results, res)
		if failed {
			loadFailed = true
		}
	}

	if f.output == "json" {
		return emitTestJSON(cmd, results, loadFailed)
	}
	return emitTestText(cmd, results, f.verbose, loadFailed)
}

// validateTestFlags checks flag values before any I/O.
func validateTestFlags(f testFlags) error {
	if f.fixtureDir == "" {
		return fmt.Errorf("flag --fixture: required")
	}
	switch f.output {
	case "text", "json":
	default:
		return fmt.Errorf("flag --output: %q must be one of [text json]", f.output)
	}
	if f.profile != "" {
		switch f.profile {
		case "service", "infra", "k8s":
		default:
			return fmt.Errorf("flag --profile: %q must be one of [service infra k8s]", f.profile)
		}
	}
	return nil
}

// testOneFile runs the full match + build + validate pipeline against a
// single recipe file. Returns the result and a boolean indicating whether
// the recipe failed to load (causing the overall command to exit 1).
func testOneFile(ctx context.Context, file string, f testFlags, snapshot recipes.ClassifiedInventorySnapshot, pipeline *validate.Pipeline) (testRecipeResult, bool) {
	res := testRecipeResult{
		File:    file,
		Matches: []testMatch{},
		Panels:  []testPanel{},
	}

	loaded, err := recipes.LoadFile(ctx, recipes.LoaderConfig{}, file, recipes.SourceUser)
	if err != nil {
		res.LoadError = err.Error()
		return res, true
	}

	rec, err := recipes.NewYAMLRecipe(loaded)
	if err != nil {
		res.LoadError = err.Error()
		return res, true
	}

	res.Recipe = rec.Name()
	res.Profile = loaded.Spec.Metadata.Profile

	// Per-metric Match scan. Records matches and, for metrics named in the
	// recipe's name_equals* clauses, records non-match reasons.
	mentioned := mentionedNames(loaded.Spec.Match)
	for _, view := range snapshot.Metrics {
		if rec.Match(view) {
			res.Matches = append(res.Matches, testMatch{
				Metric: view.Descriptor.Name,
				Type:   string(view.Type),
			})
			continue
		}
		if mentioned[view.Descriptor.Name] {
			res.NonMatches = append(res.NonMatches, testNonMatch{
				Metric: view.Descriptor.Name,
				Reason: nonMatchReason(loaded.Spec.Match, view),
			})
		}
	}

	// BuildPanels — uses the recipe's declared profile unless overridden.
	prof := profiles.Profile(loaded.Spec.Metadata.Profile)
	if f.profile != "" {
		prof = profiles.Profile(f.profile)
	}
	panels := rec.BuildPanels(snapshot, prof)

	// Score each candidate query through the 5-stage validate pipeline.
	for _, p := range panels {
		for i := range p.Queries {
			vr := pipeline.Validate(ctx, &p.Queries[i])
			res.Panels = append(res.Panels, testPanel{
				Title:    p.Title,
				Verdict:  verdictLabel(vr.Verdict),
				Query:    p.Queries[i].Expr,
				Refusal:  vr.RefusalReason,
				Warnings: vr.WarningCodes,
			})
		}
	}

	return res, false
}

// mentionedNames walks a MatchPredicate AST and collects every metric
// name appearing in name_equals / name_equals_any clauses (recursively
// across any_of / all_of / not). Used to gate non-match reporting per
// RECIPES-CLI.md §3.6: only metrics the recipe specifically targets get
// a "why didn't it match" entry.
func mentionedNames(p recipes.MatchPredicate) map[string]bool {
	out := map[string]bool{}
	walkPredicate(p, out)
	return out
}

func walkPredicate(p recipes.MatchPredicate, out map[string]bool) {
	if p.NameEquals != "" {
		out[p.NameEquals] = true
	}
	for _, n := range p.NameEqualsAny {
		out[n] = true
	}
	for _, sub := range p.AnyOf {
		walkPredicate(sub, out)
	}
	for _, sub := range p.AllOf {
		walkPredicate(sub, out)
	}
	if p.Not != nil {
		walkPredicate(*p.Not, out)
	}
}

// nonMatchReason returns a short, honest explanation of why m did not
// satisfy the top-level predicate. The full evaluation tree belongs to
// 'dashgen recipe explain' (Phase 6B); here we surface the most common
// reasons (type mismatch, trait absence) for diagnosability.
func nonMatchReason(p recipes.MatchPredicate, m recipes.ClassifiedMetricView) string {
	if p.Type != "" && string(m.Type) != p.Type {
		return fmt.Sprintf("type mismatch: predicate expects %s, metric is %s", p.Type, m.Type)
	}
	for _, t := range p.AllTraits {
		if !m.HasTrait(t) {
			return fmt.Sprintf("missing required trait: %s", t)
		}
	}
	if len(p.AnyTrait) > 0 {
		ok := false
		for _, t := range p.AnyTrait {
			if m.HasTrait(t) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Sprintf("metric has none of the any_trait set: %v", p.AnyTrait)
		}
	}
	for _, t := range p.NoneTrait {
		if m.HasTrait(t) {
			return fmt.Sprintf("metric carries excluded trait: %s", t)
		}
	}
	return "predicate not satisfied (use 'dashgen recipe explain' for details)"
}

// verdictLabel returns the human-readable form of an ir.Verdict for
// presentation in text/JSON output.
func verdictLabel(v ir.Verdict) string {
	switch v {
	case ir.VerdictAccept:
		return "Accept"
	case ir.VerdictAcceptWithWarning:
		return "AcceptWithWarning"
	case ir.VerdictRefuse:
		return "Refuse"
	}
	return string(v)
}

// =============================================================================
// Renderers
// =============================================================================

func emitTestText(cmd *cobra.Command, results []testRecipeResult, verbose bool, loadFailed bool) error {
	w := cmd.OutOrStdout()

	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if r.LoadError != "" {
			fmt.Fprintf(w, "Recipe (load failed): %s\n", r.File)
			fmt.Fprintf(w, "  error: %s\n", r.LoadError)
			continue
		}
		fmt.Fprintf(w, "Recipe: %s (profile=%s)\n", r.Recipe, r.Profile)
		fmt.Fprintf(w, "Matched metrics: %d\n", len(r.Matches))
		for _, m := range r.Matches {
			fmt.Fprintf(w, "  - %s (%s)\n", m.Metric, m.Type)
		}
		fmt.Fprintf(w, "Panels: %d\n", len(r.Panels))
		if len(r.Matches) > 0 && len(r.Panels) == 0 {
			fmt.Fprintln(w, "  (matched metrics produced no panels — pair-missing, type-dispatch, or render skip)")
		}
		for _, p := range r.Panels {
			fmt.Fprintf(w, "  [Verdict: %s] %s\n", p.Verdict, p.Title)
			fmt.Fprintf(w, "    query: %s\n", queryForText(p.Query, verbose))
			if p.Refusal != "" {
				fmt.Fprintf(w, "    refusal: %s\n", p.Refusal)
			}
			for _, wn := range p.Warnings {
				fmt.Fprintf(w, "    warning: %s\n", wn)
			}
		}
		if len(r.NonMatches) > 0 {
			fmt.Fprintln(w, "Non-matches (metrics named in predicate but did not satisfy it):")
			for _, nm := range r.NonMatches {
				fmt.Fprintf(w, "  - %s: %s\n", nm.Metric, nm.Reason)
			}
		}
	}

	if loadFailed {
		return ErrTestLoadFailure
	}
	return nil
}

func emitTestJSON(cmd *cobra.Command, results []testRecipeResult, loadFailed bool) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("encode test results: %w", err)
	}
	if loadFailed {
		return ErrTestLoadFailure
	}
	return nil
}

// queryForText returns the query string for non-verbose text rendering.
// Long expressions are collapsed to a single line and clipped so the
// output stays scannable; --verbose returns the original.
func queryForText(q string, verbose bool) string {
	if verbose {
		return q
	}
	collapsed := strings.Join(strings.Fields(q), " ")
	if len(collapsed) > queryPreviewLen {
		return collapsed[:queryPreviewLen] + "…"
	}
	return collapsed
}

// =============================================================================
// Inventory + snapshot helpers
// =============================================================================

// rawToInventory mirrors generate.rawToInventory: a thin adapter from
// discover's RawInventory to inventory.MetricInventory. Re-implemented
// here to avoid importing the generate package (which would pull the
// full pipeline).
func rawToInventory(raw *discover.RawInventory) *inventory.MetricInventory {
	if raw == nil {
		return &inventory.MetricInventory{}
	}
	inv := &inventory.MetricInventory{Metrics: make([]inventory.MetricDescriptor, 0, len(raw.Metrics))}
	for _, m := range raw.Metrics {
		inv.Metrics = append(inv.Metrics, inventory.MetricDescriptor{
			Name:   m.Name,
			Type:   inventory.MetricType(m.Type),
			Help:   m.Help,
			Labels: m.Labels,
		})
	}
	return inv
}

// buildSnapshot mirrors synth.snapshotOf: build the recipe-facing
// snapshot view from a classified inventory. Re-implemented here so
// recipe test stays at the cmd-layer without importing internal/synth.
func buildSnapshot(inv *classify.ClassifiedInventory) recipes.ClassifiedInventorySnapshot {
	if inv == nil {
		return recipes.ClassifiedInventorySnapshot{Inventory: &inventory.MetricInventory{}}
	}
	views := make([]recipes.ClassifiedMetricView, 0, len(inv.Metrics))
	for _, cm := range inv.Metrics {
		traits := make([]string, 0, len(cm.Traits))
		for _, t := range cm.Traits {
			traits = append(traits, string(t))
		}
		unit := cm.Unit
		if unit == "" {
			unit = cm.Descriptor.InferredUnit
		}
		views = append(views, recipes.ClassifiedMetricView{
			Descriptor: cm.Descriptor,
			Type:       cm.Type,
			Family:     cm.Family,
			Unit:       unit,
			Traits:     traits,
		})
	}
	return recipes.ClassifiedInventorySnapshot{
		Inventory: inv.Inventory,
		Metrics:   views,
	}
}
