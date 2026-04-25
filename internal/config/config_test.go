package config

import (
	"testing"
	"time"
)

// TestRunConfig_ZeroValueDefaults locks in the v0.2 enrichment-seam contract:
// when no config file or CLI flag overrides the four enrichment fields, they
// must be at their zero values so the generate pipeline takes the v0.1
// deterministic path (NoopEnricher → byte-identical output to v0.1 goldens).
//
// If any of these defaults change, the v0.1 goldens are likely to drift, and
// the V0.2-PLAN §6 "AI-off parity" acceptance criterion is at risk.
func TestRunConfig_ZeroValueDefaults(t *testing.T) {
	t.Parallel()
	got := Defaults()

	if got.Provider != "" {
		t.Errorf("Provider default = %q, want \"\"", got.Provider)
	}
	if got.EnrichModes != nil {
		t.Errorf("EnrichModes default = %v, want nil", got.EnrichModes)
	}
	if got.CacheDir != "" {
		t.Errorf("CacheDir default = %q, want \"\"", got.CacheDir)
	}
	if got.NoEnrichCache {
		t.Errorf("NoEnrichCache default = true, want false")
	}

	// Re-affirm the v0.1 defaults the v0.2 fields sit alongside, so a future
	// change that disturbs them is caught here too.
	if got.Profile != "service" {
		t.Errorf("Profile default = %q, want \"service\"", got.Profile)
	}
	if got.OutDir != "./out" {
		t.Errorf("OutDir default = %q, want \"./out\"", got.OutDir)
	}
	if got.HTTPTimeout != 5*time.Second {
		t.Errorf("HTTPTimeout default = %v, want 5s", got.HTTPTimeout)
	}
}
