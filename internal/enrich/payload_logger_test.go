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
	"strings"
	"sync"
	"testing"
)

// capturedPayload mirrors the PayloadLogger callback arguments so a test
// can assert on each fired entry without racing the HTTP server.
type capturedPayload struct {
	fn      string
	bytes   int
	preview string
}

// captureLogger returns a PayloadLogger that appends invocations to a
// shared, mutex-protected slice and a getter that snapshots the slice.
func captureLogger() (PayloadLogger, func() []capturedPayload) {
	var (
		mu  sync.Mutex
		log []capturedPayload
	)
	logger := func(fn string, n int, preview string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, capturedPayload{fn: fn, bytes: n, preview: preview})
	}
	snapshot := func() []capturedPayload {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedPayload, len(log))
		copy(out, log)
		return out
	}
	return logger, snapshot
}

// TestLogEnrichmentPayloads_OffByDefault_Anthropic asserts that a freshly
// constructed AnthropicEnricher carries a nil logger field, that the
// PayloadLoggerSetter contract is satisfied, and that no callback is
// invoked when no logger has been installed (zero-overhead path).
func TestLogEnrichmentPayloads_OffByDefault_Anthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[]}"}]}`)
	}))
	defer srv.Close()

	e := newAnthropicEnricherForTest(t, srv.URL)
	if e.logger != nil {
		t.Fatalf("logger non-nil after construction; default must be nil for zero overhead")
	}
	// Implements the optional setter interface.
	var _ PayloadLoggerSetter = e

	in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}
	if _, err := e.EnrichTitles(context.Background(), in); err != nil {
		t.Fatalf("EnrichTitles: %v", err)
	}
}

// TestLogEnrichmentPayloads_OffByDefault_OpenAI mirrors the anthropic
// off-by-default test for the openai provider.
func TestLogEnrichmentPayloads_OffByDefault_OpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[]}"}}]}`)
	}))
	defer srv.Close()

	e := newOpenAIEnricherForTest(t, srv.URL)
	if e.logger != nil {
		t.Fatalf("logger non-nil after construction; default must be nil for zero overhead")
	}
	var _ PayloadLoggerSetter = e

	in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}
	if _, err := e.EnrichTitles(context.Background(), in); err != nil {
		t.Fatalf("EnrichTitles: %v", err)
	}
}

// TestLogEnrichmentPayloads_FiresOncePerCall_Anthropic asserts that
// after SetPayloadLogger installs a callback, the callback fires
// exactly once per outbound HTTP call with non-zero bytes and a
// non-empty preview, and that the preview content is a substring of
// the wire bytes the server actually received.
func TestLogEnrichmentPayloads_FiresOncePerCall_Anthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var (
		mu       sync.Mutex
		wireBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		wireBody = append([]byte(nil), body...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[]}"}]}`)
	}))
	defer srv.Close()

	logger, snapshot := captureLogger()
	e := newAnthropicEnricherForTest(t, srv.URL)
	e.SetPayloadLogger(logger)

	in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}
	if _, err := e.EnrichTitles(context.Background(), in); err != nil {
		t.Fatalf("EnrichTitles: %v", err)
	}

	got := snapshot()
	if len(got) != 1 {
		t.Fatalf("logger fired %d times; want 1", len(got))
	}
	if got[0].fn != "enrich_titles" {
		t.Errorf("fn = %q; want %q", got[0].fn, "enrich_titles")
	}
	if got[0].bytes == 0 {
		t.Error("byteCount = 0; expected non-zero")
	}
	if got[0].preview == "" {
		t.Error("preview = empty; expected non-empty")
	}

	mu.Lock()
	body := append([]byte(nil), wireBody...)
	mu.Unlock()
	// The preview is built from wire bytes via payloadPreview. The head
	// segment (before the "..." elision, or the full preview when short)
	// must therefore be a prefix of the wire body.
	head := strings.SplitN(got[0].preview, "...", 2)[0]
	if !bytes.HasPrefix(body, []byte(head)) {
		t.Errorf("preview head is not a prefix of wire body\n head: %q", head)
	}
}

// TestLogEnrichmentPayloads_FiresOncePerCall_OpenAI mirrors the anthropic
// fires-once test for the openai provider.
func TestLogEnrichmentPayloads_FiresOncePerCall_OpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	var (
		mu       sync.Mutex
		wireBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		wireBody = append([]byte(nil), body...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[]}"}}]}`)
	}))
	defer srv.Close()

	logger, snapshot := captureLogger()
	e := newOpenAIEnricherForTest(t, srv.URL)
	e.SetPayloadLogger(logger)

	in := TitleInput{Requests: []PanelTitleRequest{{PanelUID: "p1", MechanicalTitle: "rps"}}}
	if _, err := e.EnrichTitles(context.Background(), in); err != nil {
		t.Fatalf("EnrichTitles: %v", err)
	}

	got := snapshot()
	if len(got) != 1 {
		t.Fatalf("logger fired %d times; want 1", len(got))
	}
	if got[0].fn != "enrich_titles" {
		t.Errorf("fn = %q; want %q", got[0].fn, "enrich_titles")
	}
	if got[0].bytes == 0 {
		t.Error("byteCount = 0; expected non-zero")
	}
	if got[0].preview == "" {
		t.Error("preview = empty; expected non-empty")
	}

	mu.Lock()
	body := append([]byte(nil), wireBody...)
	mu.Unlock()
	head := strings.SplitN(got[0].preview, "...", 2)[0]
	if !bytes.HasPrefix(body, []byte(head)) {
		t.Errorf("preview head is not a prefix of wire body\n head: %q", head)
	}
}

// TestLogEnrichmentPayloads_NeverLogsLabelValues is the load-bearing
// invariant for the redaction-safety contract: even with the logger ON,
// label-value-shaped strings planted in MetricBrief.Labels NEVER reach
// the preview, in raw, URL-encoded, or base64-encoded form.
//
// Phase 1 plants secrets in value-shaped form (`pod=pod-abc123`).
// ValidateBriefs is contracted to refuse these BEFORE the wire bytes are
// computed, so the logger callback must never fire — captured payloads
// stay empty.
//
// Phase 2 issues clean calls across all three Enricher methods. The
// captured payloads must contain real bytes (proving the logger was
// exercised) but must not contain any secret string from Phase 1's input.
//
// This test runs against BOTH hosted providers in subtests, mirroring
// TestAnthropicEnricher_RedactionAtProxyBoundary /
// TestOpenAIEnricher_RedactionAtProxyBoundary at the logger boundary.
func TestLogEnrichmentPayloads_NeverLogsLabelValues(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"content":[{"type":"text","text":"{\"proposals\":[]}"}]}`)
		}))
		defer srv.Close()

		logger, snapshot := captureLogger()
		e := newAnthropicEnricherForTest(t, srv.URL)
		e.SetPayloadLogger(logger)

		assertPayloadLoggerNeverLeaksSecrets(t, snapshot, func(in ClassifyInput) error {
			_, err := e.ClassifyUnknown(context.Background(), in)
			return err
		}, func(in TitleInput) error {
			_, err := e.EnrichTitles(context.Background(), in)
			return err
		}, func(in RationaleInput) error {
			_, err := e.EnrichRationale(context.Background(), in)
			return err
		})
	})

	t.Run("openai", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "test-key")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[]}"}}]}`)
		}))
		defer srv.Close()

		logger, snapshot := captureLogger()
		e := newOpenAIEnricherForTest(t, srv.URL)
		e.SetPayloadLogger(logger)

		assertPayloadLoggerNeverLeaksSecrets(t, snapshot, func(in ClassifyInput) error {
			_, err := e.ClassifyUnknown(context.Background(), in)
			return err
		}, func(in TitleInput) error {
			_, err := e.EnrichTitles(context.Background(), in)
			return err
		}, func(in RationaleInput) error {
			_, err := e.EnrichRationale(context.Background(), in)
			return err
		})
	})
}

// assertPayloadLoggerNeverLeaksSecrets shares the two-phase canary
// assertion between the anthropic and openai subtests of
// TestLogEnrichmentPayloads_NeverLogsLabelValues.
func assertPayloadLoggerNeverLeaksSecrets(
	t *testing.T,
	snapshot func() []capturedPayload,
	classifyFn func(ClassifyInput) error,
	titlesFn func(TitleInput) error,
	rationaleFn func(RationaleInput) error,
) {
	t.Helper()
	secrets := []string{"pod-abc123", "checkout-svc", "us-east-1"}

	// Phase 1: poisoned ClassifyUnknown. ValidateBriefs MUST refuse this
	// before any wire-byte computation, so the logger MUST NOT fire.
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
	if err := classifyFn(poisoned); err == nil {
		t.Fatal("CANARY: poisoned ClassifyUnknown returned nil error; ValidateBriefs guard appears to be bypassed")
	}
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("CANARY: poisoned ClassifyUnknown invoked logger %d times; ValidateBriefs must run BEFORE any logger fire (got %+v)", len(got), got)
	}

	// Phase 2: clean calls. These exercise the wire so the logger fires
	// against real bytes the canary can scan.
	clean := ClassifyInput{
		Metrics: []MetricBrief{{
			Name:   "http_requests_total",
			Type:   "counter",
			Help:   "Total HTTP requests",
			Labels: []string{"job", "method"},
		}},
	}
	if err := classifyFn(clean); err != nil {
		t.Fatalf("Phase 2 clean ClassifyUnknown returned error: %v", err)
	}
	if err := titlesFn(TitleInput{Requests: []PanelTitleRequest{{
		PanelUID:        "p1",
		MechanicalTitle: "rps",
		MetricName:      "http_requests_total",
		Section:         "traffic",
		Rationale:       "rps mechanical sentence",
	}}}); err != nil {
		t.Fatalf("Phase 2 EnrichTitles returned error: %v", err)
	}
	if err := rationaleFn(RationaleInput{Requests: []PanelRationaleRequest{{
		PanelUID:        "p1",
		MechanicalTitle: "rps",
		MetricName:      "http_requests_total",
		Section:         "traffic",
		Rationale:       "rps mechanical sentence",
		QueryExprs:      []string{"sum(rate(http_requests_total[5m]))"},
	}}}); err != nil {
		t.Fatalf("Phase 2 EnrichRationale returned error: %v", err)
	}

	got := snapshot()
	if len(got) == 0 {
		t.Fatal("CANARY: logger never fired across the three Phase 2 calls; SetPayloadLogger wiring is bypassed")
	}
	for _, payload := range got {
		for _, secret := range secrets {
			for _, encoded := range []string{
				secret,
				url.QueryEscape(secret),
				base64.StdEncoding.EncodeToString([]byte(secret)),
				base64.URLEncoding.EncodeToString([]byte(secret)),
			} {
				if strings.Contains(payload.preview, encoded) {
					t.Errorf("CANARY: secret %q (encoded form %q) leaked into preview %q (fn=%s)", secret, encoded, payload.preview, payload.fn)
				}
			}
		}
	}
}

// TestPayloadPreview_BoundedAndPrefixOfWire pins the helper's behavior:
// short inputs are returned in full; long inputs are clamped to head+tail
// with an elision; the head segment is always a strict prefix of the
// input bytes (proving the preview is "from wire bytes" by construction).
func TestPayloadPreview_BoundedAndPrefixOfWire(t *testing.T) {
	short := []byte(`{"model":"x","messages":[]}`)
	if got := payloadPreview(short); got != string(short) {
		t.Errorf("short input mutated: got %q want %q", got, string(short))
	}

	long := bytes.Repeat([]byte("A"), 96) // head boundary
	long = append(long, bytes.Repeat([]byte("M"), 200)...)
	long = append(long, bytes.Repeat([]byte("Z"), 96)...)
	got := payloadPreview(long)
	if !strings.Contains(got, "...") {
		t.Errorf("long preview missing elision marker: %q", got)
	}
	head := strings.SplitN(got, "...", 2)[0]
	if !bytes.HasPrefix(long, []byte(head)) {
		t.Errorf("preview head is not a prefix of input bytes\n head: %q", head)
	}
	tail := strings.SplitN(got, "...", 2)[1]
	if !bytes.HasSuffix(long, []byte(tail)) {
		t.Errorf("preview tail is not a suffix of input bytes\n tail: %q", tail)
	}
}
