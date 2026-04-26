// Anthropic Messages API backend for the v0.2 enrichment seam.
//
// This file ships in v0.2 Phase 3. The provider is intentionally narrow:
// it speaks the Anthropic /v1/messages REST surface, parses the strict-
// JSON outputs the prompt templates in prompts.go ask for, and never
// produces or proxies a PromQL expression (V0.2-PLAN §2.2).
//
// The redaction contract from V0.2-PLAN §2.5 is enforced at one point —
// every code path that issues an outbound HTTP write first runs
// ValidateBriefs against the MetricBrief slice it is about to serialize.
// The proxy-capture canary in anthropic_test.go pins this guard.
package enrich

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"
)

const (
	anthropicDefaultModel    = "claude-opus-4-7"
	anthropicDefaultEndpoint = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion      = "2023-06-01"
	anthropicMaxTokens       = 1024
	anthropicHTTPTimeout     = 30 * time.Second
)

// ErrAnthropicNoAPIKey is returned by the constructor when ANTHROPIC_API_KEY
// is unset. Callers can distinguish this from registry errors with errors.Is
// — it is intentionally NOT wrapped with ErrNotImplementedYet because the
// provider IS implemented; the operator simply has not configured credentials.
var ErrAnthropicNoAPIKey = errors.New("enrich: ANTHROPIC_API_KEY not set; required for the anthropic provider")

// AnthropicEnricher is a hosted-AI Enricher backed by the Anthropic Messages API.
// All fields are unexported. Tests substitute the endpoint via the same-package
// helper newAnthropicEnricherForTest in anthropic_test.go.
type AnthropicEnricher struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
}

// newAnthropicEnricher constructs an Enricher from a Spec. Registered at
// init() time as the "anthropic" provider in a later commit; for now this
// constructor is callable only from same-package tests.
func newAnthropicEnricher(spec Spec) (Enricher, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, ErrAnthropicNoAPIKey
	}
	model := spec.Model
	if model == "" {
		model = anthropicDefaultModel
	}
	return &AnthropicEnricher{
		apiKey:     apiKey,
		model:      model,
		endpoint:   anthropicDefaultEndpoint,
		httpClient: &http.Client{Timeout: anthropicHTTPTimeout},
	}, nil
}

// Describe identifies the provider for audit trails and cache keys.
func (e *AnthropicEnricher) Describe() Description {
	return Description{Provider: "anthropic", Model: e.model}
}

// ClassifyUnknown is a TODO stub. Implementation lands in the next commit.
func (e *AnthropicEnricher) ClassifyUnknown(_ context.Context, _ ClassifyInput) (ClassifyOutput, error) {
	return ClassifyOutput{}, nil
}

// EnrichTitles is a TODO stub. Implementation lands in the next commit.
func (e *AnthropicEnricher) EnrichTitles(_ context.Context, _ TitleInput) (TitleOutput, error) {
	return TitleOutput{}, nil
}

// EnrichRationale is a TODO stub. Implementation lands in the next commit.
func (e *AnthropicEnricher) EnrichRationale(_ context.Context, _ RationaleInput) (RationaleOutput, error) {
	return RationaleOutput{}, nil
}
