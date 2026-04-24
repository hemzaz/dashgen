// Package prometheus is the concrete HTTP client for a Prometheus-compatible
// query API. It is the only package in DashGen that speaks HTTP to a
// Prometheus backend.
//
// Per-call timeouts are bounded by the timeout passed to NewClient. The
// client exposes a hook for a total-run budget so the caller can abort
// further calls once the budget is spent.
package prometheus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Client is the narrow interface the rest of DashGen depends on.
//
// Kept as an interface (not a bare struct) so tests can substitute a
// fixture-backed source without importing HTTP machinery.
type Client interface {
	Metadata(ctx context.Context) (map[string][]MetricMetadata, error)
	LabelNames(ctx context.Context, metric string) ([]string, error)
	Series(ctx context.Context, match []string) ([]map[string]string, error)
	InstantQuery(ctx context.Context, expr string) (*QueryResult, error)
}

// MetricMetadata mirrors a single entry from the Prometheus /metadata API.
type MetricMetadata struct {
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}

// QueryResult is the narrow subset of the Prometheus instant-query response
// DashGen cares about for validation.
type QueryResult struct {
	ResultType string
	NumSeries  int
	Warnings   []string
}

// HTTPClient is the concrete Client backed by net/http.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
	// budgetUsed is incremented atomically by each call's elapsed time in
	// nanoseconds. Callers can consult BudgetUsed before making new calls.
	budgetUsed atomic.Int64
}

// NewClient constructs an HTTPClient with a per-call timeout. A timeout of 0
// falls back to 5s.
func NewClient(baseURL string, timeout time.Duration) *HTTPClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}

// BudgetUsed returns the cumulative time spent in backend calls. The caller
// owns the policy of what to do when a budget is exhausted.
func (c *HTTPClient) BudgetUsed() time.Duration {
	return time.Duration(c.budgetUsed.Load())
}

func (c *HTTPClient) trackCall(start time.Time) {
	c.budgetUsed.Add(int64(time.Since(start)))
}

// promResponse is the envelope used by all Prometheus v1 endpoints.
type promResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType,omitempty"`
	Error     string          `json:"error,omitempty"`
	Warnings  []string        `json:"warnings,omitempty"`
}

func (c *HTTPClient) get(ctx context.Context, path string, query url.Values) (*promResponse, error) {
	start := time.Now()
	defer c.trackCall(start)

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", path, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	var pr promResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prometheus %s: %s: %s", path, pr.ErrorType, pr.Error)
	}
	return &pr, nil
}

// Metadata returns the result of /api/v1/metadata keyed by metric name.
func (c *HTTPClient) Metadata(ctx context.Context) (map[string][]MetricMetadata, error) {
	pr, err := c.get(ctx, "/api/v1/metadata", nil)
	if err != nil {
		return nil, err
	}
	out := map[string][]MetricMetadata{}
	if err := json.Unmarshal(pr.Data, &out); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	return out, nil
}

// LabelNames returns the label names observed on a given metric via
// /api/v1/labels?match[]=<metric>.
func (c *HTTPClient) LabelNames(ctx context.Context, metric string) ([]string, error) {
	if metric == "" {
		return nil, errors.New("LabelNames: metric required")
	}
	q := url.Values{}
	q.Set("match[]", metric)
	pr, err := c.get(ctx, "/api/v1/labels", q)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(pr.Data, &out); err != nil {
		return nil, fmt.Errorf("decode labels: %w", err)
	}
	return out, nil
}

// Series returns matching label sets from /api/v1/series.
func (c *HTTPClient) Series(ctx context.Context, match []string) ([]map[string]string, error) {
	if len(match) == 0 {
		return nil, errors.New("Series: at least one match required")
	}
	q := url.Values{}
	for _, m := range match {
		q.Add("match[]", m)
	}
	pr, err := c.get(ctx, "/api/v1/series", q)
	if err != nil {
		return nil, err
	}
	var out []map[string]string
	if err := json.Unmarshal(pr.Data, &out); err != nil {
		return nil, fmt.Errorf("decode series: %w", err)
	}
	return out, nil
}

// InstantQuery executes /api/v1/query and returns a narrow result suitable
// for validation. The full sample payload is discarded; DashGen only needs
// series count and warnings for v0.1 validation.
func (c *HTTPClient) InstantQuery(ctx context.Context, expr string) (*QueryResult, error) {
	if expr == "" {
		return nil, errors.New("InstantQuery: expr required")
	}
	q := url.Values{}
	q.Set("query", expr)
	pr, err := c.get(ctx, "/api/v1/query", q)
	if err != nil {
		return nil, err
	}
	var shape struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(pr.Data, &shape); err != nil {
		return nil, fmt.Errorf("decode query: %w", err)
	}
	return &QueryResult{
		ResultType: shape.ResultType,
		NumSeries:  len(shape.Result),
		Warnings:   pr.Warnings,
	}, nil
}
