package recipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"dashgen/internal/recipes"
)

// Sentinel errors for exit-code mapping in main.exitCodeFor.
// Each maps to a distinct exit code per RECIPES-CLI.md §3.5.
var (
	// ErrShowNotFound is returned when no registered recipe matches the
	// requested name (after applying --profile and --source filters).
	// main.exitCodeFor maps this to exit code 1.
	ErrShowNotFound = errors.New("recipe show: recipe not found")

	// ErrShowAmbiguous is returned when the requested name resolves to
	// recipes in more than one profile and --profile was not set.
	// main.exitCodeFor maps this to exit code 2.
	ErrShowAmbiguous = errors.New("recipe show: ambiguous recipe name across profiles")
)

type showArgs struct {
	profile       string
	source        string
	output        string
	recipesDirs   []string
	noUserRecipes bool
}

// recipeMatch carries a single (profile, recipe) pairing returned by the
// per-profile name lookup. Used to detect cross-profile collisions.
type recipeMatch struct {
	profile string
	recipe  recipes.Recipe
}

func newShowCmd() *cobra.Command {
	var a showArgs

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Print the resolved YAML for a registered recipe",
		Long: `Print the resolved (post-defaults, post-unification) recipe by name.

If the same name exists in multiple profiles, --profile must be set.
With --source builtin, an existing user override is ignored so the original
built-in recipe is shown.

Output formats: yaml (default), json, tree.

Exit codes:
  0  recipe found and printed
  1  recipe not found
  2  ambiguous name across profiles (pass --profile to disambiguate)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShow(cmd, args[0], a)
		},
	}

	cmd.Flags().StringVar(&a.profile, "profile", "",
		"disambiguate cross-profile name collisions: service, infra, or k8s")
	cmd.Flags().StringVar(&a.output, "output", "yaml",
		"output format: yaml, json, or tree")
	cmd.Flags().StringVar(&a.source, "source", "all",
		"source filter: builtin, user, or all (default all; user wins on collision)")
	cmd.Flags().StringArrayVar(&a.recipesDirs, "recipes-dir", nil,
		"user recipe directory (repeatable)")
	cmd.Flags().BoolVar(&a.noUserRecipes, "no-user-recipes", false,
		"ignore user directories; builtins only")

	return cmd
}

func runShow(cmd *cobra.Command, name string, a showArgs) error {
	if err := validateShowArgs(a); err != nil {
		return err
	}

	pr := recipes.NewProfileRegistries()

	// --source builtin must show the built-in recipe even when a user
	// override exists; the registry's Replace semantics would shadow the
	// built-in, so skip user-recipe loading entirely in that case.
	if a.source != "builtin" && !a.noUserRecipes {
		userDirs, err := resolveListUserDirs(a.recipesDirs)
		if err != nil {
			return fmt.Errorf("resolve user recipe directories: %w", err)
		}
		if len(userDirs) > 0 {
			loaded, err := recipes.Load(context.Background(), recipes.LoaderConfig{
				UserDirs: userDirs,
			})
			if err != nil {
				return fmt.Errorf("load user recipes: %w", err)
			}
			if err := pr.RegisterFromLoaded(loaded); err != nil {
				return fmt.Errorf("register user recipes: %w", err)
			}
		}
	}

	matches := findRecipeByName(pr, name, a.profile, a.source)

	if len(matches) == 0 {
		return fmt.Errorf("%w: %q", ErrShowNotFound, name)
	}
	if len(matches) > 1 {
		profs := make([]string, 0, len(matches))
		for _, m := range matches {
			profs = append(profs, m.profile)
		}
		sort.Strings(profs)
		return fmt.Errorf("%w: %q exists in profiles [%s]; pass --profile to disambiguate",
			ErrShowAmbiguous, name, strings.Join(profs, " "))
	}

	return emitShow(cmd, matches[0], a.output)
}

// validateShowArgs checks flag values before any I/O.
func validateShowArgs(a showArgs) error {
	switch a.source {
	case "builtin", "user", "all":
	default:
		return fmt.Errorf("flag --source: %q must be one of [builtin user all]", a.source)
	}
	if a.profile != "" {
		switch a.profile {
		case "service", "infra", "k8s":
		default:
			return fmt.Errorf("flag --profile: %q must be one of [service infra k8s]", a.profile)
		}
	}
	switch a.output {
	case "yaml", "json", "tree":
	default:
		return fmt.Errorf("flag --output: %q must be one of [yaml json tree]", a.output)
	}
	return nil
}

// findRecipeByName scans every profile registry for a recipe with the
// given name. Profile and source filters narrow the result. The return
// is a slice because cross-profile name collisions are possible (and
// must trigger ErrShowAmbiguous).
func findRecipeByName(pr *recipes.ProfileRegistries, name, profileFilter, sourceFilter string) []recipeMatch {
	profs := []struct {
		name string
		reg  *recipes.Registry
	}{
		{"service", pr.Service},
		{"infra", pr.Infra},
		{"k8s", pr.K8s},
	}

	var out []recipeMatch
	for _, p := range profs {
		if profileFilter != "" && p.name != profileFilter {
			continue
		}
		rec := p.reg.ByName(name)
		if rec == nil {
			continue
		}
		if sourceFilter != "all" && recipeSourceOf(rec) != sourceFilter {
			continue
		}
		out = append(out, recipeMatch{profile: p.name, recipe: rec})
	}
	return out
}

// recipeSourceOf returns the canonical source label for a Recipe.
// YAMLRecipes carry their source explicitly; Go-implemented recipes are
// always built-in.
func recipeSourceOf(rec recipes.Recipe) string {
	if yr, ok := rec.(*recipes.YAMLRecipe); ok {
		return yr.Source
	}
	return recipes.SourceBuiltin
}

// emitShow renders a single match in the requested output format.
// Go-implemented (non-YAML) recipes have no spec to render and are
// reported as not-supported (exit 1) so the CLI never invents YAML.
func emitShow(cmd *cobra.Command, m recipeMatch, output string) error {
	yr, ok := m.recipe.(*recipes.YAMLRecipe)
	if !ok {
		return fmt.Errorf(
			"%w: %q is implemented in Go, not YAML; show only supports YAML recipes",
			ErrShowNotFound, m.recipe.Name(),
		)
	}
	switch output {
	case "yaml":
		return emitShowYAML(cmd, yr.Spec)
	case "json":
		return emitShowJSON(cmd, yr.Spec)
	case "tree":
		return emitShowTree(cmd, yr.Spec)
	}
	// Unreachable: validateShowArgs has already constrained output.
	return fmt.Errorf("internal: unsupported output format %q", output)
}

// emitShowJSON writes the spec as indented JSON, the canonical encoding.
func emitShowJSON(cmd *cobra.Command, spec recipes.RecipeSpec) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(spec); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

// emitShowYAML writes the spec as YAML. We round-trip through JSON first
// so the YAML encoder uses the json struct tags (RecipeSpec carries
// json tags, not yaml tags). yaml.v3 marshals map keys in sorted order,
// which keeps the output deterministic across runs.
func emitShowYAML(cmd *cobra.Command, spec recipes.RecipeSpec) error {
	obj, err := jsonRoundTrip(spec)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent(2)
	if err := enc.Encode(obj); err != nil {
		return fmt.Errorf("encode yaml: %w", err)
	}
	return enc.Close()
}

// emitShowTree writes the spec as an ASCII tree, similar to tree(1).
// Useful for visual nesting inspection (RECIPES-CLI.md §5.5).
func emitShowTree(cmd *cobra.Command, spec recipes.RecipeSpec) error {
	obj, err := jsonRoundTrip(spec)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	fmt.Fprintln(w, spec.Metadata.Name)
	walkTree(w, "", obj)
	return nil
}

// jsonRoundTrip serializes spec via json.Marshal and reparses it via
// yaml.Unmarshal so the result uses the json field names (lowercase
// camelCase from RecipeSpec's struct tags). Maps come back ordered by
// yaml.v3's stable key sort during emission.
func jsonRoundTrip(spec recipes.RecipeSpec) (any, error) {
	b, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	var obj any
	if err := yaml.Unmarshal(b, &obj); err != nil {
		return nil, fmt.Errorf("unmarshal for yaml render: %w", err)
	}
	return obj, nil
}

// walkTree recursively prints v as an indented tree under prefix. Map
// keys are emitted in sorted order so the output is byte-deterministic
// across runs.
func walkTree(w interface{ Write([]byte) (int, error) }, prefix string, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			isLast := i == len(keys)-1
			connector, nextPrefix := treeConnector(prefix, isLast)
			child := x[k]
			if isContainer(child) {
				fmt.Fprintf(w, "%s%s\n", connector, k)
				walkTree(w, nextPrefix, child)
			} else {
				fmt.Fprintf(w, "%s%s: %s\n", connector, k, formatScalar(child))
			}
		}
	case []any:
		for i, item := range x {
			isLast := i == len(x)-1
			connector, nextPrefix := treeConnector(prefix, isLast)
			label := fmt.Sprintf("[%d]", i)
			if isContainer(item) {
				fmt.Fprintf(w, "%s%s\n", connector, label)
				walkTree(w, nextPrefix, item)
			} else {
				fmt.Fprintf(w, "%s%s: %s\n", connector, label, formatScalar(item))
			}
		}
	}
}

// isContainer reports whether v is a non-empty map or non-scalar slice
// that warrants recursive descent. Empty containers and slices of
// scalars render inline so the tree stays compact.
func isContainer(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return len(x) > 0
	case []any:
		for _, item := range x {
			switch item.(type) {
			case map[string]any, []any:
				return true
			}
		}
		return false
	}
	return false
}

// formatScalar renders a scalar value (or an inline list) for tree mode.
func formatScalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		if len(x) == 0 {
			return "[]"
		}
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// treeConnector returns the line-leading and child-prefix glyphs for an
// entry at the given position (last vs. middle of its sibling group).
func treeConnector(prefix string, isLast bool) (string, string) {
	if isLast {
		return prefix + "└── ", prefix + "    "
	}
	return prefix + "├── ", prefix + "│   "
}
