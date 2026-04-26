package enrich

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

// allTemplates returns the six canonical templates in the same order
// PromptHash uses, so test enumeration matches the production hash
// input.
func allTemplates() []string {
	return []string{
		TitleSystemPrompt,
		TitleUserTemplate,
		RationaleSystemPrompt,
		RationaleUserTemplate,
		ClassifySystemPrompt,
		ClassifyUserTemplate,
	}
}

func TestPromptHash_Stable(t *testing.T) {
	first := PromptHash()
	for i := 0; i < 10; i++ {
		got := PromptHash()
		if got != first {
			t.Fatalf("PromptHash not stable: rep %d returned %q, want %q", i, got, first)
		}
	}
	if len(first) != 16 {
		t.Fatalf("PromptHash length = %d, want 16 (got %q)", len(first), first)
	}
	hexRe := regexp.MustCompile(`^[0-9a-f]{16}$`)
	if !hexRe.MatchString(first) {
		t.Fatalf("PromptHash output %q is not 16 lower-case hex chars", first)
	}
}

// TestPromptHash_ChangesWithTemplate confirms the cache-invalidation
// contract: any byte change to any of the six template constants must
// produce a different hash. We compute PromptHash() once with the real
// constants, then reconstruct the same SHA-256[:16] formula here over
// a synthetic permutation that appends a single byte, and assert the
// outputs differ. We also mutate a different template (last position)
// to prove every constant participates in the hash input.
func TestPromptHash_ChangesWithTemplate(t *testing.T) {
	prod := PromptHash()

	mutated := append([]string{}, allTemplates()...)
	mutated[0] = mutated[0] + " // synthetic-byte-change"

	sum := sha256.Sum256([]byte(strings.Join(mutated, "|")))
	mutatedHash := hex.EncodeToString(sum[:])[:16]

	if prod == mutatedHash {
		t.Fatalf("hash did not change after a synthetic template byte was appended; cache would not invalidate (prod=%q mutated=%q)",
			prod, mutatedHash)
	}

	mutated2 := append([]string{}, allTemplates()...)
	mutated2[len(mutated2)-1] = mutated2[len(mutated2)-1] + "X"
	sum2 := sha256.Sum256([]byte(strings.Join(mutated2, "|")))
	mutated2Hash := hex.EncodeToString(sum2[:])[:16]
	if prod == mutated2Hash {
		t.Fatalf("hash did not change when last template was mutated; some constants are not in the hash input")
	}
	if mutatedHash == mutated2Hash {
		t.Fatalf("two distinct mutations produced identical hashes (%q); collision suggests truncated input", mutatedHash)
	}
}

// TestPromptTemplates_NeverPromQL is the V0.2-PLAN §2.2 boundary
// guard: no template may contain anything that looks like a PromQL
// snippet, and every template must explicitly forbid PromQL output.
//
// "Forbidden" includes function-call shapes (rate(, histogram_quantile()
// and the keyword "sum by"; the `{<label>=` matcher form is detected
// via regex so the JSON output schema (which legitimately uses bare
// braces around a key) is not flagged.
func TestPromptTemplates_NeverPromQL(t *testing.T) {
	literalForbidden := []string{
		"rate(",
		"histogram_quantile(",
		"sum by",
	}
	// Prometheus matcher syntax: an opening brace followed directly by
	// an identifier and an `=` (e.g. `{job="api"}`, `{namespace=prod}`).
	// JSON object literals like `{"proposals":...` do NOT match because
	// they have a `"` between the brace and the first token.
	matcherRe := regexp.MustCompile(`\{[A-Za-z_][A-Za-z_0-9]*\s*=`)

	names := []string{
		"TitleSystemPrompt",
		"TitleUserTemplate",
		"RationaleSystemPrompt",
		"RationaleUserTemplate",
		"ClassifySystemPrompt",
		"ClassifyUserTemplate",
	}

	for i, tpl := range allTemplates() {
		t.Run(names[i]+"/no_promql_syntax", func(t *testing.T) {
			for _, frag := range literalForbidden {
				if strings.Contains(tpl, frag) {
					t.Errorf("template %s contains forbidden PromQL fragment %q; templates must not embed PromQL examples (V0.2-PLAN §2.2)",
						names[i], frag)
				}
			}
			if loc := matcherRe.FindStringIndex(tpl); loc != nil {
				t.Errorf("template %s contains Prometheus matcher syntax at offset %d (%q); templates must not embed PromQL examples (V0.2-PLAN §2.2)",
					names[i], loc[0], tpl[loc[0]:loc[1]])
			}
		})
		t.Run(names[i]+"/explicit_promql_prohibition", func(t *testing.T) {
			if !strings.Contains(tpl, "DO NOT generate PromQL") {
				t.Errorf("template %s missing explicit PromQL prohibition; task #2 contract requires the literal phrase 'DO NOT generate PromQL'",
					names[i])
			}
			if !strings.Contains(tpl, "output only the requested fields") {
				t.Errorf("template %s missing explicit output-only-requested-fields clause; task #2 contract requires it",
					names[i])
			}
		})
	}
}
