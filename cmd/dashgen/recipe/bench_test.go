package recipe

import (
	"testing"

	"dashgen/internal/recipes"
)

// (BenchmarkLint_SingleFile lives in lint_test.go — pre-existing end-to-end
// benchmark via cobra Execute. T7.3 added the budget-assertion guard there.)

// BenchmarkList_FullRegistry exercises the data path of `dashgen recipe list`
// against the full built-in registry (47 recipes): load profile registries +
// collect recipe infos. Target per RECIPES-CLI.md §10.3: ≤500 ms.
//
// The cobra-parser surface is excluded (negligible overhead vs the load).
func BenchmarkList_FullRegistry(b *testing.B) {
	const budgetMS = 500.0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pr := recipes.NewProfileRegistries()
		_ = collectRecipeInfos(pr)
	}
	b.StopTimer()

	msPerOp := float64(b.Elapsed()) / float64(b.N) / 1e6
	if msPerOp > budgetMS {
		b.Errorf("budget exceeded: list %.1fms/op > %.0fms (T7.3)", msPerOp, budgetMS)
	}
}
