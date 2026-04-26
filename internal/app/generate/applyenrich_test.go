package generate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dashgen/internal/config"
	"dashgen/internal/ir"
)

// TestApplyEnrichment_NoopPassthrough_Smoke is the unit-level contract for the
// v0.2 enrichment seam: when cfg.Provider is "", "off", or "noop", the
// applyEnrichment glue returns the *same dashboard pointer* with no mutation
// and no allocation. This is the load-bearing assertion that AI-off mode
// cannot perturb the deterministic IR.
//
// V0.2-PLAN §2.5 "Failure modes and defaults": no provider configured ⇒
// deterministic-only path. This test fails the build the moment that
// invariant is broken in code.
func TestApplyEnrichment_NoopPassthrough_Smoke(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		provider string
	}{
		{"empty_provider", ""},
		{"off_provider", "off"},
		{"noop_provider", "noop"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &ir.Dashboard{
				UID:     "abc12345",
				Title:   "synthetic",
				Profile: "service",
				Rows: []ir.Row{{
					Title: "traffic",
					Panels: []ir.Panel{{
						UID:   "p1",
						Title: "rps",
						Kind:  ir.PanelKindTimeSeries,
					}},
				}},
			}
			cfg := &config.RunConfig{Provider: tc.provider}

			out, err := applyEnrichment(context.Background(), in, cfg)
			if err != nil {
				t.Fatalf("applyEnrichment(%q) returned error: %v", tc.provider, err)
			}
			if out != in {
				t.Errorf("applyEnrichment(%q) returned different pointer; Noop must pass through unchanged", tc.provider)
			}
			// Spot-check: no field on the returned panel was mutated.
			if got := out.Rows[0].Panels[0].Title; got != "rps" {
				t.Errorf("panel title was mutated: got %q want \"rps\"", got)
			}
			if got := out.Rows[0].Panels[0].MechanicalTitle; got != "" {
				t.Errorf("MechanicalTitle was populated by Noop path: %q", got)
			}
			if got := out.Rows[0].Panels[0].RationaleExtra; got != "" {
				t.Errorf("RationaleExtra was populated by Noop path: %q", got)
			}
		})
	}
}

// TestApplyEnrichment_UnknownProviderRejected confirms that any provider
// string outside the {"", "off", "noop"} set fails fast with an error the
// caller wraps as ErrBackend. Phase 3+ adds "anthropic" and "openai"; until
// they ship, those names also fail here.
func TestApplyEnrichment_UnknownProviderRejected(t *testing.T) {
	t.Parallel()
	cfg := &config.RunConfig{Provider: "unknown"}
	in := &ir.Dashboard{UID: "x", Title: "x", Profile: "service"}
	if _, err := applyEnrichment(context.Background(), in, cfg); err == nil {
		t.Fatal("applyEnrichment with unknown provider returned nil error; expected ErrBackend wrap")
	}
}

// TestApplyEnrichment_NoopDefault_ByteIdenticalOutput is the integration-level
// contract: with cfg.Provider == "" (the default), the full generate pipeline
// produces dashboard.json + rationale.md + warnings.json byte-equal to the
// existing v0.1 service-basic golden. This proves the "AI-off parity"
// acceptance criterion from V0.2-PLAN §6.
//
// We deliberately also exercise Provider="off" and Provider="noop" since all
// three must be equivalent in this release.
func TestApplyEnrichment_NoopDefault_ByteIdenticalOutput(t *testing.T) {
	t.Parallel()
	wantDir := "../../../testdata/goldens/service-basic"
	want := readGoldenTriple(t, wantDir)

	for _, provider := range []string{"", "off", "noop"} {
		provider := provider
		t.Run("provider="+labelFor(provider), func(t *testing.T) {
			t.Parallel()
			out := t.TempDir()
			cfg := &config.RunConfig{
				FixtureDir: "../../../testdata/fixtures/service-basic",
				Profile:    "service",
				OutDir:     out,
				Provider:   provider,
			}
			if err := Run(context.Background(), cfg); err != nil {
				t.Fatalf("Run(provider=%q): %v", provider, err)
			}
			got := readGoldenTriple(t, out)
			for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
				if !bytes.Equal(got[name], want[name]) {
					t.Errorf("%s mismatch with provider=%q\n-- want --\n%s\n-- got --\n%s",
						name, provider, want[name], got[name])
				}
			}
		})
	}
}

func readGoldenTriple(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = b
	}
	return out
}

func labelFor(s string) string {
	if s == "" {
		return "empty"
	}
	return s
}

// TestApplyEnrichment_AnthropicWithoutAPIKey_FailsBeforeRender verifies that
// when Provider="anthropic" but ANTHROPIC_API_KEY is not set, Run returns an
// error wrapping ErrBackend AND writes zero output files. The zero-output
// assertion is the load-bearing "no partial output" contract: the operator
// must see a clear error without any partially-rendered files on disk.
//
// Note: this test does not run in parallel because t.Setenv modifies the
// process environment, which is shared across goroutines.
func TestApplyEnrichment_AnthropicWithoutAPIKey_FailsBeforeRender(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	outDir := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: "../../../testdata/fixtures/service-basic",
		Profile:    "service",
		OutDir:     outDir,
		Provider:   "anthropic",
	}

	err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("Run with provider=anthropic and no API key should return an error, got nil")
	}
	if !errors.Is(err, ErrBackend) {
		t.Errorf("error should wrap ErrBackend; got: %v", err)
	}

	// No output files should have been written — enrichment fails before
	// any rendering or file-write step executes.
	entries, readErr := os.ReadDir(outDir)
	if readErr != nil {
		t.Fatalf("ReadDir(%s): %v", outDir, readErr)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected empty output dir after pre-flight failure, got files: %v", names)
	}
}

// TestApplyEnrichment_OpenAIWithoutAPIKey_FailsBeforeRender mirrors the
// anthropic pre-flight test for the openai provider. The eager API-key
// check at construction (internal/enrich/openai.go) surfaces as
// ErrBackend at the Run boundary, with zero output files written.
//
// The two tests cover the same contract for the two real providers; if
// either regresses, the operator-facing failure mode silently changes
// from "clean error, no files" to "partial output on disk".
//
// Note: this test does not run in parallel because t.Setenv modifies the
// process environment, which is shared across goroutines.
func TestApplyEnrichment_OpenAIWithoutAPIKey_FailsBeforeRender(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	outDir := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: "../../../testdata/fixtures/service-basic",
		Profile:    "service",
		OutDir:     outDir,
		Provider:   "openai",
	}

	err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("Run with provider=openai and no API key should return an error, got nil")
	}
	if !errors.Is(err, ErrBackend) {
		t.Errorf("error should wrap ErrBackend; got: %v", err)
	}

	// No output files should have been written — enrichment fails before
	// any rendering or file-write step executes.
	entries, readErr := os.ReadDir(outDir)
	if readErr != nil {
		t.Fatalf("ReadDir(%s): %v", outDir, readErr)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected empty output dir after pre-flight failure, got files: %v", names)
	}
}
