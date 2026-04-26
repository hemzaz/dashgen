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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	anthropicDefaultModel    = "claude-opus-4-7"
	anthropicDefaultEndpoint = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion      = "2023-06-01"
	anthropicMaxTokens       = 1024
	anthropicHTTPTimeout     = 30 * time.Second

	// One retry on 429/5xx is the smallest backoff that satisfies the
	// "RateLimitBackoff" contract without making tests slow. The delay is
	// short enough that it never measurably impacts test runtime.
	anthropicMaxRetries = 1
	anthropicRetryDelay = 10 * time.Millisecond
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
	cache      *Cache // nil ⇒ caching disabled (Spec.NoCache or empty CacheDir)
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
	e := &AnthropicEnricher{
		apiKey:     apiKey,
		model:      model,
		endpoint:   anthropicDefaultEndpoint,
		httpClient: &http.Client{Timeout: anthropicHTTPTimeout},
	}
	if !spec.NoCache && spec.CacheDir != "" {
		e.cache = NewCache(spec.CacheDir)
	}
	return e, nil
}

// Describe identifies the provider for audit trails and cache keys.
func (e *AnthropicEnricher) Describe() Description {
	return Description{Provider: "anthropic", Model: e.model}
}

// ----- Anthropic /v1/messages wire envelope -------------------------------

type anthropicReq struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system"`
	Messages  []anthropicMsg `json:"messages"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callAnthropic issues a /v1/messages POST and returns the concatenated text
// from all "text" content blocks. Failure semantics match V0.2-PLAN §2.5:
//
//   - Envelope parse failure → ("", nil) so the per-method caller falls back
//     deterministically. This is the load-bearing "AI mush ⇒ deterministic
//     output unchanged" path.
//   - Network error / context cancel / non-200 non-transient → ("", err)
//     so the caller surfaces the failure.
//   - 429 / 5xx → one short retry; if the retry also fails, returns the
//     last transient error so the caller surfaces it.
func (e *AnthropicEnricher) callAnthropic(ctx context.Context, system, user string) (string, error) {
	body := anthropicReq{
		Model:     e.model,
		MaxTokens: anthropicMaxTokens,
		System:    system,
		Messages:  []anthropicMsg{{Role: "user", Content: user}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= anthropicMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(anthropicRetryDelay):
			}
		}
		text, transient, err := e.doOne(ctx, raw)
		if err == nil {
			return text, nil
		}
		if !transient {
			return "", err
		}
		lastErr = err
	}
	return "", lastErr
}

// doOne is one HTTP roundtrip. The transient bool tells callAnthropic whether
// the failure is retry-eligible (429 / 5xx). Envelope parse failures return
// ("", false, nil) so the caller surfaces a deterministic-fallback signal —
// these are NOT retried.
func (e *AnthropicEnricher) doOne(ctx context.Context, raw []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", false, fmt.Errorf("anthropic: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", e.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		return "", false, fmt.Errorf("anthropic: read response: %w", rerr)
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", true, fmt.Errorf("anthropic: provider transient (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("anthropic: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed anthropicResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		// Envelope parse failure → deterministic fallback (empty text + nil).
		return "", false, nil
	}
	var sb strings.Builder
	for _, b := range parsed.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String(), false, nil
}

// renderUser substitutes a single placeholder of the form {{.Key}} with the
// value string. The templates in prompts.go each have exactly one such
// placeholder; using strings.Replace instead of text/template keeps the
// substitution surface minimal and avoids template-injection concerns when
// the value is a JSON document derived from caller input.
func renderUser(tplStr, key, value string) string {
	return strings.Replace(tplStr, "{{."+key+"}}", value, 1)
}

// cachedCall wraps callAnthropic with optional disk-backed caching. The
// cache key incorporates PromptHash() so any byte change to the canonical
// prompt templates invalidates every prior entry, plus the JSON hash of
// the function-specific input so semantically-identical calls collapse to
// a single roundtrip. When e.cache is nil the call is a straight passthrough.
func (e *AnthropicEnricher) cachedCall(ctx context.Context, function, system, user string, inputForHash any) (string, error) {
	if e.cache == nil {
		return e.callAnthropic(ctx, system, user)
	}
	raw, err := json.Marshal(inputForHash)
	if err != nil {
		// Hash failure shouldn't kill the call; fall through to the network.
		return e.callAnthropic(ctx, system, user)
	}
	sum := sha256.Sum256(raw)
	key := CacheKey{
		InventoryHash:  hex.EncodeToString(sum[:])[:16],
		Function:       function,
		ProviderID:     "anthropic:" + e.model,
		PromptHash:     PromptHash(),
		DashgenVersion: "dev",
	}
	if entry, ok, err := e.cache.Get(key); err == nil && ok {
		var cached string
		if jerr := json.Unmarshal(entry.Value, &cached); jerr == nil {
			return cached, nil
		}
		// Malformed cache value ⇒ fall through and overwrite on success.
	}
	text, err := e.callAnthropic(ctx, system, user)
	if err == nil {
		_ = e.cache.Put(key, text) // best-effort; cache write failures must not break the call
	}
	return text, err
}

// warnIfPromQL inspects raw response text for any "query" field the model
// might have included against the prompt instructions. The presence of such
// a field is logged at warn level and the field is dropped by the strict
// parser anyway — this is the V0.2-PLAN §2.2 boundary, defended in depth.
func warnIfPromQL(method, text string) {
	if strings.Contains(text, `"query"`) || strings.Contains(text, `"Query"`) {
		log.Printf("anthropic: %s response contained a 'query' field; discarding (PromQL is owned by the deterministic pipeline)",
			method)
	}
}

// ----- ClassifyUnknown ----------------------------------------------------

type classifyHintWire struct {
	Traits     []string `json:"Traits"`
	Confidence float64  `json:"Confidence"`
}

type classifyProposalWire struct {
	PanelUID string             `json:"PanelUID"` // metric name in the classify shape
	Hints    []classifyHintWire `json:"Hints"`
}

type classifyEnvelopeWire struct {
	Proposals []classifyProposalWire `json:"proposals"`
}

// ClassifyUnknown runs ValidateBriefs first (V0.2-PLAN §2.5 redaction
// contract), then issues the request. An empty input is a no-op — the
// "OnlyFiresForUnknownMetrics" contract: the enricher never writes a
// request when there are no metrics to classify.
func (e *AnthropicEnricher) ClassifyUnknown(ctx context.Context, in ClassifyInput) (ClassifyOutput, error) {
	if len(in.Metrics) == 0 {
		return ClassifyOutput{}, nil
	}
	if err := ValidateBriefs(in.Metrics); err != nil {
		return ClassifyOutput{}, err
	}

	payload, err := json.Marshal(in.Metrics)
	if err != nil {
		return ClassifyOutput{}, fmt.Errorf("anthropic: marshal classify payload: %w", err)
	}
	user := renderUser(ClassifyUserTemplate, "Metrics", string(payload))

	text, err := e.cachedCall(ctx, "classify_unknown", ClassifySystemPrompt, user, in.Metrics)
	if err != nil {
		return ClassifyOutput{}, err
	}
	if text == "" {
		return ClassifyOutput{}, nil
	}
	warnIfPromQL("ClassifyUnknown", text)

	var env classifyEnvelopeWire
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		// Inner-text parse failure → deterministic fallback.
		return ClassifyOutput{}, nil
	}

	// Pin proposals to request order. The classify shape uses metric NAME
	// as the join key (it's stable and unique within an inventory).
	hintsByMetric := make(map[string][]classifyHintWire, len(env.Proposals))
	for _, p := range env.Proposals {
		hintsByMetric[p.PanelUID] = p.Hints
	}
	out := ClassifyOutput{}
	for _, m := range in.Metrics {
		hints := hintsByMetric[m.Name]
		for _, h := range hints {
			if len(h.Traits) == 0 {
				continue
			}
			out.Hints = append(out.Hints, TraitHint{
				Metric:     m.Name,
				Traits:     h.Traits,
				Confidence: h.Confidence,
			})
		}
	}
	return out, nil
}

// ----- EnrichTitles -------------------------------------------------------

type titleProposalWire struct {
	PanelUID string `json:"PanelUID"`
	Title    string `json:"Title"`
}

type titleEnvelopeWire struct {
	Proposals []titleProposalWire `json:"proposals"`
}

// EnrichTitles requests human-scannable titles for the supplied panels and
// returns them in REQUEST order. The strict parser silently drops any
// `query` field the model attempts to emit; warnIfPromQL surfaces the
// attempt to operators.
func (e *AnthropicEnricher) EnrichTitles(ctx context.Context, in TitleInput) (TitleOutput, error) {
	if len(in.Requests) == 0 {
		return TitleOutput{}, nil
	}
	payload, err := json.Marshal(in.Requests)
	if err != nil {
		return TitleOutput{}, fmt.Errorf("anthropic: marshal titles payload: %w", err)
	}
	user := renderUser(TitleUserTemplate, "Panels", string(payload))

	text, err := e.cachedCall(ctx, "enrich_titles", TitleSystemPrompt, user, in.Requests)
	if err != nil {
		return TitleOutput{}, err
	}
	if text == "" {
		return TitleOutput{}, nil
	}
	warnIfPromQL("EnrichTitles", text)

	var env titleEnvelopeWire
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		return TitleOutput{}, nil
	}

	titlesByUID := make(map[string]string, len(env.Proposals))
	for _, p := range env.Proposals {
		titlesByUID[p.PanelUID] = p.Title
	}
	out := TitleOutput{}
	for _, req := range in.Requests {
		if t, ok := titlesByUID[req.PanelUID]; ok && t != "" {
			out.Proposals = append(out.Proposals, PanelTitleProposal{
				PanelUID: req.PanelUID,
				Title:    t,
			})
		}
	}
	return out, nil
}

// ----- EnrichRationale ----------------------------------------------------

type rationaleProposalWire struct {
	PanelUID  string `json:"PanelUID"`
	Paragraph string `json:"Paragraph"`
}

type rationaleEnvelopeWire struct {
	Proposals []rationaleProposalWire `json:"proposals"`
}

// init replaces the placeholder anthropic constructor (registered in
// factory.go's init) with the real one. Last-init-wins is the documented
// substitution contract — see TestRegister_LastInitWinsForTestSubstitution
// in factory_test.go.
func init() {
	Register("anthropic", newAnthropicEnricher)
}

// EnrichRationale requests supplementary rationale paragraphs for the
// supplied panels and returns them in REQUEST order.
func (e *AnthropicEnricher) EnrichRationale(ctx context.Context, in RationaleInput) (RationaleOutput, error) {
	if len(in.Requests) == 0 {
		return RationaleOutput{}, nil
	}
	payload, err := json.Marshal(in.Requests)
	if err != nil {
		return RationaleOutput{}, fmt.Errorf("anthropic: marshal rationale payload: %w", err)
	}
	user := renderUser(RationaleUserTemplate, "Panels", string(payload))

	text, err := e.cachedCall(ctx, "enrich_rationale", RationaleSystemPrompt, user, in.Requests)
	if err != nil {
		return RationaleOutput{}, err
	}
	if text == "" {
		return RationaleOutput{}, nil
	}
	warnIfPromQL("EnrichRationale", text)

	var env rationaleEnvelopeWire
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		return RationaleOutput{}, nil
	}

	paraByUID := make(map[string]string, len(env.Proposals))
	for _, p := range env.Proposals {
		paraByUID[p.PanelUID] = p.Paragraph
	}
	out := RationaleOutput{}
	for _, req := range in.Requests {
		if p, ok := paraByUID[req.PanelUID]; ok && p != "" {
			out.Proposals = append(out.Proposals, PanelRationaleProposal{
				PanelUID:  req.PanelUID,
				Paragraph: p,
			})
		}
	}
	return out, nil
}
