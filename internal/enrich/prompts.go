package enrich

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Canonical prompt templates for the three Enricher methods. They are
// reused by every hosted provider (Anthropic in Phase 3, OpenAI in
// Phase 4) so the wire payload to one provider is byte-identical to
// another given the same MetricBriefs.
//
// Contract (V0.2-PLAN §2.2):
//   - Templates accept only metric NAMES, label NAMES, and help text.
//     Label values never appear; ValidateBriefs is the runtime guard.
//   - Templates explicitly forbid PromQL output. The deterministic
//     pipeline owns query generation; AI may not produce a query string.
//   - Each template specifies the JSON shape the enricher will parse so
//     malformed responses are discarded by callers (per §2.5).
//
// Any byte change to any of the six string constants below causes
// PromptHash() to change, which causes every cached entry to miss and
// re-fetch — that's the cache-invalidation contract called out in
// V0.2-PLAN §2.4.

// TitleSystemPrompt frames the EnrichTitles request.
const TitleSystemPrompt = `You are a Prometheus dashboard editor.
Your only job is to rewrite mechanical panel titles into short,
human-scannable English.

DO NOT generate PromQL queries; output only the requested fields.
The deterministic pipeline owns all queries.

You receive a list of panels. For each panel you are given a stable
PanelUID, a mechanical fallback title, the metric name, the section
name, and the mechanical rationale sentence.

You must return strictly valid JSON of the shape:
{"proposals":[{"PanelUID":"<uid>","Title":"<short human title>"}]}

Do not include explanations, markdown, code fences, or any field
other than PanelUID and Title.`

// TitleUserTemplate frames the per-request payload for EnrichTitles.
const TitleUserTemplate = `Rewrite the titles of the following panels.
Each entry shows: PanelUID, MechanicalTitle, MetricName, Section,
Rationale.

Constraints:
- DO NOT generate PromQL queries; output only the requested fields.
- Do not invent metrics, labels, or numbers not in the input.
- Keep each Title under 60 characters when possible.

Panels:
{{.Panels}}

Respond with strictly valid JSON of the shape:
{"proposals":[{"PanelUID":"<uid>","Title":"<short human title>"}]}`

// RationaleSystemPrompt frames the EnrichRationale request.
const RationaleSystemPrompt = `You are a Prometheus dashboard editor.
Your only job is to write a one-paragraph supplementary rationale for
each panel that explains, in plain English, why an operator would
care about this metric.

DO NOT generate PromQL queries; output only the requested fields.
The deterministic pipeline owns all queries.

The mechanical rationale sentence is always preserved by the caller as
an audit trail; your paragraph is appended, never substituted. Do not
repeat the mechanical sentence verbatim.

You must return strictly valid JSON of the shape:
{"proposals":[{"PanelUID":"<uid>","Paragraph":"<one short paragraph>"}]}

Do not include explanations, markdown, code fences, or any field
other than PanelUID and Paragraph.`

// RationaleUserTemplate frames the per-request payload for EnrichRationale.
const RationaleUserTemplate = `Write supplementary rationale paragraphs
for the following panels. Each entry shows: PanelUID, MechanicalTitle,
MetricName, Section, Rationale, and the read-only QueryExprs the
deterministic pipeline produced.

Constraints:
- DO NOT generate PromQL queries; output only the requested fields.
- Do not propose alternative queries; QueryExprs are read-only context.
- Do not invent metrics, labels, or numbers not in the input.
- Keep each Paragraph to one short paragraph (2-4 sentences).

Panels:
{{.Panels}}

Respond with strictly valid JSON of the shape:
{"proposals":[{"PanelUID":"<uid>","Paragraph":"<one short paragraph>"}]}`

// ClassifySystemPrompt frames the ClassifyUnknown request.
const ClassifySystemPrompt = `You are a Prometheus metrics classifier.
Your only job is to suggest trait names for metrics the deterministic
classifier could not place.

DO NOT generate PromQL queries; output only the requested fields.
The deterministic pipeline owns all queries.

Trait names are short snake_case identifiers like service_http,
runtime_go, or node_filesystem. They are advisory — an empty
proposal list is always a valid answer.

You must return strictly valid JSON of the shape:
{"proposals":[{"PanelUID":"<metric-name>","Hints":[{"Traits":["<trait>"],"Confidence":0.0}]}]}

Do not include explanations, markdown, code fences, or any field other
than PanelUID, Traits, and Confidence.`

// ClassifyUserTemplate frames the per-request payload for ClassifyUnknown.
const ClassifyUserTemplate = `Propose trait candidates for the following
unknown metrics. Each entry shows: MetricName, Type, Help, Unit, and
the sorted list of label NAMES the metric carries.

Constraints:
- DO NOT generate PromQL queries; output only the requested fields.
- Do not invent labels or label values; the input is the only source.
- Confidence is a float in 0 to 1; emit 0.0 for low-evidence proposals.
- An empty proposals list is valid when no metric is recognisable.

Metrics:
{{.Metrics}}

Respond with strictly valid JSON of the shape:
{"proposals":[{"PanelUID":"<metric-name>","Hints":[{"Traits":["<trait>"],"Confidence":0.0}]}]}`

// PromptHash returns the first 16 hex chars of SHA-256 over the
// canonical concatenation of all six template strings, joined by '|'.
// The output is stable across builds and changes byte-for-byte the
// moment any template constant changes — that's the cache-invalidation
// hook required by V0.2-PLAN §2.4.
func PromptHash() string {
	parts := []string{
		TitleSystemPrompt,
		TitleUserTemplate,
		RationaleSystemPrompt,
		RationaleUserTemplate,
		ClassifySystemPrompt,
		ClassifyUserTemplate,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])[:16]
}
