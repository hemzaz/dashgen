// diff.go — `dashgen recipe diff` (T6B.2).
//
// Compares two recipes (or fileA vs the built-in with the same name when
// --against-builtin is set) by their effect on a fixture: load both, run
// each through BuildPanels against the same classified snapshot, then
// surface panel-level adds/removes/changes per RECIPES-CLI.md §3.8.
//
// Identity model (within the panel diff):
//   - Panels are bucketed by Title within each side.
//   - Same-title panels at the same intra-bucket index are compared
//     pairwise; surplus on either side becomes added/removed.
//   - Per-pair diff covers the user-visible structural fields the spec
//     calls out: query (Queries[0].Expr), legend (Queries[0].LegendFormat),
//     unit, kind. group_by changes manifest as query changes (the rendered
//     `sum by (...)` clause embeds the resolved labels), so they surface
//     under the query field rather than as a synthetic separate diff line.
//
// Exit codes (mapped in main.exitCodeFor):
//   0  no differences
//   1  differences exist (CI gate); also recipe load failed
//   2  fixture missing/malformed, or --against-builtin recipe not found
package recipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"dashgen/internal/classify"
	"dashgen/internal/discover"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
	"dashgen/internal/recipes"
)

// Sentinel errors for exit-code mapping in main.exitCodeFor.
// Per RECIPES-CLI.md §3.8.
var (
	// ErrDiffNotFound: --against-builtin lookup failed (no built-in with
	// this name) or resolved to multiple profiles without --profile.
	// main.exitCodeFor maps this to exit code 2.
	ErrDiffNotFound = errors.New("recipe diff: recipe not found")

	// ErrDiffLoadFailure: a recipe file could not be loaded (file missing,
	// schema invalid, template parse failed).
	// main.exitCodeFor maps this to exit code 1.
	ErrDiffLoadFailure = errors.New("recipe diff: recipe load failed")

	// ErrDiffFixtureError: --fixture is missing or malformed.
	// main.exitCodeFor maps this to exit code 2.
	ErrDiffFixtureError = errors.New("recipe diff: fixture error")

	// ErrDiffMismatch: the diff is non-empty. Returned after rendering so
	// the command can act as a CI gate. Maps to exit code 1.
	ErrDiffMismatch = errors.New("recipe diff: panels differ")
)

type diffArgs struct {
	fixtureDir     string
	againstBuiltin bool
	output         string
	profile        string
}

func newDiffCmd() *cobra.Command {
	var a diffArgs

	cmd := &cobra.Command{
		Use:   "diff <fileA> [<fileB>]",
		Short: "Compare two recipes (or fileA vs the built-in with same name) by their effect on a fixture",
		Long: `Load two recipe YAML files (or fileA against the built-in with the same name
when --against-builtin is set), classify the fixture, run both recipes
through BuildPanels, and print panel-level differences.

Output formats: text (default), json, unified.

Exit codes:
  0  no differences
  1  differences exist (CI gate); also recipe load failed
  2  fixture missing/malformed, or --against-builtin recipe not found`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, a, args)
		},
	}

	cmd.Flags().StringVar(&a.fixtureDir, "fixture", "",
		"fixture directory (required)")
	cmd.Flags().BoolVar(&a.againstBuiltin, "against-builtin", false,
		"compare <fileA> against the built-in recipe with the same name (omit <fileB>)")
	cmd.Flags().StringVar(&a.output, "output", "text",
		"output format: text, json, or unified")
	cmd.Flags().StringVar(&a.profile, "profile", "",
		"disambiguate cross-profile collisions for --against-builtin")

	return cmd
}

// =============================================================================
// Result types — used for both text and JSON renderers.
// =============================================================================

// diffSideMeta is the per-side header rendered above the diff body.
type diffSideMeta struct {
	Recipe  string `json:"recipe"`
	File    string `json:"file"`
	Profile string `json:"profile"`
}

// diffPanelView is the structural snapshot we keep for added / removed
// panels. Captures the same fields panelChange compares so the renderers
// have a single source of truth for "what's a panel".
type diffPanelView struct {
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Query  string `json:"query"`
	Legend string `json:"legend"`
	Unit   string `json:"unit"`
}

// diffPanelChange is one paired-panel difference: the title (which is the
// pair identity) plus the per-field diffs. An empty Fields slice means
// the pair is byte-identical and is dropped before render.
type diffPanelChange struct {
	Title  string                `json:"title"`
	Fields []diffPanelFieldDelta `json:"fields"`
}

// diffPanelFieldDelta captures one field's old/new values. Field is one
// of "query", "legend", "unit", "kind".
type diffPanelFieldDelta struct {
	Field string `json:"field"`
	A     string `json:"a"`
	B     string `json:"b"`
}

// diffResult is the complete cross-side comparison rendered by all output
// formats. Keep counts implicit (len of the slices) — RECIPES-CLI.md §3.8
// shows them in the text summary, but JSON consumers can compute them.
type diffResult struct {
	A        diffSideMeta      `json:"a"`
	B        diffSideMeta      `json:"b"`
	AddedB   []diffPanelView   `json:"added_in_b"`
	RemovedB []diffPanelView   `json:"removed_in_b"`
	Changed  []diffPanelChange `json:"changed"`
}

// totalChanges returns the number of cross-side differences. Zero means
// the diff is empty and the command should exit 0; non-zero triggers
// ErrDiffMismatch and exit 1.
func (r diffResult) totalChanges() int {
	return len(r.AddedB) + len(r.RemovedB) + len(r.Changed)
}

// diffSide captures a single recipe's BuildPanels output plus the
// metadata that goes into the rendered header.
type diffSide struct {
	Recipe  string
	File    string
	Profile string
	Panels  []ir.Panel
}

// =============================================================================
// Run
// =============================================================================

func runDiff(cmd *cobra.Command, a diffArgs, args []string) error {
	if err := validateDiffArgs(a, args); err != nil {
		return err
	}

	fileA := args[0]
	recA, err := loadDiffRecipeFile(fileA)
	if err != nil {
		return err
	}

	var (
		recB     *recipes.YAMLRecipe
		labelB   string
		profileB string
	)
	if a.againstBuiltin {
		// Default to fileA's declared profile when --profile is empty.
		// This matches "compare <fileA> against the built-in with the
		// same name" — the built-in lives in the same profile as the
		// override unless the user overrides via --profile.
		profileFilter := a.profile
		if profileFilter == "" {
			profileFilter = recA.Spec.Metadata.Profile
		}
		recB, profileB, err = loadDiffBuiltinByName(recA.Name(), profileFilter)
		if err != nil {
			return err
		}
		labelB = "<built-in>"
	} else {
		fileB := args[1]
		recB, err = loadDiffRecipeFile(fileB)
		if err != nil {
			return err
		}
		labelB = fileB
		profileB = recB.Spec.Metadata.Profile
	}

	snapshot, err := loadDiffFixture(a.fixtureDir)
	if err != nil {
		return err
	}

	sideA := diffSide{
		Recipe:  recA.Name(),
		File:    fileA,
		Profile: recA.Spec.Metadata.Profile,
		Panels:  recA.BuildPanels(snapshot, profiles.Profile(recA.Spec.Metadata.Profile)),
	}
	sideB := diffSide{
		Recipe:  recB.Name(),
		File:    labelB,
		Profile: profileB,
		Panels:  recB.BuildPanels(snapshot, profiles.Profile(profileB)),
	}
	result := computeDiff(sideA, sideB)

	switch a.output {
	case "json":
		if err := emitDiffJSON(cmd, result); err != nil {
			return err
		}
	case "unified":
		if err := emitDiffUnified(cmd, result); err != nil {
			return err
		}
	default:
		if err := emitDiffText(cmd, result); err != nil {
			return err
		}
	}

	if result.totalChanges() > 0 {
		return ErrDiffMismatch
	}
	return nil
}

// validateDiffArgs checks flag values and positional-arg shape before any
// I/O. The two arg-count modes are:
//   - 1 arg + --against-builtin
//   - 2 args (no --against-builtin)
func validateDiffArgs(a diffArgs, args []string) error {
	if a.fixtureDir == "" {
		return fmt.Errorf("flag --fixture: required")
	}
	switch a.output {
	case "text", "json", "unified":
	default:
		return fmt.Errorf("flag --output: %q must be one of [text json unified]", a.output)
	}
	if a.profile != "" {
		switch a.profile {
		case "service", "infra", "k8s":
		default:
			return fmt.Errorf("flag --profile: %q must be one of [service infra k8s]", a.profile)
		}
	}
	if a.againstBuiltin {
		if len(args) != 1 {
			return fmt.Errorf("--against-builtin takes exactly one positional arg (got %d)", len(args))
		}
	} else {
		if len(args) != 2 {
			return fmt.Errorf("recipe diff requires two positional args (or one + --against-builtin); got %d", len(args))
		}
	}
	return nil
}

// loadDiffRecipeFile loads a single user-supplied YAML file from disk and
// wraps it as a *YAMLRecipe. Failure surfaces as ErrDiffLoadFailure (exit 1).
//
// adversary: CT6 — diff with malicious user override. All loader limits
// from RECIPES-DSL-ADVERSARY.md (file size, CUE deadline, template AST
// budget, predicate depth/node caps) apply transparently here; a
// pathological override surfaces as ErrDiffLoadFailure rather than hanging.
func loadDiffRecipeFile(path string) (*recipes.YAMLRecipe, error) {
	loaded, err := recipes.LoadFile(context.Background(), recipes.LoaderConfig{}, path, recipes.SourceUser)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDiffLoadFailure, err)
	}
	rec, err := recipes.NewYAMLRecipe(loaded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDiffLoadFailure, err)
	}
	return rec, nil
}

// loadDiffBuiltinByName resolves <name> to the built-in YAML recipe with
// that name. Uses a fresh ProfileRegistries so user-recipe overrides on
// disk never shadow the built-in we want to compare against. Returns the
// recipe and the profile it lives in. Failure surfaces as
// ErrDiffNotFound (exit 2).
func loadDiffBuiltinByName(name, profileFilter string) (*recipes.YAMLRecipe, string, error) {
	pr := recipes.NewProfileRegistries()
	matches := findRecipeByName(pr, name, profileFilter, "builtin")
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("%w: built-in %q", ErrDiffNotFound, name)
	}
	if len(matches) > 1 {
		profs := make([]string, 0, len(matches))
		for _, m := range matches {
			profs = append(profs, m.profile)
		}
		sort.Strings(profs)
		return nil, "", fmt.Errorf(
			"%w: %q exists in profiles [%s]; pass --profile to disambiguate",
			ErrDiffNotFound, name, strings.Join(profs, " "),
		)
	}
	yr, ok := matches[0].recipe.(*recipes.YAMLRecipe)
	if !ok {
		return nil, "", fmt.Errorf(
			"%w: built-in %q is implemented in Go, not YAML; diff only supports YAML recipes",
			ErrDiffNotFound, name,
		)
	}
	return yr, matches[0].profile, nil
}

// loadDiffFixture mirrors test.go's fixture-load path: open, discover,
// classify, snapshot. Errors (path missing, malformed JSON) surface as
// ErrDiffFixtureError (exit 2).
func loadDiffFixture(dir string) (recipes.ClassifiedInventorySnapshot, error) {
	// adversary: CT7 — fixture file-size cap (per-file 16 MB, cumulative 64 MB).
	if err := guardFixtureSize(dir); err != nil {
		return recipes.ClassifiedInventorySnapshot{}, fmt.Errorf("%w: %v", ErrDiffFixtureError, err)
	}
	src, err := discover.NewFixtureSource(dir)
	if err != nil {
		return recipes.ClassifiedInventorySnapshot{}, fmt.Errorf("%w: %v", ErrDiffFixtureError, err)
	}
	raw, err := src.Discover(context.Background(), discover.Selector{})
	if err != nil {
		return recipes.ClassifiedInventorySnapshot{}, fmt.Errorf("%w: discover: %v", ErrDiffFixtureError, err)
	}
	inv := rawToInventory(raw)
	inv.Sort()
	classified := classify.Classify(inv)
	return buildSnapshot(classified), nil
}

// =============================================================================
// Diff algorithm
// =============================================================================

// computeDiff buckets each side's panels by Title, then walks the union
// of titles in sorted order to produce a deterministic added/removed/
// changed result. Within a title bucket, panels are compared pairwise
// by intra-bucket index; surplus on either side falls into the
// added/removed lists.
func computeDiff(a, b diffSide) diffResult {
	res := diffResult{
		A:        diffSideMeta{Recipe: a.Recipe, File: a.File, Profile: a.Profile},
		B:        diffSideMeta{Recipe: b.Recipe, File: b.File, Profile: b.Profile},
		AddedB:   []diffPanelView{},
		RemovedB: []diffPanelView{},
		Changed:  []diffPanelChange{},
	}
	bucketsA := bucketByTitle(a.Panels)
	bucketsB := bucketByTitle(b.Panels)

	for _, title := range sortedTitleUnion(bucketsA, bucketsB) {
		ap := bucketsA[title]
		bp := bucketsB[title]
		n := len(ap)
		if len(bp) > n {
			n = len(bp)
		}
		for i := 0; i < n; i++ {
			switch {
			case i >= len(ap):
				res.AddedB = append(res.AddedB, panelToView(bp[i]))
			case i >= len(bp):
				res.RemovedB = append(res.RemovedB, panelToView(ap[i]))
			default:
				if change, ok := comparePanel(ap[i], bp[i]); ok {
					res.Changed = append(res.Changed, change)
				}
			}
		}
	}
	return res
}

// bucketByTitle groups panels by their Title in input order. Two panels
// in the same recipe with identical titles are kept in slice order so
// comparePanel can pair them deterministically.
func bucketByTitle(panels []ir.Panel) map[string][]ir.Panel {
	m := make(map[string][]ir.Panel, len(panels))
	for _, p := range panels {
		m[p.Title] = append(m[p.Title], p)
	}
	return m
}

// sortedTitleUnion returns the deduplicated union of map keys in sorted
// order so the diff output is byte-deterministic across runs.
func sortedTitleUnion(a, b map[string][]ir.Panel) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// comparePanel walks the user-visible structural fields and records every
// difference. Title is excluded (it is the pair identity). UID/Verdict/
// Confidence/Warnings are derived/transient and excluded as well — the
// spec calls out query/legend/unit/group_by/title; group_by is embedded
// in the rendered query.
func comparePanel(a, b ir.Panel) (diffPanelChange, bool) {
	var fields []diffPanelFieldDelta
	qa, qb := firstQuery(a), firstQuery(b)
	if qa.Expr != qb.Expr {
		fields = append(fields, diffPanelFieldDelta{Field: "query", A: qa.Expr, B: qb.Expr})
	}
	if qa.LegendFormat != qb.LegendFormat {
		fields = append(fields, diffPanelFieldDelta{Field: "legend", A: qa.LegendFormat, B: qb.LegendFormat})
	}
	if a.Unit != b.Unit {
		fields = append(fields, diffPanelFieldDelta{Field: "unit", A: a.Unit, B: b.Unit})
	}
	if a.Kind != b.Kind {
		fields = append(fields, diffPanelFieldDelta{Field: "kind", A: string(a.Kind), B: string(b.Kind)})
	}
	if len(fields) == 0 {
		return diffPanelChange{}, false
	}
	return diffPanelChange{Title: a.Title, Fields: fields}, true
}

// firstQuery returns the panel's primary query candidate, or a zero
// QueryCandidate if the panel has no queries (which would itself be a
// recipe authoring error caught upstream).
func firstQuery(p ir.Panel) ir.QueryCandidate {
	if len(p.Queries) == 0 {
		return ir.QueryCandidate{}
	}
	return p.Queries[0]
}

// panelToView projects an ir.Panel into the structural snapshot used by
// added/removed lists. Mirrors the fields comparePanel diffs against so
// the JSON output is symmetric between added/removed and changed.
func panelToView(p ir.Panel) diffPanelView {
	q := firstQuery(p)
	return diffPanelView{
		Title:  p.Title,
		Kind:   string(p.Kind),
		Query:  q.Expr,
		Legend: q.LegendFormat,
		Unit:   p.Unit,
	}
}

// =============================================================================
// Renderers
// =============================================================================

// emitDiffJSON writes the canonical JSON encoding of the diff result.
// The schema is the diffResult struct verbatim; consumers can len() the
// added_in_b / removed_in_b / changed slices for counts.
func emitDiffJSON(cmd *cobra.Command, r diffResult) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("encode diff json: %w", err)
	}
	return nil
}

// emitDiffText writes the human-readable diff per RECIPES-CLI.md §3.8.
// The format chooses brevity over pixel-faithful copy of the spec sample:
// the same sections (header, per-bucket changed/added/removed, summary
// counts) appear in the same order.
func emitDiffText(cmd *cobra.Command, r diffResult) error {
	w := cmd.OutOrStdout()

	header := r.A.Recipe
	if r.B.Recipe != "" && r.B.Recipe != r.A.Recipe {
		header = r.A.Recipe + " vs " + r.B.Recipe
	}
	fmt.Fprintf(w, "Recipe: %s\n", header)
	fmt.Fprintf(w, "A: %s (profile=%s)\n", r.A.File, r.A.Profile)
	fmt.Fprintf(w, "B: %s (profile=%s)\n", r.B.File, r.B.Profile)
	fmt.Fprintln(w)

	if len(r.Changed) > 0 {
		fmt.Fprintln(w, "Panels changed:")
		for _, ch := range r.Changed {
			fmt.Fprintf(w, "  [%s]\n", ch.Title)
			for _, fd := range ch.Fields {
				fmt.Fprintf(w, "    %s:\n", fd.Field)
				fmt.Fprintf(w, "      - %s\n", fd.A)
				fmt.Fprintf(w, "      + %s\n", fd.B)
			}
		}
		fmt.Fprintln(w)
	}
	if len(r.RemovedB) > 0 {
		fmt.Fprintln(w, "Panels removed (in A, not in B):")
		for _, p := range r.RemovedB {
			renderDiffPanelView(w, p)
		}
		fmt.Fprintln(w)
	}
	if len(r.AddedB) > 0 {
		fmt.Fprintln(w, "Panels added (in B, not in A):")
		for _, p := range r.AddedB {
			renderDiffPanelView(w, p)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "Panels added:    %d\n", len(r.AddedB))
	fmt.Fprintf(w, "Panels removed:  %d\n", len(r.RemovedB))
	fmt.Fprintf(w, "Panels changed:  %d\n", len(r.Changed))
	return nil
}

// renderDiffPanelView writes one added/removed panel as a small block.
// Kept separate so the added and removed sections share formatting.
func renderDiffPanelView(w interface{ Write([]byte) (int, error) }, p diffPanelView) {
	fmt.Fprintf(w, "  [%s] kind=%s unit=%s\n", p.Title, p.Kind, p.Unit)
	fmt.Fprintf(w, "    query:  %s\n", p.Query)
	fmt.Fprintf(w, "    legend: %s\n", p.Legend)
}

// emitDiffUnified writes a unified-diff-shaped output suitable for
// piping into colordiff / diff-so-fancy. Hunks are keyed by panel title;
// added panels show only `+` lines, removed show only `-`, changed show
// both. Per RECIPES-CLI.md §5.4, the format starts with a `--- / +++`
// header pair.
func emitDiffUnified(cmd *cobra.Command, r diffResult) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "--- %s\n", r.A.File)
	fmt.Fprintf(w, "+++ %s\n", r.B.File)
	for _, ch := range r.Changed {
		fmt.Fprintf(w, "@@ %s @@\n", ch.Title)
		for _, fd := range ch.Fields {
			fmt.Fprintf(w, "-%s: %s\n", fd.Field, fd.A)
			fmt.Fprintf(w, "+%s: %s\n", fd.Field, fd.B)
		}
	}
	for _, p := range r.RemovedB {
		fmt.Fprintf(w, "@@ %s (removed) @@\n", p.Title)
		fmt.Fprintf(w, "-kind: %s\n", p.Kind)
		fmt.Fprintf(w, "-unit: %s\n", p.Unit)
		fmt.Fprintf(w, "-query: %s\n", p.Query)
		fmt.Fprintf(w, "-legend: %s\n", p.Legend)
	}
	for _, p := range r.AddedB {
		fmt.Fprintf(w, "@@ %s (added) @@\n", p.Title)
		fmt.Fprintf(w, "+kind: %s\n", p.Kind)
		fmt.Fprintf(w, "+unit: %s\n", p.Unit)
		fmt.Fprintf(w, "+query: %s\n", p.Query)
		fmt.Fprintf(w, "+legend: %s\n", p.Legend)
	}
	return nil
}
