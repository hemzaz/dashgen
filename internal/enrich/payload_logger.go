// Pre-send payload-preview logging for the v0.2 enrichment seam.
//
// This file ships with v0.2 Phase 4 Step 5.1. It defines an optional
// callback that hosted-AI enrichers (anthropic, openai) invoke right
// before every outbound HTTP write. The callback is wired by the cmd
// layer when the operator passes the hidden `--log-enrichment-payloads`
// flag and is otherwise nil — when nil the enricher does not invoke it
// and pays zero overhead (guarded by `if logger != nil`).
//
// REDACTION-SAFETY INVARIANT (V0.2-PLAN §2.5):
// The "preview" passed to the logger is always derived from the wire
// bytes the enricher is about to send. ValidateBriefs runs FIRST against
// caller input; the request body is marshalled AFTER it succeeds; the
// preview is a substring of those marshalled wire bytes. Anything the
// redaction guard caught therefore can never reach the logger. This is
// what TestLogEnrichmentPayloads_NeverLogsLabelValues pins.
package enrich

// PayloadLogger is the optional pre-send callback. fnName is the
// enricher-internal function identifier (e.g. "enrich_titles");
// byteCount is len(wire); preview is operator-visible debug text safe
// for stderr — built from wire bytes, never from pre-redaction input.
type PayloadLogger func(fnName string, byteCount int, preview string)

// PayloadLoggerSetter is the optional interface implemented by enrichers
// that issue HTTP traffic and therefore have a "before-send" point at
// which to invoke a logger. The factory does not require it; only the
// hosted providers (Anthropic, OpenAI) implement it.
//
// Callers (cmd/dashgen via internal/app/generate.applyEnrichment) type-
// assert the constructed Enricher onto this interface and install a
// callback when --log-enrichment-payloads is on. Providers that do not
// implement it are silently skipped — the noop path never reaches this
// branch and a future local provider can opt in by adding a setter.
type PayloadLoggerSetter interface {
	SetPayloadLogger(PayloadLogger)
}

// payloadPreview returns a bounded, redaction-safe preview of the given
// wire bytes. The preview includes the head and tail of the wire body
// with an elision in between. Both halves come from the same []byte the
// enricher hands to http.Client.Do, so anything ValidateBriefs would
// have rejected cannot appear here by construction.
//
// The function deliberately takes a []byte (not a string) so callers
// cannot mistakenly pass a pre-redaction caller input.
func payloadPreview(raw []byte) string {
	const head = 96
	const tail = 96
	if len(raw) <= head+tail {
		return string(raw)
	}
	return string(raw[:head]) + "..." + string(raw[len(raw)-tail:])
}
