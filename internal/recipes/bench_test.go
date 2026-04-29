package recipes

import "testing"

// BenchmarkLoadBuiltinRegistry measures cold load of the full built-in
// registry (47 YAMLs across the 3 profiles). Target per V0.3-PLAN T7.3:
// ≤500 ms cold on commodity hardware. Each iteration constructs a fresh
// ProfileRegistries — no caching is reused across iterations.
//
// Run with: go test -bench=BenchmarkLoadBuiltinRegistry -benchtime=10x
func BenchmarkLoadBuiltinRegistry(b *testing.B) {
	const budgetMS = 500.0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewProfileRegistries()
	}
	b.StopTimer()

	msPerOp := float64(b.Elapsed()) / float64(b.N) / 1e6
	if msPerOp > budgetMS {
		b.Errorf("budget exceeded: cold load %.1fms/op > %.0fms (T7.3)", msPerOp, budgetMS)
	}
}
