# dashgen e2e harness

End-to-end test that runs the full dashgen pipeline against a real Prometheus
and a real Grafana, both running in Docker via [testcontainers-go]. The test
asks the load-bearing question:

> Do the dashboards dashgen produces actually import into Grafana?

The default `go test ./...` skips this file because of the `e2e` build tag, so
day-to-day development is unaffected. CI (see `.github/workflows/e2e.yml`)
runs the e2e job on `pull_request` and `workflow_dispatch`.

## What it does

1. Builds the `dashgen` CLI binary into a temp dir.
2. Starts the synthetic `metrics-emitter` on the host at `:9091` — exposes
   one or more series for every metric name + label set + type that
   dashgen's 47 built-in recipes match against.
3. Spins up Prometheus in Docker, scraping the host emitter via the Docker
   host gateway alias. Waits for at least 2 scrape cycles.
4. Spins up Grafana in Docker (anonymous Admin, datasource pre-provisioned).
5. For each profile (`service`, `infra`, `k8s`):
   - Runs `dashgen generate --prom-url <prom> --profile <p> --out <tmp>`.
   - Asserts the generated dashboard has at least one panel.
   - POSTs the dashboard to `POST /api/dashboards/db` and asserts HTTP 200.
6. Cross-checks `GET /api/search` and asserts all 3 dashboards are visible.
7. Tears every container down via `t.Cleanup`.

## Run locally

```bash
# Docker daemon must be running.
docker info

# Run the test (the build tag is mandatory).
cd /path/to/dashgen
go test -tags=e2e -timeout 10m -v ./e2e/...
```

Wall-clock cost on Apple Silicon: ~60–90 seconds for a single run after
images are cached locally. First run pulls `prom/prometheus:v3.2.1` and
`grafana/grafana:11.4.0` (~250MB combined).

## Why the emitter runs on the host

We avoid building a separate Docker image for the emitter. Containers reach
the host emitter via testcontainers' `HostAccessPorts`, which sets up the
`host.docker.internal` gateway (Docker Desktop) or its Linux equivalent.
Saves ~10–30 seconds of `docker build` per run and keeps the test single-binary.

## What is NOT covered

- **Render fidelity.** Grafana's import endpoint validates the dashboard JSON
  shape, but does not exercise panel-render paths. A 200 from the import
  endpoint plus a hit in `/api/search` is the test's success signal.
- **Long-running or rate-only data.** Counters are seeded once with constant
  values; rate() over them returns 0 after the first scrape pair. Recipes
  with `rate(...)` panels still validate (they pass the PromQL parser and
  produce non-empty result sets), which is what dashgen's validation
  pipeline asserts.
- **Recipe coverage proof.** The test asserts each profile's dashboard has
  at least one panel. It does NOT individually assert each of the 47 recipes
  fired. The unit-test matrix in `internal/recipes/data/*.testdata.json`
  already provides per-recipe Match/BuildPanels coverage.

## Recipes that genuinely cannot fire

None. Every recipe in `internal/recipes/data/{service,infra,k8s}/*.yaml`
matches at least one synthetic series emitted by `metrics-emitter/main.go`.

[testcontainers-go]: https://golang.testcontainers.org/
