package recipes

import (
	"context"
	"embed"
	"fmt"
	"path"
)

// DataFS embeds the v0.3 built-in YAML recipe catalog. Subdirectories
// under data/ are organized by profile:
//
//	data/service/<recipe>.yaml
//	data/infra/<recipe>.yaml
//	data/k8s/<recipe>.yaml
//
// Sibling .testdata.json files are embedded for the harness test
// (yaml_recipes_harness_test.go); the loader only reads *.yaml entries.
//
//go:embed data
var DataFS embed.FS

// LoadBuiltinYAMLs loads + registers every *.yaml under
// data/<profileSubdir>/ into target via Register. Designed to be called
// from the per-profile registry constructors (NewServiceRegistry,
// NewInfraRegistry, NewK8sRegistry) AFTER the surviving Go recipes
// have been registered.
//
// A name collision with an already-registered Go recipe panics: the
// migration contract is "remove the Go registration before adding the
// YAML". Build-time errors should surface immediately.
//
// Errors during built-in YAML decode panic: built-ins are CUE-validated
// at build time and ship in the binary. Any failure means the binary
// itself is malformed.
func LoadBuiltinYAMLs(target *Registry, profileSubdir string) {
	if target == nil {
		return
	}
	root := path.Join("data", profileSubdir)
	cfg := LoaderConfig{
		BuiltinFS:   DataFS,
		BuiltinRoot: root,
	}
	loaded, err := Load(context.Background(), cfg)
	if err != nil {
		panic(fmt.Sprintf("recipes: load built-in YAMLs from %s: %v", root, err))
	}
	for _, l := range loaded {
		rec, err := NewYAMLRecipe(l)
		if err != nil {
			panic(fmt.Sprintf("recipes: build YAMLRecipe %s: %v", l.Path, err))
		}
		if existing := target.ByName(rec.Name()); existing != nil {
			panic(fmt.Sprintf(
				"recipes: built-in YAML %s collides with already-registered recipe %q",
				l.Path, rec.Name(),
			))
		}
		target.Register(rec)
	}
}
