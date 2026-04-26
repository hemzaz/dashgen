package main

import (
	"context"
	"strings"
	"testing"

	"dashgen/internal/config"
)

// testFixtureDir is the service-basic offline fixture, relative to the
// cmd/dashgen package directory where go test runs.
const testFixtureDir = "../../testdata/fixtures/service-basic"

// TestGenerateCmd_ProviderOffIsDefault verifies that when --provider is not
// supplied, cfg.Provider is the empty string (which the factory treats as
// "off", the no-op path).
func TestGenerateCmd_ProviderOffIsDefault(t *testing.T) {
	t.Parallel()
	var captured *config.RunConfig
	cmd := newGenerateCmdWithRunner(func(_ context.Context, cfg *config.RunConfig) error {
		captured = cfg
		return nil
	})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--fixture-dir", testFixtureDir, "--out", t.TempDir()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if captured == nil {
		t.Fatal("runFn was not called")
	}
	if captured.Provider != "" {
		t.Errorf("Provider = %q, want empty string (routes to off)", captured.Provider)
	}
}

// TestGenerateCmd_FlagsParseAndPropagate is a table-driven test asserting that
// each of the five v0.2 enrichment flags round-trips cleanly into the
// resolved RunConfig.
func TestGenerateCmd_FlagsParseAndPropagate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		args  []string
		check func(t *testing.T, cfg *config.RunConfig)
	}{
		{
			name: "provider",
			args: []string{"--provider", "off"},
			check: func(t *testing.T, cfg *config.RunConfig) {
				if cfg.Provider != "off" {
					t.Errorf("Provider = %q, want \"off\"", cfg.Provider)
				}
			},
		},
		{
			name: "provider_model",
			args: []string{"--provider-model", "claude-3-haiku"},
			check: func(t *testing.T, cfg *config.RunConfig) {
				if cfg.Model != "claude-3-haiku" {
					t.Errorf("Model = %q, want \"claude-3-haiku\"", cfg.Model)
				}
			},
		},
		{
			name: "enrich_two_modes",
			args: []string{"--enrich", "titles,rationale"},
			check: func(t *testing.T, cfg *config.RunConfig) {
				want := []string{"titles", "rationale"}
				if len(cfg.EnrichModes) != len(want) {
					t.Fatalf("EnrichModes = %v, want %v", cfg.EnrichModes, want)
				}
				for i, w := range want {
					if cfg.EnrichModes[i] != w {
						t.Errorf("EnrichModes[%d] = %q, want %q", i, cfg.EnrichModes[i], w)
					}
				}
			},
		},
		{
			name: "no_enrich_cache",
			args: []string{"--no-enrich-cache"},
			check: func(t *testing.T, cfg *config.RunConfig) {
				if !cfg.NoEnrichCache {
					t.Error("NoEnrichCache = false, want true")
				}
			},
		},
		{
			name: "cache_dir",
			args: []string{"--cache-dir", "/tmp/dashgen-test-cache"},
			check: func(t *testing.T, cfg *config.RunConfig) {
				if cfg.CacheDir != "/tmp/dashgen-test-cache" {
					t.Errorf("CacheDir = %q, want \"/tmp/dashgen-test-cache\"", cfg.CacheDir)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var captured *config.RunConfig
			cmd := newGenerateCmdWithRunner(func(_ context.Context, cfg *config.RunConfig) error {
				captured = cfg
				return nil
			})
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			baseArgs := []string{"--fixture-dir", testFixtureDir, "--out", t.TempDir()}
			cmd.SetArgs(append(baseArgs, tc.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if captured == nil {
				t.Fatal("runFn was not called")
			}
			tc.check(t, captured)
		})
	}
}

// TestGenerateCmd_UnknownProviderRejected verifies that --provider with an
// unregistered name fails with an error that names the unknown provider. The
// error is produced by enrich.New (ErrUnknownProvider) and wrapped as
// ErrBackend by the generate pipeline.
func TestGenerateCmd_UnknownProviderRejected(t *testing.T) {
	t.Parallel()
	cmd := newGenerateCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--fixture-dir", testFixtureDir,
		"--out", t.TempDir(),
		"--provider", "banana",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown provider \"banana\", got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error %q does not contain \"unknown provider\"", err.Error())
	}
}

// TestGenerateCmd_LogEnrichmentPayloadsHiddenByDefault asserts the hidden-
// flag contract from ADVERSARY §6: without DASHGEN_DEBUG=1 the
// --log-enrichment-payloads flag is registered but marked hidden so it
// cannot drift into operator-facing CI flows. We assert via the pflag
// Hidden bit (the cobra usage string is generated from this bit, so
// asserting the bit is the strongest local check).
func TestGenerateCmd_LogEnrichmentPayloadsHiddenByDefault(t *testing.T) {
	// Not parallel: t.Setenv mutates the process environment, which is
	// shared across goroutines — and the flag's hidden state is decided
	// at command-construction time from the env var.
	t.Setenv("DASHGEN_DEBUG", "")
	cmd := newGenerateCmd()
	flag := cmd.Flags().Lookup("log-enrichment-payloads")
	if flag == nil {
		t.Fatal("--log-enrichment-payloads flag is not registered")
	}
	if !flag.Hidden {
		t.Error("--log-enrichment-payloads flag is visible without DASHGEN_DEBUG=1; ADVERSARY §6 drift guard regressed")
	}
}

// TestGenerateCmd_LogEnrichmentPayloadsVisibleInDebug asserts the inverse:
// with DASHGEN_DEBUG=1 set at construction time, the flag is exposed in
// help output. This is the diagnostic escape hatch the spec requires.
func TestGenerateCmd_LogEnrichmentPayloadsVisibleInDebug(t *testing.T) {
	// Not parallel: t.Setenv mutates the process environment.
	t.Setenv("DASHGEN_DEBUG", "1")
	cmd := newGenerateCmd()
	flag := cmd.Flags().Lookup("log-enrichment-payloads")
	if flag == nil {
		t.Fatal("--log-enrichment-payloads flag is not registered")
	}
	if flag.Hidden {
		t.Error("--log-enrichment-payloads flag is hidden under DASHGEN_DEBUG=1; expected visible")
	}
}

// TestGenerateCmd_LogEnrichmentPayloadsPropagates verifies that the
// flag value round-trips into the resolved RunConfig regardless of
// hidden state. cobra's documented behavior is that hidden flags are
// still parseable on the command line — this test guards against a
// future MarkHidden upgrade silently breaking that.
func TestGenerateCmd_LogEnrichmentPayloadsPropagates(t *testing.T) {
	t.Parallel()
	var captured *config.RunConfig
	cmd := newGenerateCmdWithRunner(func(_ context.Context, cfg *config.RunConfig) error {
		captured = cfg
		return nil
	})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--fixture-dir", testFixtureDir,
		"--out", t.TempDir(),
		"--log-enrichment-payloads",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if captured == nil {
		t.Fatal("runFn was not called")
	}
	if !captured.LogEnrichmentPayloads {
		t.Error("LogEnrichmentPayloads = false; want true after --log-enrichment-payloads")
	}
}

// TestGenerateCmd_EnrichModesParse verifies that a comma-separated list with
// surrounding whitespace is split and trimmed into the correct slice.
func TestGenerateCmd_EnrichModesParse(t *testing.T) {
	t.Parallel()
	var captured *config.RunConfig
	cmd := newGenerateCmdWithRunner(func(_ context.Context, cfg *config.RunConfig) error {
		captured = cfg
		return nil
	})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--fixture-dir", testFixtureDir,
		"--out", t.TempDir(),
		"--enrich", "titles, rationale",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if captured == nil {
		t.Fatal("runFn was not called")
	}
	want := []string{"titles", "rationale"}
	if len(captured.EnrichModes) != len(want) {
		t.Fatalf("EnrichModes = %v, want %v", captured.EnrichModes, want)
	}
	for i, w := range want {
		if captured.EnrichModes[i] != w {
			t.Errorf("EnrichModes[%d] = %q, want %q", i, captured.EnrichModes[i], w)
		}
	}
}
