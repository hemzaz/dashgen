// Package e2e_test runs the full dashgen pipeline against real Prometheus and
// Grafana containers. The synthetic metrics-emitter exposes one or more series
// per recipe; Prometheus scrapes it; dashgen --prom-url targets that
// Prometheus; the resulting dashboard JSON is POSTed to Grafana's
// /api/dashboards/db endpoint and the import is asserted to succeed.
//
// Run with: go test -tags=e2e -timeout 10m ./e2e/...
// Requires a running Docker daemon; default `go test ./...` skips this file.

//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// emitterPort is the host port the metrics-emitter listens on. The Prometheus
// container scrapes the host's gateway address at this port.
const emitterPort = "9091"

// profiles enumerates the dashgen profile catalog the test exercises end-to-end.
var profiles = []string{"service", "infra", "k8s"}

// TestE2E_AllRecipes_LoadInGrafana is the top-level e2e test. It is split into
// per-profile subtests that all share a single Prometheus + Grafana container
// pair (cheaper than tearing down between profiles).
func TestE2E_AllRecipes_LoadInGrafana(t *testing.T) {
	ctx := context.Background()

	// 1. Build the dashgen CLI binary into a temp file we can invoke per profile.
	binDir := t.TempDir()
	dashgenBin := filepath.Join(binDir, "dashgen")
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	build := exec.Command("go", "build", "-o", dashgenBin, "./cmd/dashgen")
	build.Dir = repoRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build dashgen: %v", err)
	}

	// 2. Start the metrics-emitter on the host. Running the emitter on the
	//    host (not in a container) is the cheapest path: containers can reach
	//    it via the Docker host gateway alias added in step 5 (HostAccessPorts).
	emitterCmd, emitterErrCh := startEmitter(t, repoRoot)
	t.Cleanup(func() {
		if emitterCmd != nil && emitterCmd.Process != nil {
			_ = emitterCmd.Process.Kill()
			_, _ = emitterCmd.Process.Wait()
		}
	})

	// 3. Sanity-check the emitter is up. Prometheus' scrape will fail if it
	//    isn't, and the failure mode (empty /metadata) gives a confusing
	//    "all panels refused" error several seconds later.
	if err := waitForLocalEmitter(ctx, 10*time.Second); err != nil {
		select {
		case eerr := <-emitterErrCh:
			t.Fatalf("emitter failed to start: %v (run error: %v)", err, eerr)
		default:
			t.Fatalf("emitter failed to start: %v", err)
		}
	}

	// 4. Build a shared Docker network so Prometheus and Grafana can talk
	//    by name.
	dockerNet, err := network.New(ctx)
	if err != nil {
		t.Fatalf("create docker network: %v", err)
	}
	t.Cleanup(func() { _ = dockerNet.Remove(ctx) })

	// 5. Start Prometheus, mounting our scrape config. The scrape target
	//    `host.docker.internal:9091` is added via testcontainers' HostAccessPorts
	//    so Prometheus can resolve the host gateway on Linux runners.
	promYML := filepath.Join(repoRoot, "e2e", "testdata", "prometheus.yml")
	promReq := testcontainers.ContainerRequest{
		Image:        "prom/prometheus:v3.2.1",
		ExposedPorts: []string{"9090/tcp"},
		Networks:     []string{dockerNet.Name},
		NetworkAliases: map[string][]string{
			dockerNet.Name: {"prometheus"},
		},
		Cmd: []string{
			"--config.file=/etc/prometheus/prometheus.yml",
			"--storage.tsdb.path=/prometheus",
			"--storage.tsdb.retention.time=1h",
		},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      promYML,
			ContainerFilePath: "/etc/prometheus/prometheus.yml",
			FileMode:          0o644,
		}},
		HostAccessPorts: []int{9091},
		WaitingFor:      wait.ForHTTP("/-/ready").WithPort("9090/tcp").WithStartupTimeout(60 * time.Second),
	}
	promC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: promReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start prometheus: %v", err)
	}
	t.Cleanup(func() { _ = promC.Terminate(ctx) })

	promHost, err := promC.Host(ctx)
	if err != nil {
		t.Fatalf("prom host: %v", err)
	}
	promPort, err := promC.MappedPort(ctx, "9090/tcp")
	if err != nil {
		t.Fatalf("prom port: %v", err)
	}
	promURL := fmt.Sprintf("http://%s:%s", promHost, promPort.Port())
	t.Logf("Prometheus reachable from host: %s", promURL)

	// 6. Wait for Prometheus to scrape at least 2 cycles (2s interval -> 5s slack).
	if err := waitForPromTarget(ctx, promURL, 30*time.Second); err != nil {
		t.Fatalf("prometheus did not see emitter: %v", err)
	}

	// 7. Start Grafana with anonymous auth and a provisioned Prometheus
	//    datasource pointing at the in-network `prometheus:9090` alias.
	dsYML := filepath.Join(repoRoot, "e2e", "testdata", "grafana-datasource.yml")
	grafReq := testcontainers.ContainerRequest{
		Image:        "grafana/grafana:11.4.0",
		ExposedPorts: []string{"3000/tcp"},
		Networks:     []string{dockerNet.Name},
		Env: map[string]string{
			"GF_AUTH_ANONYMOUS_ENABLED":  "true",
			"GF_AUTH_ANONYMOUS_ORG_ROLE": "Admin",
			"GF_LOG_LEVEL":               "warn",
		},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      dsYML,
			ContainerFilePath: "/etc/grafana/provisioning/datasources/datasource.yml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForHTTP("/api/health").WithPort("3000/tcp").WithStartupTimeout(60 * time.Second),
	}
	grafC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: grafReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start grafana: %v", err)
	}
	t.Cleanup(func() { _ = grafC.Terminate(ctx) })

	grafHost, err := grafC.Host(ctx)
	if err != nil {
		t.Fatalf("grafana host: %v", err)
	}
	grafPort, err := grafC.MappedPort(ctx, "3000/tcp")
	if err != nil {
		t.Fatalf("grafana port: %v", err)
	}
	grafURL := fmt.Sprintf("http://%s:%s", grafHost, grafPort.Port())
	t.Logf("Grafana reachable from host: %s", grafURL)

	// 8. Per-profile pipeline: dashgen generate -> POST to Grafana -> verify.
	imported := 0
	for _, prof := range profiles {
		prof := prof
		t.Run(prof, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), prof)
			cmd := exec.Command(dashgenBin, "generate",
				"--prom-url", promURL,
				"--profile", prof,
				"--out", outDir,
			)
			cmd.Dir = repoRoot
			var out, errBuf bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errBuf
			if err := cmd.Run(); err != nil {
				t.Fatalf("dashgen generate %s: %v\nstdout:\n%s\nstderr:\n%s",
					prof, err, out.String(), errBuf.String())
			}

			dashPath := filepath.Join(outDir, "dashboard.json")
			dashBytes, err := os.ReadFile(dashPath)
			if err != nil {
				t.Fatalf("read dashboard.json: %v", err)
			}

			panels, title := summarizeDashboard(t, dashBytes)
			if panels == 0 {
				t.Fatalf("profile %s: dashboard has zero panels (recipe matching failed)", prof)
			}
			t.Logf("profile %s: dashboard %q has %d panels", prof, title, panels)

			if err := importIntoGrafana(ctx, grafURL, dashBytes); err != nil {
				t.Fatalf("import %s into Grafana: %v", prof, err)
			}
			imported++
		})
	}

	if imported != len(profiles) {
		t.Fatalf("imported %d/%d dashboards; expected all profiles to succeed", imported, len(profiles))
	}

	// 9. Final cross-check: GET /api/search and assert the imported dashboards
	//    are actually persisted.
	if err := verifyGrafanaSearch(ctx, grafURL, len(profiles)); err != nil {
		t.Fatalf("verify search: %v", err)
	}
	t.Logf("e2e: %d/%d dashboards imported and visible in /api/search", imported, len(profiles))
}

// startEmitter launches the metrics-emitter on :9091 via `go run`. Returning
// the *exec.Cmd lets the caller kill the process via t.Cleanup.
func startEmitter(t *testing.T, repoRoot string) (*exec.Cmd, <-chan error) {
	t.Helper()
	errCh := make(chan error, 1)
	cmd := exec.Command("go", "run", "./e2e/metrics-emitter")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "EMITTER_ADDR=:"+emitterPort)
	// Do not attach os.Stdout/os.Stderr: when the process is killed via
	// Process.Kill(), open pipes cause go test to report "WaitDelay expired
	// before I/O complete" and exit non-zero even though all subtests passed.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start emitter: %v", err)
	}
	go func() { errCh <- cmd.Wait() }()
	return cmd, errCh
}

// waitForLocalEmitter polls the host /metrics endpoint until it responds 200
// or the deadline expires.
func waitForLocalEmitter(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := "http://127.0.0.1:" + emitterPort + "/metrics"
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("emitter did not become ready within %s", timeout)
}

// waitForPromTarget polls the Prometheus instant-query API until the synthetic
// emitter target is UP and at least one classifier-relevant metric series is
// queryable. This proves we have ≥2 scrape cycles' worth of samples.
func waitForPromTarget(ctx context.Context, promURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	checkURL := promURL + `/api/v1/query?query=up%7Bjob%3D%22dashgen-e2e%22%7D`
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if bytes.Contains(body, []byte(`"value":[`)) && bytes.Contains(body, []byte(`"1"`)) {
				// At least one sample arrived. Sleep for one more scrape interval
				// so rate() has the two consecutive points it needs.
				time.Sleep(3 * time.Second)
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("prometheus target up{job=dashgen-e2e}==1 not seen within %s", timeout)
}

// summarizeDashboard walks the dashboard JSON and returns (panel count,
// title). dashgen emits panels nested inside rows, so we recursively flatten.
func summarizeDashboard(t *testing.T, raw []byte) (int, string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse dashboard.json: %v", err)
	}
	title, _ := doc["title"].(string)
	count := countPanels(doc["panels"])
	return count, title
}

// countPanels recursively counts terminal (non-row) panels in Grafana
// dashboard JSON. Rows have type=="row" and a nested "panels" array.
func countPanels(v any) int {
	arr, ok := v.([]any)
	if !ok {
		return 0
	}
	n := 0
	for _, p := range arr {
		obj, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := obj["type"].(string); t == "row" {
			n += countPanels(obj["panels"])
			continue
		}
		n++
	}
	return n
}

// importIntoGrafana wraps the dashboard JSON in the Grafana import envelope
// and POSTs it. The endpoint validates the dashboard server-side; a 200
// response means Grafana accepted it.
func importIntoGrafana(ctx context.Context, baseURL string, dashboardJSON []byte) error {
	var dashboard any
	if err := json.Unmarshal(dashboardJSON, &dashboard); err != nil {
		return fmt.Errorf("decode dashboard: %w", err)
	}
	envelope := map[string]any{
		"dashboard": dashboard,
		"overwrite": true,
		"folderId":  0,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/api/dashboards/db", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// verifyGrafanaSearch fetches /api/search and asserts at least `want`
// dashboards are present.
func verifyGrafanaSearch(ctx context.Context, baseURL string, want int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/api/search?type=dash-db", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	var hits []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
		return fmt.Errorf("decode search: %w", err)
	}
	if len(hits) < want {
		return fmt.Errorf("only %d dashboards visible in /api/search; expected at least %d",
			len(hits), want)
	}
	return nil
}
