package inspect

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"dashgen/internal/config"
)

// fixtureDir is the canonical first-slice fixture, resolved from the package
// test working directory.
const fixtureDir = "../../../testdata/fixtures/service-basic"

// TestRunTo_ServiceBasic_ContainsExpectedSections asserts the report has
// every required section header plus at least one known metric, recipe, and
// verdict string.
func TestRunTo_ServiceBasic_ContainsExpectedSections(t *testing.T) {
	out := runOnce(t)
	text := out.String()

	for _, want := range []string{
		"Inventory",
		"Classification",
		"Recipes",
		"Candidates",
		"Summary",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing section %q\n---\n%s", want, text)
		}
	}

	if !strings.Contains(text, "http_requests_total") {
		t.Errorf("report missing metric name http_requests_total\n---\n%s", text)
	}
	if !strings.Contains(text, "service_http_rate") {
		t.Errorf("report missing recipe name service_http_rate\n---\n%s", text)
	}
	// The validate pipeline must have labeled candidates with at least one
	// of the verdict words.
	if !strings.Contains(text, "accept") && !strings.Contains(text, "refuse") {
		t.Errorf("report missing any verdict word\n---\n%s", text)
	}
}

// TestRunTo_Determinism runs inspect twice into separate buffers and asserts
// byte-for-byte equality. Catches map iteration leaks.
func TestRunTo_Determinism(t *testing.T) {
	first := runOnce(t)
	second := runOnce(t)
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Errorf("determinism: reports differ across runs\n-- first --\n%s\n-- second --\n%s",
			first.String(), second.String())
	}
}

// TestRunTo_DefaultProfile asserts an empty cfg.Profile falls back to service
// without erroring.
func TestRunTo_DefaultProfile(t *testing.T) {
	var buf bytes.Buffer
	cfg := &config.RunConfig{
		FixtureDir: fixtureDir,
		// Profile intentionally left blank.
	}
	if err := RunTo(context.Background(), cfg, &buf); err != nil {
		t.Fatalf("RunTo: %v", err)
	}
	if !strings.Contains(buf.String(), "Inspection: service") {
		t.Errorf("default profile: expected service header\n---\n%s", buf.String())
	}
}

// TestRunTo_MutualExclusion catches the common wiring mistake of both
// --prom-url and --fixture-dir set simultaneously.
func TestRunTo_MutualExclusion(t *testing.T) {
	var buf bytes.Buffer
	cfg := &config.RunConfig{
		FixtureDir: fixtureDir,
		PromURL:    "http://example:9090",
		Profile:    "service",
	}
	if err := RunTo(context.Background(), cfg, &buf); err == nil {
		t.Fatal("expected mutual-exclusion error, got nil")
	}
}

func runOnce(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	cfg := &config.RunConfig{
		FixtureDir: fixtureDir,
		Profile:    "service",
	}
	if err := RunTo(context.Background(), cfg, &buf); err != nil {
		t.Fatalf("RunTo: %v", err)
	}
	return &buf
}
