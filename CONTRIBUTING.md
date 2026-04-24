# Contributing to dashgen

Thanks for your interest. Before opening a PR, read [SPECS.md](SPECS.md) — it
defines what's in and out of v0.1 scope and the non-negotiables (determinism,
safety, separation of synthesis from rendering).

## Prerequisites

- Go 1.25+
- `make`
- (optional) `golangci-lint` v1.62+ for local linting

## Development loop

```bash
make build   # go build -o dashgen ./cmd/dashgen
make vet     # go vet ./...
make fmt     # gofmt -w .
make test    # go test ./...
```

`go test -race -cover ./...` runs in CI.

## Pull request checklist

- [ ] Change is in scope per `SPECS.md` (no AI hooks, no `/metrics` mode, no
      Grafana auto-apply, no alert/SLO generation in v0.1).
- [ ] `make build && make vet && make test` all pass locally.
- [ ] `gofmt -l .` is empty.
- [ ] New behavior has a test. Output-producing changes have a fixture or
      golden test.
- [ ] Determinism preserved — sorted output, stable IDs, no map iteration in
      user-visible output paths.
- [ ] Renderer code stays in `internal/render/*` and doesn't leak into
      synthesis (`internal/synth`, `internal/recipes`).
- [ ] Validation pipeline order is preserved: parse → selector → execute →
      safety → verdict.

## Commit style

Conventional-style prefix, imperative mood:

```
feat: add infra disk recipe
fix: reject queries with banned high-cardinality labels
test: add golden coverage for k8s pod-health profile
docs: clarify safety contract in SPECS
```

## Reporting issues

Include: dashgen version (`dashgen version` if available, or commit SHA),
Prometheus endpoint shape (live vs fixture), expected vs actual output, and a
minimal repro fixture under `testdata/fixtures/` if possible.
