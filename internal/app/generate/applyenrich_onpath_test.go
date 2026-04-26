package generate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"dashgen/internal/config"
	"dashgen/internal/enrich"
	"dashgen/internal/ir"
)

// ---------------------------------------------------------------------------
// Fake providers — registered in init() so they never touch production code.
// ---------------------------------------------------------------------------

// fakeTitlesEnricher returns "Fake Title for <UID>" for every panel request.
type fakeTitlesEnricher struct{}

func (fakeTitlesEnricher) Describe() enrich.Description {
	return enrich.Description{Provider: "fake-titles"}
}

func (fakeTitlesEnricher) ClassifyUnknown(_ context.Context, _ enrich.ClassifyInput) (enrich.ClassifyOutput, error) {
	return enrich.ClassifyOutput{}, nil
}

func (fakeTitlesEnricher) EnrichTitles(_ context.Context, in enrich.TitleInput) (enrich.TitleOutput, error) {
	props := make([]enrich.PanelTitleProposal, len(in.Requests))
	for i, req := range in.Requests {
		props[i] = enrich.PanelTitleProposal{
			PanelUID: req.PanelUID,
			Title:    fmt.Sprintf("Fake Title for %s", req.PanelUID),
		}
	}
	return enrich.TitleOutput{Proposals: props}, nil
}

func (fakeTitlesEnricher) EnrichRationale(_ context.Context, _ enrich.RationaleInput) (enrich.RationaleOutput, error) {
	return enrich.RationaleOutput{}, nil
}

// fakeErroringEnricher always returns an error from EnrichTitles, simulating a
// provider that constructs successfully but fails on every outbound call.
type fakeErroringEnricher struct{}

func (fakeErroringEnricher) Describe() enrich.Description {
	return enrich.Description{Provider: "fake-erroring"}
}

func (fakeErroringEnricher) ClassifyUnknown(_ context.Context, _ enrich.ClassifyInput) (enrich.ClassifyOutput, error) {
	return enrich.ClassifyOutput{}, nil
}

func (fakeErroringEnricher) EnrichTitles(_ context.Context, _ enrich.TitleInput) (enrich.TitleOutput, error) {
	return enrich.TitleOutput{}, errors.New("fake provider: intentional failure")
}

func (fakeErroringEnricher) EnrichRationale(_ context.Context, _ enrich.RationaleInput) (enrich.RationaleOutput, error) {
	return enrich.RationaleOutput{}, nil
}

func init() {
	enrich.Register("fake-titles", func(_ enrich.Spec) (enrich.Enricher, error) {
		return fakeTitlesEnricher{}, nil
	})
	enrich.Register("fake-erroring", func(_ enrich.Spec) (enrich.Enricher, error) {
		return fakeErroringEnricher{}, nil
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestApplyEnrichment_OnPath_TitlesByteIdentical verifies two contracts for the
// live (non-noop) enrichment path:
//
// 1. dispatch_fires: applyEnrichment with provider="fake-titles" and
// modes=["titles"] writes Panel.MechanicalTitle on every non-refused panel.
//
// 2. byte_identical_runs: two identical calls to generate.Run with the same
// provider and modes produce byte-identical dashboard.json (determinism on the
// enriched path — no hidden randomness from call ordering or map iteration).
func TestApplyEnrichment_OnPath_TitlesByteIdentical(t *testing.T) {
	t.Parallel()

	// Sub-test A: verify dispatch fires at the IR level.
	t.Run("dispatch_fires", func(t *testing.T) {
		t.Parallel()
		in := &ir.Dashboard{
			UID:     "test-dispatch",
			Title:   "test",
			Profile: "service",
			Rows: []ir.Row{{
				Title: "traffic",
				Panels: []ir.Panel{
					{UID: "p1", Title: "rps", Kind: ir.PanelKindTimeSeries},
					{UID: "p2", Title: "errors", Kind: ir.PanelKindTimeSeries},
				},
			}},
		}
		cfg := &config.RunConfig{
			Provider:    "fake-titles",
			EnrichModes: []string{"titles"},
		}
		out, err := applyEnrichment(context.Background(), in, cfg)
		if err != nil {
			t.Fatalf("applyEnrichment returned error: %v", err)
		}
		found := false
		for _, row := range out.Rows {
			for _, p := range row.Panels {
				want := fmt.Sprintf("Fake Title for %s", p.UID)
				if p.MechanicalTitle == want {
					found = true
				} else {
					t.Errorf("panel %q: MechanicalTitle = %q, want %q", p.UID, p.MechanicalTitle, want)
				}
			}
		}
		if !found {
			t.Error("no panel had MechanicalTitle matching fake pattern; dispatch may not have fired")
		}
	})

	// Sub-test B: two full Run() invocations produce byte-identical output.
	t.Run("byte_identical_runs", func(t *testing.T) {
		t.Parallel()
		makeCfg := func(outDir string) *config.RunConfig {
			return &config.RunConfig{
				FixtureDir:  "../../../testdata/fixtures/service-basic",
				Profile:     "service",
				OutDir:      outDir,
				Provider:    "fake-titles",
				EnrichModes: []string{"titles"},
			}
		}
		out1 := t.TempDir()
		if err := Run(context.Background(), makeCfg(out1)); err != nil {
			t.Fatalf("first Run: %v", err)
		}
		out2 := t.TempDir()
		if err := Run(context.Background(), makeCfg(out2)); err != nil {
			t.Fatalf("second Run: %v", err)
		}
		got1 := readGoldenTriple(t, out1)
		got2 := readGoldenTriple(t, out2)
		for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
			if !bytes.Equal(got1[name], got2[name]) {
				t.Errorf("%s: not byte-identical between two enriched runs", name)
			}
		}
	})
}

// TestApplyEnrichment_OnPath_DegradesOnError verifies the "graceful degradation"
// contract from V0.2-PLAN §2.5: if the enricher returns an error after successful
// construction, Run completes without returning an error and the rendered output
// is byte-identical to the noop-path service-basic golden (the enrichment error
// must not corrupt or suppress the deterministic output).
func TestApplyEnrichment_OnPath_DegradesOnError(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir:  "../../../testdata/fixtures/service-basic",
		Profile:     "service",
		OutDir:      outDir,
		Provider:    "fake-erroring",
		EnrichModes: []string{"titles"},
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run with erroring provider should degrade gracefully, got: %v", err)
	}
	// Output must be byte-identical to the noop-path golden.
	want := readGoldenTriple(t, "../../../testdata/goldens/service-basic")
	got := readGoldenTriple(t, outDir)
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		if !bytes.Equal(got[name], want[name]) {
			t.Errorf("%s: degraded output differs from noop golden; enrichment errors must not corrupt output", name)
		}
	}
}

// TestApplyEnrichment_OnPath_CacheKeyChangesWithModel would verify that two runs
// with different cfg.Model values produce distinct on-disk cache entries. This
// contract is specific to hosted providers (anthropic, openai) whose cache key
// includes the model ID. Fake in-memory providers registered in this test file
// do not participate in the on-disk cache, so there is nothing to walk.
//
// TODO(phase-3): when a hosted provider can be exercised end-to-end from
// generate.Run via a recorded-HTTP transport, point cfg.CacheDir = t.TempDir()
// and assert ≥2 distinct files after two runs with different cfg.Model values.
// Until then, the model→cache-key composition is covered by provider-level tests
// in internal/enrich/anthropic_test.go and internal/enrich/openai_test.go.
func TestApplyEnrichment_OnPath_CacheKeyChangesWithModel(t *testing.T) {
	t.Skip("fake providers are in-memory: on-disk cache wiring is not testable at this level; see internal/enrich/*_test.go")
}
