package enrich

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newOpenAIEnricherForTest builds an *OpenAIEnricher pointing at a
// caller-supplied endpoint. Same-package test helper only — never exposed
// through the factory or any production caller.
func newOpenAIEnricherForTest(t *testing.T, endpoint string) *OpenAIEnricher {
	t.Helper()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = "test-key"
	}
	return &OpenAIEnricher{
		apiKey:     apiKey,
		model:      openaiDefaultModel,
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// TestOpenAIEnricher_RedactionAtProxyBoundary is the load-bearing canary
// for V0.2-PLAN §2.5: NO label-value-shaped string supplied by the caller may
// ever reach the wire, in raw, URL-encoded, or base64-encoded form. The test
// is intentionally pessimistic — it captures every byte of every outbound
// HTTP request, then scans for synthetic secrets across multiple encodings.
//
// Phase 1 plants secrets in MetricBrief.Labels using the value-shaped form
// (`pod=pod-abc123`). ValidateBriefs is contracted (redaction.go) to refuse
// these BEFORE the enricher writes any bytes, so the captured buffer must
// remain empty for that call.
//
// Phase 2 issues clean calls across all three Enricher methods. The captured
// buffer must contain real bytes (proving the wire was exercised) but must
// not contain any secret string from Phase 1's input.
//
// PHASE 4 NOTE: this is the canary that drove the enricher implementation.
// Before ValidateBriefs was wired into ClassifyUnknown, Phase 1 would write
// the poisoned payload to the captured buffer; the test surfaces that
// regression as a clear "ValidateBriefs guard appears to be bypassed" fail.
func TestOpenAIEnricher_RedactionAtProxyBoundary(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	var (
		mu       sync.Mutex
		captured bytes.Buffer
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		captured.WriteString(r.Method + " " + r.URL.String() + "\n")
		for k, v := range r.Header {
			captured.WriteString(k + ": " + strings.Join(v, ",") + "\n")
		}
		body, _ := io.ReadAll(r.Body)
		captured.Write(body)
		captured.WriteString("\n---\n")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[]}"}}]}`)
	}))
	defer srv.Close()

	e := newOpenAIEnricherForTest(t, srv.URL)

	secrets := []string{"pod-abc123", "checkout-svc", "us-east-1"}

	// Phase 1: poisoned ClassifyUnknown — ValidateBriefs MUST refuse this
	// before any HTTP write. If the captured buffer grows during this call
	// the redaction guard has been bypassed.
	mu.Lock()
	preLen := captured.Len()
	mu.Unlock()
	poisoned := ClassifyInput{
		Metrics: []MetricBrief{{
			Name: "http_requests_total",
			Type: "counter",
			Labels: []string{
				"pod=pod-abc123",
				"service=checkout-svc",
				"region=us-east-1",
			},
		}},
	}
	if _, err := e.ClassifyUnknown(context.Background(), poisoned); err == nil {
		t.Fatal("CANARY: poisoned ClassifyUnknown returned nil error; ValidateBriefs guard appears to be bypassed")
	}
	mu.Lock()
	postLen := captured.Len()
	mu.Unlock()
	if postLen != preLen {
		t.Fatalf("CANARY: poisoned ClassifyUnknown wrote %d bytes outbound; ValidateBriefs must run BEFORE any HTTP write",
			postLen-preLen)
	}

	// Phase 2: clean calls. These exercise the wire so the canary scans
	// real outbound bytes (not just an empty buffer).
	clean := ClassifyInput{
		Metrics: []MetricBrief{{
			Name:   "http_requests_total",
			Type:   "counter",
			Help:   "Total HTTP requests",
			Labels: []string{"job", "method"},
		}},
	}
	if _, err := e.ClassifyUnknown(context.Background(), clean); err != nil {
		t.Fatalf("Phase 2 clean ClassifyUnknown returned error: %v", err)
	}
	titles := TitleInput{Requests: []PanelTitleRequest{{
		PanelUID:        "p1",
		MechanicalTitle: "rps",
		MetricName:      "http_requests_total",
		Section:         "traffic",
		Rationale:       "rps mechanical sentence",
	}}}
	if _, err := e.EnrichTitles(context.Background(), titles); err != nil {
		t.Fatalf("Phase 2 EnrichTitles returned error: %v", err)
	}
	rationale := RationaleInput{Requests: []PanelRationaleRequest{{
		PanelUID:        "p1",
		MechanicalTitle: "rps",
		MetricName:      "http_requests_total",
		Section:         "traffic",
		Rationale:       "rps mechanical sentence",
		QueryExprs:      []string{"sum(rate(http_requests_total[5m]))"},
	}}}
	if _, err := e.EnrichRationale(context.Background(), rationale); err != nil {
		t.Fatalf("Phase 2 EnrichRationale returned error: %v", err)
	}

	mu.Lock()
	capturedBytes := append([]byte(nil), captured.Bytes()...)
	mu.Unlock()

	if len(capturedBytes) == 0 {
		t.Fatal("CANARY: no HTTP traffic captured across the three Phase 2 calls; enricher did not exercise the wire")
	}

	for _, secret := range secrets {
		for _, encoded := range []string{
			secret,
			url.QueryEscape(secret),
			base64.StdEncoding.EncodeToString([]byte(secret)),
			base64.URLEncoding.EncodeToString([]byte(secret)),
		} {
			if bytes.Contains(capturedBytes, []byte(encoded)) {
				t.Errorf("CANARY: secret %q (encoded form %q) leaked into outbound HTTP traffic", secret, encoded)
			}
		}
	}
}

// TestOpenAIEnricher_RequiresAPIKey asserts the constructor refuses to
// build an enricher when OPENAI_API_KEY is unset, returning the typed
// sentinel ErrOpenAINoAPIKey so callers can distinguish "missing creds"
// from "registry doesn't know this name".
func TestOpenAIEnricher_RequiresAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	got, err := newOpenAIEnricher(Spec{Provider: "openai"})
	if got != nil {
		t.Errorf("newOpenAIEnricher returned non-nil enricher with no API key; want nil")
	}
	if err == nil {
		t.Fatal("newOpenAIEnricher returned nil error with no API key; want ErrOpenAINoAPIKey")
	}
	if !errors.Is(err, ErrOpenAINoAPIKey) {
		t.Errorf("error chain missing ErrOpenAINoAPIKey: %v", err)
	}
}

// TestOpenAIEnricher_NeverGeneratesPromQL pins the §2.2 boundary: even when
// the model ignores its instructions and emits a `query` field, the
// enricher must drop it AND log a warning. The strict typed parser is the
// first line of defense; openaiWarnIfPromQL is the second.
func TestOpenAIEnricher_NeverGeneratesPromQL(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Inner content carries a "query" field the enricher must refuse to surface.
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[{\"PanelUID\":\"p1\",\"Title\":\"Naughty title\",\"query\":\"sum(rate(http_requests_total[5m]))\"}]}"}}]}`)
	}))
	defer srv.Close()

	// Capture log output to verify the warn fired.
	var logBuf bytes.Buffer
	oldOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(oldOut)

	e := newOpenAIEnricherForTest(t, srv.URL)
	in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}

	out, err := e.EnrichTitles(context.Background(), in)
	if err != nil {
		t.Fatalf("EnrichTitles: %v", err)
	}

	// Returned proposals must carry only the title, never any PromQL.
	if len(out.Proposals) != 1 {
		t.Fatalf("got %d proposals, want 1", len(out.Proposals))
	}
	got := out.Proposals[0]
	for _, banned := range []string{"rate(", "histogram_quantile(", "sum by", "sum(rate"} {
		if strings.Contains(got.Title, banned) {
			t.Errorf("returned title %q contains forbidden PromQL fragment %q", got.Title, banned)
		}
	}

	// And a warning must have been logged so operators see the upstream's misbehavior.
	logged := logBuf.String()
	if !strings.Contains(logged, "openai") {
		t.Errorf("expected an 'openai' prefix in the discard warning; got %q", logged)
	}
	if !strings.Contains(logged, "query") || !strings.Contains(logged, "discarding") {
		t.Errorf("expected a 'discarding query field' warning in log output; got %q", logged)
	}
}

// TestOpenAIEnricher_RateLimitBackoff pins the single-retry behavior on
// HTTP 429: the enricher must retry once after a brief delay and surface
// the eventual success (or failure) to the caller.
func TestOpenAIEnricher_RateLimitBackoff(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[{\"PanelUID\":\"p1\",\"Title\":\"After backoff\"}]}"}}]}`)
	}))
	defer srv.Close()

	e := newOpenAIEnricherForTest(t, srv.URL)
	in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}

	start := time.Now()
	out, err := e.EnrichTitles(context.Background(), in)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("EnrichTitles after retry: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected 2 HTTP calls (one 429 + one 200); got %d", got)
	}
	if elapsed < openaiRetryDelay {
		t.Errorf("retry happened in %v; expected at least %v of backoff", elapsed, openaiRetryDelay)
	}
	if len(out.Proposals) != 1 || out.Proposals[0].Title != "After backoff" {
		t.Errorf("expected one proposal with title 'After backoff'; got %+v", out.Proposals)
	}
}

// TestOpenAIEnricher_ClassifyUnknown_CacheHit pins the V0.2-PLAN §2.4
// caching contract: a second call with semantically identical input must
// be served from disk cache without issuing a second HTTP request. The
// cache key includes PromptHash() so any prompt-template byte change
// invalidates every prior entry.
func TestOpenAIEnricher_ClassifyUnknown_CacheHit(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[{\"PanelUID\":\"weird_metric\",\"Hints\":[{\"Traits\":[\"service_http\"],\"Confidence\":0.7}]}]}"}}]}`)
	}))
	defer srv.Close()

	e := newOpenAIEnricherForTest(t, srv.URL)
	e.cache = NewCache(t.TempDir())

	in := ClassifyInput{Metrics: []MetricBrief{{Name: "weird_metric", Type: "counter"}}}

	// First call: cold cache ⇒ HTTP roundtrip happens.
	first, err := e.ClassifyUnknown(context.Background(), in)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("first call issued %d HTTP calls; want 1", got)
	}
	if len(first.Hints) != 1 || first.Hints[0].Metric != "weird_metric" {
		t.Fatalf("first call returned unexpected output: %+v", first)
	}

	// Second call with identical input: must hit cache, not network.
	second, err := e.ClassifyUnknown(context.Background(), in)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("second call issued total %d HTTP calls; cache should have absorbed it (want 1)", got)
	}
	if len(second.Hints) != 1 || second.Hints[0].Metric != "weird_metric" {
		t.Errorf("cached output diverged from first: %+v vs %+v", second, first)
	}
}
