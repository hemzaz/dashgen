// explain.go — `dashgen recipe explain` (T6B.1).
//
// Walks a recipe's match predicate AST against a single metric in a fixture
// and reports the boolean result of every node so users can answer
// "why didn't my recipe fire on metric X?". Output formats: text (tree)
// and json. See docs/RECIPES-CLI.md §3.7.
//
// adversary: CT5 — RECIPES-CLI.md §9.2: explain output exposes label
// NAMES only — never label values. Enforced structurally: every "actual"
// value rendered here is sourced from MetricDescriptor.Labels (which is
// []string of label names), the predicate spec (authored by the user), or
// the classifier's enumerated traits/types. The fixture's raw series.json
// (which carries label values) is never read by this command.
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
	"dashgen/internal/recipes"
)

// Sentinel errors for exit-code mapping in main.exitCodeFor.
// Per RECIPES-CLI.md §3.7, both recipe-not-found and metric-not-found
// surface as exit code 2.
var (
	// ErrExplainNotFound is returned when --name does not resolve to a
	// registered YAML recipe, or --metric does not exist in the fixture.
	// main.exitCodeFor maps this to exit code 2.
	ErrExplainNotFound = errors.New("recipe explain: not found")
)

type explainArgs struct {
	name          string
	metric        string
	fixtureDir    string
	profile       string
	output        string
	recipesDirs   []string
	noUserRecipes bool
}

func newExplainCmd() *cobra.Command {
	var a explainArgs

	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Walk a recipe's match predicate against one fixture metric and explain the result",
		Long: `Load the named recipe and the named metric from the fixture, then walk the
match predicate AST node-by-node, printing the boolean result of every node.
Useful for diagnosing "why didn't my recipe fire on metric X?".

Output formats: text (tree, default) and json.

Privacy invariant: explain operates on label NAMES only — it never prints
label values. Fixtures whose series.json carries sensitive label values
(real hostnames, customer IDs, etc.) are safe to use here.

Exit codes:
  0  diagnostic produced (regardless of whether the recipe matched)
  2  recipe not found, metric not found, or invalid flag combination`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExplain(cmd, a)
		},
	}

	cmd.Flags().StringVar(&a.name, "name", "",
		"recipe name (required); must be a registered YAML recipe")
	cmd.Flags().StringVar(&a.metric, "metric", "",
		"metric name (required); must exist in the fixture")
	cmd.Flags().StringVar(&a.fixtureDir, "fixture", "",
		"fixture directory (required)")
	cmd.Flags().StringVar(&a.profile, "profile", "",
		"disambiguate cross-profile name collisions: service, infra, or k8s")
	cmd.Flags().StringVar(&a.output, "output", "text",
		"output format: text or json")
	cmd.Flags().StringArrayVar(&a.recipesDirs, "recipes-dir", nil,
		"user recipe directory (repeatable)")
	cmd.Flags().BoolVar(&a.noUserRecipes, "no-user-recipes", false,
		"ignore user directories; builtins only")

	return cmd
}

// =============================================================================
// Output types — used for both text and JSON renderers.
// =============================================================================

// explainCheck records a single primitive predicate-field evaluation.
// Field names ("type", "name_equals", "any_trait", ...) are constants from
// the DSL; Expected is the predicate's authored value; Actual is the
// metric's corresponding NAME-level value (label names, trait names, type
// enum, metric name) — never a label value (CT5).
type explainCheck struct {
	Field    string `json:"field"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Result   bool   `json:"result"`
}

// explainNode is one node in the predicate evaluation tree. Combinators
// (any_of, all_of, not) carry Children; primitives carry Checks. A node
// has either Children or Checks but not both.
type explainNode struct {
	Kind     string         `json:"kind"` // "primitive" | "any_of" | "all_of" | "not"
	Result   bool           `json:"result"`
	Checks   []explainCheck `json:"checks,omitempty"`   // primitive only
	Children []explainNode  `json:"children,omitempty"` // logical only
}

// explainOutput is the top-level shape rendered by both text and json
// emitters. Recipe and Metric are NAMES only.
type explainOutput struct {
	Recipe     string      `json:"recipe"`
	Profile    string      `json:"profile"`
	Metric     string      `json:"metric"`
	MetricType string      `json:"metric_type"`
	Result     bool        `json:"result"`
	Tree       explainNode `json:"tree"`
}

// =============================================================================
// Run
// =============================================================================

func runExplain(cmd *cobra.Command, a explainArgs) error {
	if err := validateExplainArgs(a); err != nil {
		return err
	}

	rec, profile, err := loadExplainRecipe(a)
	if err != nil {
		return err
	}

	view, err := loadExplainMetric(a.fixtureDir, a.metric)
	if err != nil {
		return err
	}

	tree := explainPredicate(rec.Spec.Match, view)
	out := explainOutput{
		Recipe:     rec.Name(),
		Profile:    profile,
		Metric:     view.Descriptor.Name,
		MetricType: string(view.Type),
		Result:     tree.Result,
		Tree:       tree,
	}

	switch a.output {
	case "json":
		return emitExplainJSON(cmd, out)
	default:
		return emitExplainText(cmd, out)
	}
}

func validateExplainArgs(a explainArgs) error {
	if a.name == "" {
		return fmt.Errorf("flag --name: required")
	}
	if a.metric == "" {
		return fmt.Errorf("flag --metric: required")
	}
	if a.fixtureDir == "" {
		return fmt.Errorf("flag --fixture: required")
	}
	switch a.output {
	case "text", "json":
	default:
		return fmt.Errorf("flag --output: %q must be one of [text json]", a.output)
	}
	if a.profile != "" {
		switch a.profile {
		case "service", "infra", "k8s":
		default:
			return fmt.Errorf("flag --profile: %q must be one of [service infra k8s]", a.profile)
		}
	}
	return nil
}

// loadExplainRecipe resolves --name to a registered *YAMLRecipe, applying
// the same load semantics as `recipe show` (built-in + user dirs). Go-only
// recipes have no Spec to walk and are reported as not-supported via
// ErrExplainNotFound.
func loadExplainRecipe(a explainArgs) (*recipes.YAMLRecipe, string, error) {
	pr := recipes.NewProfileRegistries()

	if !a.noUserRecipes {
		userDirs, err := resolveListUserDirs(a.recipesDirs)
		if err != nil {
			return nil, "", fmt.Errorf("resolve user recipe directories: %w", err)
		}
		if len(userDirs) > 0 {
			loaded, err := recipes.Load(context.Background(), recipes.LoaderConfig{
				UserDirs: userDirs,
			})
			if err != nil {
				return nil, "", fmt.Errorf("load user recipes: %w", err)
			}
			if err := pr.RegisterFromLoaded(loaded); err != nil {
				return nil, "", fmt.Errorf("register user recipes: %w", err)
			}
		}
	}

	matches := findRecipeByName(pr, a.name, a.profile, "all")
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("%w: recipe %q", ErrExplainNotFound, a.name)
	}
	if len(matches) > 1 {
		profs := make([]string, 0, len(matches))
		for _, m := range matches {
			profs = append(profs, m.profile)
		}
		sort.Strings(profs)
		return nil, "", fmt.Errorf(
			"%w: recipe %q exists in profiles [%s]; pass --profile to disambiguate",
			ErrExplainNotFound, a.name, strings.Join(profs, " "),
		)
	}

	yr, ok := matches[0].recipe.(*recipes.YAMLRecipe)
	if !ok {
		return nil, "", fmt.Errorf(
			"%w: recipe %q is implemented in Go, not YAML; explain only supports YAML recipes",
			ErrExplainNotFound, a.name,
		)
	}
	return yr, matches[0].profile, nil
}

// loadExplainMetric loads the fixture, classifies it, and returns the
// view for the metric with the given exact name. CT5: only NAME-level
// fields (Descriptor.Name, .Labels, classifier traits/types) are read.
// The raw series.json values are not touched by this code path.
func loadExplainMetric(fixtureDir, metric string) (recipes.ClassifiedMetricView, error) {
	// adversary: CT7 — fixture file-size cap (per-file 16 MB, cumulative 64 MB).
	if err := guardFixtureSize(fixtureDir); err != nil {
		return recipes.ClassifiedMetricView{}, fmt.Errorf("%w: %v", ErrExplainNotFound, err)
	}
	src, err := discover.NewFixtureSource(fixtureDir)
	if err != nil {
		return recipes.ClassifiedMetricView{}, fmt.Errorf("%w: open fixture: %w", ErrExplainNotFound, err)
	}

	raw, err := src.Discover(context.Background(), discover.Selector{})
	if err != nil {
		return recipes.ClassifiedMetricView{}, fmt.Errorf("%w: discover fixture: %w", ErrExplainNotFound, err)
	}
	inv := rawToInventory(raw)
	inv.Sort()
	classified := classify.Classify(inv)
	snap := buildSnapshot(classified)

	for _, view := range snap.Metrics {
		if view.Descriptor.Name == metric {
			return view, nil
		}
	}
	return recipes.ClassifiedMetricView{}, fmt.Errorf(
		"%w: metric %q not found in fixture %q", ErrExplainNotFound, metric, fixtureDir,
	)
}

// =============================================================================
// AST walk
// =============================================================================

// explainPredicate walks the predicate AST and returns the evaluation tree
// rooted at p. The traversal mirrors recipes.Eval exactly: combinators
// short-circuit at the *visit* level, but for diagnostic completeness this
// walker visits *all* children so the user sees every check. The boolean
// Result fields still match recipes.Eval's output bit-for-bit.
func explainPredicate(p recipes.MatchPredicate, m recipes.ClassifiedMetricView) explainNode {
	switch {
	case len(p.AnyOf) > 0:
		node := explainNode{Kind: "any_of"}
		for _, child := range p.AnyOf {
			cn := explainPredicate(child, m)
			node.Children = append(node.Children, cn)
			if cn.Result {
				node.Result = true
			}
		}
		return node
	case len(p.AllOf) > 0:
		node := explainNode{Kind: "all_of", Result: true}
		for _, child := range p.AllOf {
			cn := explainPredicate(child, m)
			node.Children = append(node.Children, cn)
			if !cn.Result {
				node.Result = false
			}
		}
		return node
	case p.Not != nil:
		cn := explainPredicate(*p.Not, m)
		return explainNode{
			Kind:     "not",
			Result:   !cn.Result,
			Children: []explainNode{cn},
		}
	default:
		return explainPrimitive(p, m)
	}
}

// explainPrimitive evaluates each populated leaf field in p against m and
// records one explainCheck per field. The primitive's Result is the AND of
// all check results (matching recipes.evalPrimitive). An empty primitive
// (zero populated fields) trivially matches everything → Result=true.
func explainPrimitive(p recipes.MatchPredicate, m recipes.ClassifiedMetricView) explainNode {
	node := explainNode{Kind: "primitive", Result: true}
	name := m.Descriptor.Name
	add := func(c explainCheck) {
		node.Checks = append(node.Checks, c)
		if !c.Result {
			node.Result = false
		}
	}

	// ── Type ─────────────────────────────────────────────────────────────────
	if p.Type != "" {
		actual := string(m.Type)
		add(explainCheck{
			Field: "type", Expected: p.Type, Actual: actual,
			Result: actual == p.Type,
		})
	}

	// ── Name predicates ──────────────────────────────────────────────────────
	if p.NameEquals != "" {
		add(explainCheck{
			Field: "name_equals", Expected: p.NameEquals, Actual: name,
			Result: name == p.NameEquals,
		})
	}
	if len(p.NameEqualsAny) > 0 {
		ok := false
		for _, n := range p.NameEqualsAny {
			if name == n {
				ok = true
				break
			}
		}
		add(explainCheck{
			Field: "name_equals_any", Expected: p.NameEqualsAny, Actual: name, Result: ok,
		})
	}
	if p.NameHasPrefix != "" {
		add(explainCheck{
			Field: "name_has_prefix", Expected: p.NameHasPrefix, Actual: name,
			Result: strings.HasPrefix(name, p.NameHasPrefix),
		})
	}
	if p.NameHasSuffix != "" {
		add(explainCheck{
			Field: "name_has_suffix", Expected: p.NameHasSuffix, Actual: name,
			Result: strings.HasSuffix(name, p.NameHasSuffix),
		})
	}
	if p.NameContains != "" {
		add(explainCheck{
			Field: "name_contains", Expected: p.NameContains, Actual: name,
			Result: strings.Contains(name, p.NameContains),
		})
	}
	if len(p.NameContainsAny) > 0 {
		ok := false
		for _, sub := range p.NameContainsAny {
			if strings.Contains(name, sub) {
				ok = true
				break
			}
		}
		add(explainCheck{
			Field: "name_contains_any", Expected: p.NameContainsAny, Actual: name, Result: ok,
		})
	}
	if p.NameMatches != "" {
		// Use Eval's path indirectly: evalPrimitive does the regex compile
		// + match on the cached regex. We re-use the public matcher just
		// for this single-field check by constructing a minimal predicate.
		ok := recipes.Eval(recipes.MatchPredicate{NameMatches: p.NameMatches}, m)
		add(explainCheck{
			Field: "name_matches", Expected: p.NameMatches, Actual: name, Result: ok,
		})
	}

	// ── Trait predicates (NAME-level: trait identifiers, not values) ─────────
	if len(p.AnyTrait) > 0 {
		ok := false
		for _, t := range p.AnyTrait {
			if m.HasTrait(t) {
				ok = true
				break
			}
		}
		add(explainCheck{
			Field: "any_trait", Expected: p.AnyTrait, Actual: m.Traits, Result: ok,
		})
	}
	if len(p.AllTraits) > 0 {
		ok := true
		for _, t := range p.AllTraits {
			if !m.HasTrait(t) {
				ok = false
				break
			}
		}
		add(explainCheck{
			Field: "all_traits", Expected: p.AllTraits, Actual: m.Traits, Result: ok,
		})
	}
	if len(p.NoneTrait) > 0 {
		ok := true
		for _, t := range p.NoneTrait {
			if m.HasTrait(t) {
				ok = false
				break
			}
		}
		add(explainCheck{
			Field: "none_trait", Expected: p.NoneTrait, Actual: m.Traits, Result: ok,
		})
	}

	// ── Label predicates (label NAMES only — invariant I2 / CT5) ─────────────
	if p.HasLabel != "" {
		add(explainCheck{
			Field: "has_label", Expected: p.HasLabel, Actual: m.Descriptor.Labels,
			Result: m.HasLabel(p.HasLabel),
		})
	}
	if len(p.HasLabelAny) > 0 {
		ok := false
		for _, l := range p.HasLabelAny {
			if m.HasLabel(l) {
				ok = true
				break
			}
		}
		add(explainCheck{
			Field: "has_label_any", Expected: p.HasLabelAny, Actual: m.Descriptor.Labels, Result: ok,
		})
	}
	if len(p.HasLabelAll) > 0 {
		ok := true
		for _, l := range p.HasLabelAll {
			if !m.HasLabel(l) {
				ok = false
				break
			}
		}
		add(explainCheck{
			Field: "has_label_all", Expected: p.HasLabelAll, Actual: m.Descriptor.Labels, Result: ok,
		})
	}
	if len(p.HasLabelNone) > 0 {
		ok := true
		for _, l := range p.HasLabelNone {
			if m.HasLabel(l) {
				ok = false
				break
			}
		}
		add(explainCheck{
			Field: "has_label_none", Expected: p.HasLabelNone, Actual: m.Descriptor.Labels, Result: ok,
		})
	}

	return node
}

// =============================================================================
// Renderers
// =============================================================================

func emitExplainJSON(cmd *cobra.Command, out explainOutput) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

func emitExplainText(cmd *cobra.Command, out explainOutput) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Recipe: %s (profile=%s)\n", out.Recipe, out.Profile)
	fmt.Fprintf(w, "Metric: %s (%s)\n\n", out.Metric, out.MetricType)
	fmt.Fprintf(w, "match%s → %s\n", kindSuffix(out.Tree.Kind), boolMark(out.Tree.Result))
	renderExplainBody(w, "", out.Tree)
	fmt.Fprintf(w, "\nRESULT: %t\n", out.Result)
	return nil
}

// renderExplainBody emits the children of node n at the given prefix. For a
// primitive node it lists Checks; for a logical node it recurses into
// Children. Each line uses the standard tree-connector glyphs (├── / └──).
func renderExplainBody(w interface{ Write([]byte) (int, error) }, prefix string, n explainNode) {
	if n.Kind == "primitive" {
		for i, c := range n.Checks {
			isLast := i == len(n.Checks)-1
			conn, _ := treeConnector(prefix, isLast)
			fmt.Fprintf(w, "%s%s\n", conn, formatCheck(c))
		}
		return
	}
	for i, child := range n.Children {
		isLast := i == len(n.Children)-1
		conn, nextPrefix := treeConnector(prefix, isLast)
		fmt.Fprintf(w, "%s%s%s → %s\n", conn, childLabel(n.Kind, i), kindSuffix(child.Kind), boolMark(child.Result))
		renderExplainBody(w, nextPrefix, child)
	}
}

// childLabel disambiguates each child of a logical node so the tree reads
// naturally in text. any_of/all_of children get an ordinal so the user can
// tell siblings apart; not has exactly one child.
func childLabel(parentKind string, idx int) string {
	switch parentKind {
	case "any_of", "all_of":
		return fmt.Sprintf("[%d] ", idx)
	case "not":
		return "child "
	}
	return ""
}

// kindSuffix returns the parenthetical kind annotation for a non-primitive
// node, or empty string for primitives. Keeps the tree readable.
func kindSuffix(kind string) string {
	if kind == "primitive" {
		return ""
	}
	return fmt.Sprintf(" (%s)", kind)
}

// boolMark renders ✓/✗ for true/false. Ascii-friendly variant chosen so
// the test harness's string assertions remain stable across terminals.
func boolMark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

// formatCheck renders one primitive-leaf evaluation as a single line.
// Layout: "<field>: <expected> → ✓/✗ (got <actual>)". The "actual" hint
// is omitted when it would duplicate the expected value (e.g. when the
// check passed and expected/actual are byte-equal scalars).
func formatCheck(c explainCheck) string {
	exp := formatValue(c.Expected)
	act := formatValue(c.Actual)
	mark := boolMark(c.Result)

	// Hide the trailing "got X" when expected and actual already match
	// textually — keeps lines short for the common pass case.
	if exp == act {
		return fmt.Sprintf("%s: %s → %s", c.Field, exp, mark)
	}
	return fmt.Sprintf("%s: %s → %s (got %s)", c.Field, exp, mark, act)
}

// formatValue renders a scalar or string-list "expected/actual" payload
// for text output. Only string and []string variants occur here (the
// predicate fields are typed accordingly), so the switch is exhaustive
// for our domain. Empty []string renders as "[]".
func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []string:
		if len(x) == 0 {
			return "[]"
		}
		return "[" + strings.Join(x, ", ") + "]"
	default:
		return fmt.Sprintf("%v", v)
	}
}
