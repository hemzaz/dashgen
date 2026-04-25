package generate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dashgen/internal/config"
)

// TestPartialRegeneration_IdempotentOverIdenticalInventory is the
// load-bearing acceptance for v0.2 Phase 6 Step 3.2: when --in-place
// is set and the inventory is unchanged, a re-run produces byte-
// identical output AND does not rewrite any of the three output
// files (mtime preserved). Cross-section preservation across
// inventory changes is OUT of scope (deferred to v0.3 per
// .omc/plans/v0.2-remainder.md §7.2).
func TestPartialRegeneration_IdempotentOverIdenticalInventory(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: "../../../testdata/fixtures/service-basic",
		Profile:    "service",
		OutDir:     out,
		InPlace:    true,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	mtimes := map[string]time.Time{}
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("stat %s after first run: %v", name, err)
		}
		mtimes[name] = info.ModTime()
	}

	// Sleep so a hypothetical rewrite would advance mtime past the
	// filesystem's resolution window.
	time.Sleep(20 * time.Millisecond)

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("stat %s after second run: %v", name, err)
		}
		if !info.ModTime().Equal(mtimes[name]) {
			t.Errorf("%s mtime advanced despite identical inventory: before=%v after=%v",
				name, mtimes[name], info.ModTime())
		}
	}
}

// TestInPlace_RewritesWhenContentDiffers: when --in-place is set but
// the on-disk content is stale (e.g., a hand edit), Run rewrites the
// file. This guards against the "in-place silently keeps stale
// content" failure mode.
func TestInPlace_RewritesWhenContentDiffers(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: "../../../testdata/fixtures/service-basic",
		Profile:    "service",
		OutDir:     out,
		InPlace:    true,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dashPath := filepath.Join(out, "dashboard.json")
	good, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	// Tamper.
	if err := os.WriteFile(dashPath, []byte("stale content"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	got, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(got) != string(good) {
		t.Errorf("InPlace did not restore tampered file:\nwant %d bytes, got %d bytes", len(good), len(got))
	}
}

// TestInPlace_OffByDefault_PreservesV01Behavior: with InPlace=false
// (the v0.1 default), every run rewrites the files unconditionally.
// We verify by tampering with a file and confirming Run replaced it,
// which is the existing v0.1 contract.
func TestInPlace_OffByDefault_PreservesV01Behavior(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: "../../../testdata/fixtures/service-basic",
		Profile:    "service",
		OutDir:     out,
		// InPlace defaults to false.
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	dashPath := filepath.Join(out, "dashboard.json")
	good, _ := os.ReadFile(dashPath)
	if err := os.WriteFile(dashPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	got, _ := os.ReadFile(dashPath)
	if string(got) != string(good) {
		t.Errorf("v0.1 default did not rewrite file: want %d bytes, got %d", len(good), len(got))
	}
}
