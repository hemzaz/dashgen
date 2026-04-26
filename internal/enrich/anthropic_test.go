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

// newAnthropicEnricherForTest builds an *AnthropicEnricher pointing at a
// caller-supplied endpoint. Same-package test helper only — never exposed
// through the factory or any production caller.
func newAnthropicEnricherForTest(t *testing.T, endpoint string) *AnthropicEnricher {
	t.Helper()
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = "test-key"
	}
	return &AnthropicEnricher{
		apiKey:     apiKey,
		model:      anthropicDefaultModel,
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// TestAnthropicEnricher_RedactionAtProxyBoundary is the load-bearing canary
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
func TestAnthropicEnricher_RedactionAtProxyBoundary(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

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
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[]}"}]}`)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)

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

// TestAnthropicEnricher_RegistersOverridesPlaceholder pins the
// last-init-wins override: after anthropic.go's init() runs, calling the
// factory with Provider="anthropic" must return the real *AnthropicEnricher
// (not the placeholder ErrNotImplementedYet stub from factory.go). With
// no API key set the override surfaces ErrAnthropicNoAPIKey — distinct
// from ErrNotImplementedYet — proving the registration is live.
func TestAnthropicEnricher_RegistersOverridesPlaceholder(t *testing.T) {
	t.Run("with_api_key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		got, err := New(Spec{Provider: "anthropic"})
		if err != nil {
			t.Fatalf("New(anthropic) with API key: %v", err)
		}
		if errors.Is(err, ErrNotImplementedYet) {
			t.Errorf("placeholder still wired; expected real constructor")
		}
		if _, ok := got.(*AnthropicEnricher); !ok {
			t.Errorf("got %T; want *AnthropicEnricher", got)
		}
		desc := got.Describe()
		if desc.Provider != "anthropic" {
			t.Errorf("Describe().Provider = %q; want %q", desc.Provider, "anthropic")
		}
		if desc.Offline {
			t.Errorf("Describe().Offline = true; anthropic is network-bound")
		}
	})
	t.Run("without_api_key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		got, err := New(Spec{Provider: "anthropic"})
		if got != nil {
			t.Errorf("New(anthropic) without API key returned non-nil enricher; want nil")
		}
		if err == nil {
			t.Fatal("New(anthropic) without API key returned nil error")
		}
		if !errors.Is(err, ErrAnthropicNoAPIKey) {
			t.Errorf("error chain missing ErrAnthropicNoAPIKey: %v", err)
		}
		if errors.Is(err, ErrNotImplementedYet) {
			t.Errorf("real constructor leaked ErrNotImplementedYet; placeholder still wins")
		}
	})
}

// TestAnthropicEnricher_RequiresAPIKey asserts the constructor refuses to
// build an enricher when ANTHROPIC_API_KEY is unset, returning the typed
// sentinel ErrAnthropicNoAPIKey so callers can distinguish "missing creds"
// from "registry doesn't know this name".
func TestAnthropicEnricher_RequiresAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	got, err := newAnthropicEnricher(Spec{Provider: "anthropic"})
	if got != nil {
		t.Errorf("newAnthropicEnricher returned non-nil enricher with no API key; want nil")
	}
	if err == nil {
		t.Fatal("newAnthropicEnricher returned nil error with no API key; want ErrAnthropicNoAPIKey")
	}
	if !errors.Is(err, ErrAnthropicNoAPIKey) {
		t.Errorf("error chain missing ErrAnthropicNoAPIKey: %v", err)
	}
}

// TestAnthropicEnricher_EnrichTitles_Shape pins the structural happy path:
// given valid request inputs and a server that returns valid proposals, the
// enricher returns a TitleOutput whose proposals carry both PanelUID and
// Title fields populated.
func TestAnthropicEnricher_EnrichTitles_Shape(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[{\"PanelUID\":\"p1\",\"Title\":\"API request rate\"},{\"PanelUID\":\"p2\",\"Title\":\"Error ratio\"}]}"}]}`)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)
	in := TitleInput{Requests: []PanelTitleRequest{
		{PanelUID: "p1", MechanicalTitle: "rps", MetricName: "http_requests_total", Section: "traffic"},
		{PanelUID: "p2", MechanicalTitle: "err ratio", MetricName: "http_errors_total", Section: "errors"},
	}}

	out, err := e.EnrichTitles(context.Background(), in)
	if err != nil {
		t.Fatalf("EnrichTitles: %v", err)
	}
	if len(out.Proposals) != 2 {
		t.Fatalf("got %d proposals, want 2", len(out.Proposals))
	}
	for _, p := range out.Proposals {
		if p.PanelUID == "" {
			t.Errorf("proposal has empty PanelUID: %+v", p)
		}
		if p.Title == "" {
			t.Errorf("proposal has empty Title: %+v", p)
		}
	}
}

// TestAnthropicEnricher_EnrichTitles_MalformedResponseFallsBack pins the
// "AI mush ⇒ deterministic output unchanged" contract. When the server
// returns a malformed envelope or unparseable inner text the enricher must
// return (zero, nil) so the caller can fall back to the mechanical title.
func TestAnthropicEnricher_EnrichTitles_MalformedResponseFallsBack(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	cases := []struct {
		name string
		body string
	}{
		{
			name: "malformed_envelope",
			body: `not even json {{{`,
		},
		{
			name: "envelope_ok_inner_text_garbage",
			body: `{"content":[{"type":"text","text":"absolutely not json {{{"}]}`,
		},
		{
			name: "envelope_ok_inner_missing_proposals_key",
			body: `{"content":[{"type":"text","text":"{\"unrelated\":\"shape\"}"}]}`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			e := newAnthropicEnricherForTest(t, srv.URL)
			in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}

			out, err := e.EnrichTitles(context.Background(), in)
			if err != nil {
				t.Fatalf("malformed response should yield (zero,nil); got err=%v", err)
			}
			if len(out.Proposals) != 0 {
				t.Errorf("malformed response should yield zero proposals; got %d (%+v)", len(out.Proposals), out.Proposals)
			}
		})
	}
}

// TestAnthropicEnricher_EnrichRationale_OrderPreserved pins V0.2-PLAN §2.3:
// even when the model returns proposals in a different order from the
// request, the enricher's output is in REQUEST order. Without this guard a
// caller that walks both slices in parallel would write the wrong paragraph
// onto the wrong panel.
func TestAnthropicEnricher_EnrichRationale_OrderPreserved(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Server returns proposals in REVERSED order vs. the request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[{\"PanelUID\":\"p3\",\"Paragraph\":\"third\"},{\"PanelUID\":\"p2\",\"Paragraph\":\"second\"},{\"PanelUID\":\"p1\",\"Paragraph\":\"first\"}]}"}]}`)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)
	in := RationaleInput{Requests: []PanelRationaleRequest{
		{PanelUID: "p1", MechanicalTitle: "one"},
		{PanelUID: "p2", MechanicalTitle: "two"},
		{PanelUID: "p3", MechanicalTitle: "three"},
	}}

	out, err := e.EnrichRationale(context.Background(), in)
	if err != nil {
		t.Fatalf("EnrichRationale: %v", err)
	}
	if len(out.Proposals) != 3 {
		t.Fatalf("got %d proposals, want 3", len(out.Proposals))
	}
	wantPairs := [][2]string{{"p1", "first"}, {"p2", "second"}, {"p3", "third"}}
	for i, want := range wantPairs {
		got := out.Proposals[i]
		if got.PanelUID != want[0] || got.Paragraph != want[1] {
			t.Errorf("proposal[%d] = (%s, %q); want (%s, %q)", i, got.PanelUID, got.Paragraph, want[0], want[1])
		}
	}
}

// TestAnthropicEnricher_ClassifyUnknown_OnlyFiresForUnknownMetrics pins the
// no-allocation no-traffic contract for the empty-input case: when the
// caller has nothing to classify, the enricher must NOT issue a request.
// This is the cost-control invariant called out in V0.2-PLAN §2.4.
func TestAnthropicEnricher_ClassifyUnknown_OnlyFiresForUnknownMetrics(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[]}"}]}`)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)

	// Empty metrics → must NOT fire.
	out, err := e.ClassifyUnknown(context.Background(), ClassifyInput{})
	if err != nil {
		t.Fatalf("ClassifyUnknown(empty): %v", err)
	}
	if len(out.Hints) != 0 {
		t.Errorf("ClassifyUnknown(empty) returned %d hints; want 0", len(out.Hints))
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("ClassifyUnknown(empty) issued %d HTTP calls; must not fire when no unknown metrics", got)
	}

	// Non-empty metrics → must fire exactly once.
	in := ClassifyInput{Metrics: []MetricBrief{{Name: "weird_metric", Type: "counter"}}}
	if _, err := e.ClassifyUnknown(context.Background(), in); err != nil {
		t.Fatalf("ClassifyUnknown(non-empty): %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("ClassifyUnknown(non-empty) issued %d HTTP calls; want 1", got)
	}
}

// TestAnthropicEnricher_ClassifyUnknown_CacheHit pins the V0.2-PLAN §2.4
// caching contract: a second call with semantically identical input must
// be served from disk cache without issuing a second HTTP request. The
// cache key includes PromptHash() so any prompt-template byte change
// invalidates every prior entry — the prompts_test.go suite covers that
// half of the contract; this test covers the hit path itself.
func TestAnthropicEnricher_ClassifyUnknown_CacheHit(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[{\"PanelUID\":\"weird_metric\",\"Hints\":[{\"Traits\":[\"service_http\"],\"Confidence\":0.7}]}]}"}]}`)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)
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

// TestAnthropicEnricher_ContextCancel asserts the enricher honors the
// caller's context. A pre-cancelled context surfaces an error wrapping
// context.Canceled so callers can errors.Is-distinguish it from a true
// provider failure. We pre-cancel rather than dance with a blocking
// handler so httptest.Server.Close() does not hang waiting for an
// unfinished response.
func TestAnthropicEnricher_ContextCancel(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// The handler closes the connection abruptly if it ever runs — but
	// with a pre-cancelled context the http.Client refuses to dial in the
	// first place, so the handler should never fire.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: http.Client.Do refuses to issue the request

	in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}
	_, err := e.EnrichTitles(ctx, in)
	if err == nil {
		t.Fatal("EnrichTitles with cancelled ctx returned nil error; want context.Canceled wrap")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error chain missing context.Canceled: %v", err)
	}
}

// TestAnthropicEnricher_RateLimitBackoff pins the single-retry behavior on
// HTTP 429: the enricher must retry once after a brief delay and surface
// the eventual success (or failure) to the caller.
func TestAnthropicEnricher_RateLimitBackoff(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[{\"PanelUID\":\"p1\",\"Title\":\"After backoff\"}]}"}]}`)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)
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
	if elapsed < anthropicRetryDelay {
		t.Errorf("retry happened in %v; expected at least %v of backoff", elapsed, anthropicRetryDelay)
	}
	if len(out.Proposals) != 1 || out.Proposals[0].Title != "After backoff" {
		t.Errorf("expected one proposal with title 'After backoff'; got %+v", out.Proposals)
	}
}

// TestAnthropicEnricher_NeverGeneratesPromQL pins the §2.2 boundary: even
// when the model ignores its instructions and emits a `query` field, the
// enricher must drop it AND log a warning. The strict typed parser is the
// first line of defense; warnIfPromQL is the second.
func TestAnthropicEnricher_NeverGeneratesPromQL(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Inner text contains a "query" field the enricher must refuse to surface.
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[{\"PanelUID\":\"p1\",\"Title\":\"Naughty title\",\"query\":\"sum(rate(http_requests_total[5m]))\"}]}"}]}`)
	}))
	defer srv.Close()

	// Capture log output to verify the warn fired.
	var logBuf bytes.Buffer
	oldOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(oldOut)

	e := newAnthropicEnricherForTest(t, srv.URL)
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
	if !strings.Contains(logged, "query") || !strings.Contains(logged, "discarding") {
		t.Errorf("expected a 'discarding query field' warning in log output; got %q", logged)
	}
}
