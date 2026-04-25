// Package enrich is the v0.2 AI-plumbing seam between the deterministic
// dashboard synthesis pipeline and optional AI enrichment providers.
//
// The Enricher interface is intentionally narrow: it takes string-keyed inputs
// (metric names, section names, panel UIDs) so that this package never imports
// internal/classify or internal/ir, avoiding import cycles.
//
// Contract rules inherited from V0.2-PLAN §2.2:
//   - Enrichers MUST NOT generate PromQL. All queries come from the
//     deterministic recipe pipeline. No method in this interface accepts or
//     returns a PromQL expression.
//   - Enrichers MUST NOT influence validation verdicts. Accept/refuse decisions
//     remain the exclusive output of internal/validate.
//   - Label values are never passed to any method (redaction rule §2.5). Only
//     label names are included in MetricBrief.Labels.
//
// NoopEnricher is the default implementation. It is used when the caller
// passes --provider=off or when a real provider errors out. Providers such as
// anthropic and openai are registered in sibling files added in a later phase.
package enrich

import "context"

// Enricher is the interface every AI provider must satisfy.
//
// ClassifyUnknown, EnrichTitles, and EnrichRationale all follow the same
// failure contract: return a non-nil error only when the provider itself
// failed. Returning empty output with a nil error is always valid and causes
// the caller to fall back to the deterministic result.
type Enricher interface {
	// Describe returns metadata about the provider for audit trails and
	// rationale documents.
	Describe() Description

	// ClassifyUnknown proposes trait candidates for metrics the deterministic
	// classifier could not place. Returning an empty Hints slice is valid.
	// An error is returned only when the provider itself failed; unrecognised
	// metrics are not an error.
	ClassifyUnknown(ctx context.Context, in ClassifyInput) (ClassifyOutput, error)

	// EnrichTitles proposes human-scannable panel titles. The caller falls
	// back to the mechanical title if this method returns an error.
	EnrichTitles(ctx context.Context, in TitleInput) (TitleOutput, error)

	// EnrichRationale proposes supplementary rationale paragraphs per panel.
	// The mechanical rationale sentence is always preserved by the caller as
	// an audit trail regardless of this method's output.
	EnrichRationale(ctx context.Context, in RationaleInput) (RationaleOutput, error)
}

// Description identifies the provider for rationale documents and cache keys.
type Description struct {
	Provider string // "noop" | "anthropic" | "openai"
	Model    string // informational; "" for noop
	Offline  bool   // true only for noop; hosted providers (anthropic, openai) are network-bound
}

// MetricBrief is a redacted summary of one Prometheus metric suitable for
// sending to an AI provider. Label values are never included; only label
// names are present (see §2.5 of V0.2-PLAN).
type MetricBrief struct {
	Name   string   // metric name
	Type   string   // Prometheus type ("counter", "gauge", "histogram", "summary") or ""
	Help   string   // from Prometheus metadata; may be empty
	Unit   string   // from Prometheus metadata; may be empty
	Labels []string // sorted label NAMES only; never label values
}

// ClassifyInput carries the metrics for which the deterministic classifier
// produced no trait match.
type ClassifyInput struct {
	Metrics []MetricBrief
}

// TraitHint is one provider suggestion associating a set of trait names with
// a metric. Confidence is in [0, 1]; the caller may use it to gate acceptance.
type TraitHint struct {
	Metric     string   // metric name
	Traits     []string // proposed trait names, e.g. "service_http"
	Confidence float64  // 0..1
}

// ClassifyOutput carries the provider's trait proposals. An empty Hints slice
// is valid and means the provider had no suggestions.
type ClassifyOutput struct {
	Hints []TraitHint
}

// PanelTitleRequest describes one panel whose title the provider may improve.
type PanelTitleRequest struct {
	PanelUID        string // stable panel identity (from internal/ids)
	MechanicalTitle string // deterministic fallback; always preserved in rationale
	MetricName      string
	Section         string
	Rationale       string // mechanical rationale sentence
}

// TitleInput carries all panels whose titles are candidates for enrichment.
type TitleInput struct {
	Requests []PanelTitleRequest
}

// PanelTitleProposal is the provider's suggested replacement title for one panel.
type PanelTitleProposal struct {
	PanelUID string
	Title    string // proposed replacement; caller uses MechanicalTitle if empty
}

// TitleOutput carries the provider's title proposals.
type TitleOutput struct {
	Proposals []PanelTitleProposal
}

// PanelRationaleRequest describes one panel for which the provider may supply
// supplementary rationale prose.
type PanelRationaleRequest struct {
	PanelUID        string
	MechanicalTitle string
	MetricName      string
	Section         string
	Rationale       string   // mechanical rationale sentence (always emitted by caller)
	QueryExprs      []string // deterministic PromQL expressions; read-only for provider
}

// RationaleInput carries all panels that are candidates for rationale enrichment.
type RationaleInput struct {
	Requests []PanelRationaleRequest
}

// PanelRationaleProposal is the provider's supplementary prose for one panel.
// It is appended to, never replaces, the mechanical rationale sentence.
type PanelRationaleProposal struct {
	PanelUID  string
	Paragraph string // supplementary natural-language paragraph
}

// RationaleOutput carries the provider's rationale proposals.
type RationaleOutput struct {
	Proposals []PanelRationaleProposal
}

// NoopEnricher is the default, always-empty enricher. It is used when the
// user passes --provider=off or when a real provider errors out.
// Contract: returns nil error and zero-valued output from every method.
type NoopEnricher struct{}

// NewNoopEnricher returns the default no-op enricher.
func NewNoopEnricher() *NoopEnricher { return &NoopEnricher{} }

// Describe returns the noop provider description.
func (NoopEnricher) Describe() Description {
	return Description{Provider: "noop", Offline: true}
}

// ClassifyUnknown returns empty output and no error.
func (NoopEnricher) ClassifyUnknown(_ context.Context, _ ClassifyInput) (ClassifyOutput, error) {
	return ClassifyOutput{}, nil
}

// EnrichTitles returns empty output and no error.
func (NoopEnricher) EnrichTitles(_ context.Context, _ TitleInput) (TitleOutput, error) {
	return TitleOutput{}, nil
}

// EnrichRationale returns empty output and no error.
func (NoopEnricher) EnrichRationale(_ context.Context, _ RationaleInput) (RationaleOutput, error) {
	return RationaleOutput{}, nil
}
