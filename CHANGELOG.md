# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.2.0] — 2026-04-27

### Added

- **Recipe catalog expanded** from 12 to 44 recipes (service +12, infra +12, k8s +8).
  Every new recipe ships with Match test, BuildPanels test, fixture entries, and
  a discrimination negative-case guard.
- **`dashgen lint`** — offline audit of an existing dashboard bundle against seven
  check classes. See [`docs/lint.md`](docs/lint.md) for the check catalog and JSON
  output schema.
- **`dashgen coverage`** — offline report of metrics covered vs uncovered by a
  dashboard bundle, with family-grouping. See [`docs/coverage.md`](docs/coverage.md)
  for the report schema.
- **`dashgen generate --in-place`** — skip rewriting output files whose content is
  unchanged (idempotent re-runs preserve mtime).
- **Anthropic enrichment provider** (`--provider anthropic`, Phase 3) — opt-in AI
  titles and rationale via `claude-opus-4-7`. Requires `ANTHROPIC_API_KEY`.
- **OpenAI enrichment provider** (`--provider openai`, Phase 4) — same contract over
  `gpt-5`. Requires `OPENAI_API_KEY`. One-file addition validating the registry
  extension contract.
- **Shared enrichment cache** — results keyed by `(InventoryHash, Function,
  ProviderID, PromptHash, DashgenVersion)`; second run over the same inventory
  issues zero outbound requests. Invalidated automatically on prompt or binary
  version change.
- **Redaction guard** (`ValidateBriefs`) — called before every outbound enrichment
  request; enforces that label values, PromQL expressions, and endpoint URLs never
  cross the provider boundary. Pinned by per-provider proxy-capture regression tests.
- **New CLI flags:** `--provider`, `--provider-model`, `--enrich`, `--cache-dir`,
  `--no-enrich-cache`. See [`docs/AI-PROVIDERS.md`](docs/AI-PROVIDERS.md).
- **New IR fields** `Panel.MechanicalTitle` and `Panel.RationaleExtra` — populated
  only when enrichment runs; absent (zero-value) in `--provider off` output so
  existing tooling is unaffected.
- **`--log-enrichment-payloads`** debug flag (hidden unless `DASHGEN_DEBUG=1`) —
  emits one line per outbound enrichment call to stderr for local diagnostics.

### Changed

- **Panel-ID modulus** widened from `2^31-1` (int32 max) to `9007199254740881`
  (largest prime below `2^53`) to eliminate cross-panel UID collisions while
  staying within `Number.MAX_SAFE_INTEGER` for Grafana JS consumers (commit
  `6d3c8e0`).
- **Help-text trait hints** in `internal/classify` are now gated by an infra-label
  allowlist, preventing false-positive trait assignments from ambiguous help strings.

### Fixed

- Panel-ID and panel-UID cross-collisions in golden fixtures exposed by the widened
  modulus change.
