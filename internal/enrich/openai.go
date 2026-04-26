// OpenAI Chat Completions API backend for the v0.2 enrichment seam.
//
// This file ships in v0.2 Phase 4. It mirrors internal/enrich/anthropic.go's
// shape — option-pattern config, eager API-key check at construction,
// ValidateBriefs before any HTTP write, cache wrapper using PromptHash,
// response-order-by-PanelUID. The whole point of Phase 4 is to prove that
// anthropic.go's shape generalizes; if shipping this provider needed more
// than ONE NEW FILE plus the existing shared helpers, the registry contract
// would be wrong.
//
// The redaction contract from V0.2-PLAN §2.5 is enforced at one point —
// every code path that issues an outbound HTTP write first runs
// ValidateBriefs against the MetricBrief slice it is about to serialize.
// The proxy-capture canary in openai_test.go pins this guard.
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
	openaiDefaultModel    = "gpt-5"
	openaiDefaultEndpoint = "https://api.openai.com/v1/chat/completions"
	openaiMaxTokens       = 1024
	openaiHTTPTimeout     = 30 * time.Second

	// One retry on 429/5xx is the smallest backoff that satisfies the
	// "RateLimitBackoff" contract without making tests slow. The delay is
	// short enough that it never measurably impacts test runtime.
	openaiMaxRetries = 1
	openaiRetryDelay = 10 * time.Millisecond
)

// ErrOpenAINoAPIKey is returned by the constructor when OPENAI_API_KEY is
// unset. Callers can distinguish this from registry errors with errors.Is —
// it is intentionally NOT wrapped with ErrNotImplementedYet because the
// provider IS implemented; the operator simply has not configured credentials.
var ErrOpenAINoAPIKey = errors.New("enrich: OPENAI_API_KEY not set; required for the openai provider")

// OpenAIEnricher is a hosted-AI Enricher backed by the OpenAI Chat
// Completions API. All fields are unexported. Tests substitute the endpoint
// via the same-package helper newOpenAIEnricherForTest in openai_test.go.
type OpenAIEnricher struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
	cache      *Cache // nil ⇒ caching disabled (Spec.NoCache or empty CacheDir)
}

// newOpenAIEnricher constructs an Enricher from a Spec. Registered at
// init() time as the "openai" provider in a later commit; for now this
// constructor is callable only from same-package tests.
func newOpenAIEnricher(spec Spec) (Enricher, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, ErrOpenAINoAPIKey
	}
	model := spec.Model
	if model == "" {
		model = openaiDefaultModel
	}
	e := &OpenAIEnricher{
		apiKey:     apiKey,
		model:      model,
		endpoint:   openaiDefaultEndpoint,
		httpClient: &http.Client{Timeout: openaiHTTPTimeout},
	}
	if !spec.NoCache && spec.CacheDir != "" {
		e.cache = NewCache(spec.CacheDir)
	}
	return e, nil
}

// Describe identifies the provider for audit trails and cache keys.
func (e *OpenAIEnricher) Describe() Description {
	return Description{Provider: "openai", Model: e.model}
}

// ----- OpenAI /v1/chat/completions wire envelope --------------------------

type openaiReq struct {
	Model               string      `json:"model"`
	MaxCompletionTokens int         `json:"max_completion_tokens"`
	Messages            []openaiMsg `json:"messages"`
}

type openaiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResp struct {
	Choices []openaiChoice `json:"choices"`
}

type openaiChoice struct {
	Message openaiMsg `json:"message"`
}

// callOpenAI issues a /v1/chat/completions POST and returns the concatenated
// content from all message choices. Failure semantics match V0.2-PLAN §2.5
// and mirror callAnthropic exactly:
//
//   - Envelope parse failure → ("", nil) so the per-method caller falls back
//     deterministically. This is the load-bearing "AI mush ⇒ deterministic
//     output unchanged" path.
//   - Network error / context cancel / non-200 non-transient → ("", err) so
//     the caller surfaces the failure.
//   - 429 / 5xx → one short retry; if the retry also fails, returns the last
//     transient error so the caller surfaces it.
func (e *OpenAIEnricher) callOpenAI(ctx context.Context, system, user string) (string, error) {
	body := openaiReq{
		Model:               e.model,
		MaxCompletionTokens: openaiMaxTokens,
		Messages: []openaiMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("openai: marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= openaiMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(openaiRetryDelay):
			}
		}
		text, transient, err := e.doOneOpenAI(ctx, raw)
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

// doOneOpenAI is one HTTP roundtrip. The transient bool tells callOpenAI
// whether the failure is retry-eligible (429 / 5xx). Envelope parse failures
// return ("", false, nil) so the caller surfaces a deterministic-fallback
// signal — these are NOT retried.
func (e *OpenAIEnricher) doOneOpenAI(ctx context.Context, raw []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", false, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("openai: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		return "", false, fmt.Errorf("openai: read response: %w", rerr)
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", true, fmt.Errorf("openai: provider transient (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("openai: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed openaiResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		// Envelope parse failure → deterministic fallback (empty text + nil).
		return "", false, nil
	}
	var sb strings.Builder
	for _, c := range parsed.Choices {
		sb.WriteString(c.Message.Content)
	}
	return sb.String(), false, nil
}

// cachedCallOpenAI wraps callOpenAI with optional disk-backed caching. The
// cache key incorporates PromptHash() so any byte change to the canonical
// prompt templates invalidates every prior entry, plus the JSON hash of the
// function-specific input so semantically-identical calls collapse to a
// single roundtrip. When e.cache is nil the call is a straight passthrough.
func (e *OpenAIEnricher) cachedCallOpenAI(ctx context.Context, function, system, user string, inputForHash any) (string, error) {
	if e.cache == nil {
		return e.callOpenAI(ctx, system, user)
	}
	raw, err := json.Marshal(inputForHash)
	if err != nil {
		// Hash failure shouldn't kill the call; fall through to the network.
		return e.callOpenAI(ctx, system, user)
	}
	sum := sha256.Sum256(raw)
	key := CacheKey{
		InventoryHash:  hex.EncodeToString(sum[:])[:16],
		Function:       function,
		ProviderID:     "openai:" + e.model,
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
	text, err := e.callOpenAI(ctx, system, user)
	if err == nil {
		_ = e.cache.Put(key, text) // best-effort; cache write failures must not break the call
	}
	return text, err
}

// openaiWarnIfPromQL inspects raw response text for any "query" field the
// model might have included against the prompt instructions. The presence
// of such a field is logged at warn level and the field is dropped by the
// strict parser anyway — this is the V0.2-PLAN §2.2 boundary, defended in
// depth. Mirrors anthropic.go's warnIfPromQL with the openai prefix; we
// duplicate rather than share the helper because anthropic.go is locked
// for this phase (and the prefix is the only thing that differs).
func openaiWarnIfPromQL(method, text string) {
	if strings.Contains(text, `"query"`) || strings.Contains(text, `"Query"`) {
		log.Printf("openai: %s response contained a 'query' field; discarding (PromQL is owned by the deterministic pipeline)",
			method)
	}
}

// ----- ClassifyUnknown ----------------------------------------------------

// ClassifyUnknown runs ValidateBriefs first (V0.2-PLAN §2.5 redaction
// contract), then issues the request. An empty input is a no-op — the
// "OnlyFiresForUnknownMetrics" contract: the enricher never writes a
// request when there are no metrics to classify.
func (e *OpenAIEnricher) ClassifyUnknown(ctx context.Context, in ClassifyInput) (ClassifyOutput, error) {
	if len(in.Metrics) == 0 {
		return ClassifyOutput{}, nil
	}
	if err := ValidateBriefs(in.Metrics); err != nil {
		return ClassifyOutput{}, err
	}

	payload, err := json.Marshal(in.Metrics)
	if err != nil {
		return ClassifyOutput{}, fmt.Errorf("openai: marshal classify payload: %w", err)
	}
	user := renderUser(ClassifyUserTemplate, "Metrics", string(payload))

	text, err := e.cachedCallOpenAI(ctx, "classify_unknown", ClassifySystemPrompt, user, in.Metrics)
	if err != nil {
		return ClassifyOutput{}, err
	}
	if text == "" {
		return ClassifyOutput{}, nil
	}
	openaiWarnIfPromQL("ClassifyUnknown", text)

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

// EnrichTitles requests human-scannable titles for the supplied panels and
// returns them in REQUEST order. The strict parser silently drops any
// `query` field the model attempts to emit; openaiWarnIfPromQL surfaces
// the attempt to operators.
func (e *OpenAIEnricher) EnrichTitles(ctx context.Context, in TitleInput) (TitleOutput, error) {
	if len(in.Requests) == 0 {
		return TitleOutput{}, nil
	}
	payload, err := json.Marshal(in.Requests)
	if err != nil {
		return TitleOutput{}, fmt.Errorf("openai: marshal titles payload: %w", err)
	}
	user := renderUser(TitleUserTemplate, "Panels", string(payload))

	text, err := e.cachedCallOpenAI(ctx, "enrich_titles", TitleSystemPrompt, user, in.Requests)
	if err != nil {
		return TitleOutput{}, err
	}
	if text == "" {
		return TitleOutput{}, nil
	}
	openaiWarnIfPromQL("EnrichTitles", text)

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

// EnrichRationale requests supplementary rationale paragraphs for the
// supplied panels and returns them in REQUEST order.
func (e *OpenAIEnricher) EnrichRationale(ctx context.Context, in RationaleInput) (RationaleOutput, error) {
	if len(in.Requests) == 0 {
		return RationaleOutput{}, nil
	}
	payload, err := json.Marshal(in.Requests)
	if err != nil {
		return RationaleOutput{}, fmt.Errorf("openai: marshal rationale payload: %w", err)
	}
	user := renderUser(RationaleUserTemplate, "Panels", string(payload))

	text, err := e.cachedCallOpenAI(ctx, "enrich_rationale", RationaleSystemPrompt, user, in.Requests)
	if err != nil {
		return RationaleOutput{}, err
	}
	if text == "" {
		return RationaleOutput{}, nil
	}
	openaiWarnIfPromQL("EnrichRationale", text)

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
