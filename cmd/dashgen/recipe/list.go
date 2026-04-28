package recipe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"dashgen/internal/recipes"
)

// recipeInfo is the canonical display record for a single registered recipe.
// Used for both text and JSON output.
type recipeInfo struct {
	Name        string   `json:"name"`
	Profile     string   `json:"profile"`
	Section     string   `json:"section"`
	Confidence  float64  `json:"confidence"`
	Source      string   `json:"source"`
	Path        string   `json:"path"`
	Tier        string   `json:"tier,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description,omitempty"`
}

type listArgs struct {
	profile       string
	source        string
	match         string
	output        string
	recipesDirs   []string
	noUserRecipes bool
}

func newListCmd() *cobra.Command {
	var a listArgs

	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print all registered recipes with provenance",
		Long: `Print all registered recipes (built-in + user) with provenance information.

Columns (text): NAME, PROFILE, SECTION, CONFIDENCE, SOURCE, PATH.
Sorted by (profile, name). Use --output json for machine-readable output.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, a)
		},
	}

	cmd.Flags().StringVar(&a.profile, "profile", "",
		"filter to one profile: service, infra, or k8s")
	cmd.Flags().StringVar(&a.source, "source", "all",
		"filter by source: builtin, user, or all")
	cmd.Flags().StringVar(&a.match, "match", "",
		"shell-style glob against recipe name (e.g. 'service_http_*')")
	cmd.Flags().StringVar(&a.output, "output", "text",
		"output format: text or json")
	cmd.Flags().StringArrayVar(&a.recipesDirs, "recipes-dir", nil,
		"user recipe directory (repeatable)")
	cmd.Flags().BoolVar(&a.noUserRecipes, "no-user-recipes", false,
		"ignore user directories; builtins only")

	return cmd
}

func runList(cmd *cobra.Command, a listArgs) error {
	if err := validateListArgs(a); err != nil {
		return err
	}

	pr := recipes.NewProfileRegistries()

	if !a.noUserRecipes {
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

	infos := collectRecipeInfos(pr)
	infos = filterRecipes(infos, a)

	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Profile != infos[j].Profile {
			return infos[i].Profile < infos[j].Profile
		}
		return infos[i].Name < infos[j].Name
	})

	switch a.output {
	case "json":
		return outputListJSON(cmd, infos)
	default:
		return outputListText(cmd, infos)
	}
}

// validateListArgs checks flag values before any I/O.
func validateListArgs(a listArgs) error {
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
	case "text", "json":
	default:
		return fmt.Errorf("flag --output: %q must be one of [text json]", a.output)
	}
	if a.match != "" {
		if _, err := path.Match(a.match, ""); err != nil {
			return fmt.Errorf("flag --match: %q is not a valid glob pattern: %w", a.match, err)
		}
	}
	return nil
}

// resolveListUserDirs returns the list of user recipe directories to search.
// When explicit dirs are given they are abs-resolved and returned. When none
// are given, the XDG default is tried; a missing default is silently skipped
// (the user simply hasn't run "dashgen recipe init" yet).
func resolveListUserDirs(explicit []string) ([]string, error) {
	if len(explicit) > 0 {
		dirs := make([]string, 0, len(explicit))
		for _, d := range explicit {
			abs, err := filepath.Abs(d)
			if err != nil {
				return nil, fmt.Errorf("resolve path %q: %w", d, err)
			}
			dirs = append(dirs, abs)
		}
		return dirs, nil
	}
	// No explicit dirs: try XDG default, skip if it doesn't exist yet.
	dir, err := resolveRecipesDir("")
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		return nil, nil
	}
	return []string{dir}, nil
}

// collectRecipeInfos enumerates all recipes from all three profile registries.
// For YAML recipes, full metadata is extracted via type assertion. For Go
// recipes, provenance defaults are used (source=builtin, confidence=1.0).
func collectRecipeInfos(pr *recipes.ProfileRegistries) []recipeInfo {
	profRegs := []struct {
		profile string
		reg     *recipes.Registry
	}{
		{"service", pr.Service},
		{"infra", pr.Infra},
		{"k8s", pr.K8s},
	}

	var infos []recipeInfo
	for _, entry := range profRegs {
		for _, rec := range entry.reg.All() {
			infos = append(infos, recipeInfoFrom(rec, entry.profile))
		}
	}
	return infos
}

// recipeInfoFrom converts a recipes.Recipe to a recipeInfo. YAML recipes
// carry full metadata; Go recipes use synthesized provenance.
func recipeInfoFrom(rec recipes.Recipe, profile string) recipeInfo {
	if yr, ok := rec.(*recipes.YAMLRecipe); ok {
		return recipeInfo{
			Name:        yr.Spec.Metadata.Name,
			Profile:     yr.Spec.Metadata.Profile,
			Section:     yr.Spec.Metadata.Section,
			Confidence:  yr.Spec.Metadata.Confidence,
			Source:      yr.Source,
			Path:        yr.Path,
			Tier:        yr.Spec.Metadata.Tier,
			Tags:        yr.Spec.Metadata.Tags,
			Description: yr.Spec.Metadata.Description,
		}
	}
	// Go-implemented recipe: built-in, no file path.
	return recipeInfo{
		Name:       rec.Name(),
		Profile:    profile,
		Section:    rec.Section(),
		Confidence: 1.0,
		Source:     recipes.SourceBuiltin,
	}
}

// filterRecipes applies the profile, source, and match filters to infos.
func filterRecipes(infos []recipeInfo, a listArgs) []recipeInfo {
	out := make([]recipeInfo, 0, len(infos))
	for _, info := range infos {
		if a.profile != "" && info.Profile != a.profile {
			continue
		}
		if a.source != "all" && info.Source != a.source {
			continue
		}
		if a.match != "" {
			matched, err := path.Match(a.match, info.Name)
			if err != nil || !matched {
				continue
			}
		}
		out = append(out, info)
	}
	return out
}

// outputListText writes the recipe list as tab-aligned columns.
func outputListText(cmd *cobra.Command, infos []recipeInfo) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPROFILE\tSECTION\tCONFIDENCE\tSOURCE\tPATH")
	for _, info := range infos {
		fmt.Fprintf(w, "%s\t%s\t%s\t%.2f\t%s\t%s\n",
			info.Name, info.Profile, info.Section,
			info.Confidence, info.Source, info.Path)
	}
	return w.Flush()
}

// outputListJSON writes the recipe list as a JSON array. An empty result
// produces [] rather than null.
func outputListJSON(cmd *cobra.Command, infos []recipeInfo) error {
	if infos == nil {
		infos = []recipeInfo{}
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(infos)
}
