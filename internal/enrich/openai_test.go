package enrich

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
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
