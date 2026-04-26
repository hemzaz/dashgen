# AI Enrichment Providers

AI enrichment in DashGen is **optional, opt-in, and isolated** from the deterministic
generation pipeline. Enabling a provider adds human-scannable titles and rationale
paragraphs to the output; it cannot generate PromQL, cannot upgrade a refused verdict,
and cannot bypass any stage of the validation pipeline. The full contract is defined
in [V0.2-PLAN.md §2.2](V0.2-PLAN.md).

`--provider off` (the default) produces output byte-identical to v0.1. No API call is
issued unless you explicitly pass `--provider <name>`.

## Quickstart

```bash
export ANTHROPIC_API_KEY=sk-ant-...
dashgen generate --prom-url http://localhost:9090 --profile service \
    --provider anthropic --enrich titles,rationale
```

With enrichment enabled, each panel in `dashboard.json` gains two additional IR
fields: `MechanicalTitle` (the original deterministic title, always preserved) and
`RationaleExtra` (AI-authored supplementary prose). The PromQL expressions, recipe
selections, validation verdicts, panel UIDs, and section ordering are identical to a
`--provider off` run — only the human-facing text changes.

## Provider matrix

| Provider | Status | Auth env-var | Model default | Network boundary |
|----------|--------|--------------|---------------|------------------|
| `anthropic` | Shipped (v0.2 Phase 3) | `ANTHROPIC_API_KEY` | `claude-opus-4-7` | `https://api.anthropic.com` |
| `openai` | Shipped (v0.2 Phase 4) | `OPENAI_API_KEY` | `gpt-5` | `https://api.openai.com` |
| `ollama` | Placeholder (v0.3 backlog) | n/a | TBD | localhost only when shipped |

## Anthropic setup

### Prerequisites

Export your API key before running:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

The constructor fails fast with `ErrAnthropicNoAPIKey` when the variable is unset.
No partial output is produced.

### Invocation

```bash
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --provider anthropic \
  --enrich titles,rationale \
  --out ./dashboards
```

The first run contacts the Anthropic Messages API for each requested enrichment
function. Every subsequent run over the same inventory is served entirely from the
on-disk cache — **zero outbound traffic is issued**. The cache-hit path is verified by
`TestAnthropicEnricher_ClassifyUnknown_CacheHit` in
[`internal/enrich/anthropic_test.go`](../internal/enrich/anthropic_test.go).

### Failure modes

| Condition | Behavior |
|-----------|----------|
| `ANTHROPIC_API_KEY` unset | Constructor fails fast; run aborts before any file is written. |
| Provider unreachable / network error | Logged at `warn`; run continues with deterministic-only output. |
| 429 or 5xx from the API | One short retry; if the retry also fails, falls back to deterministic output. Non-fatal. |
| Malformed JSON response | Logged, discarded; deterministic title and rationale are preserved unchanged. |
| AI response contains a `query` field | Field is dropped, warning logged; PromQL is always owned by the deterministic pipeline ([V0.2-PLAN.md §2.2](V0.2-PLAN.md)). |

## OpenAI setup

### Prerequisites

Export your API key before running:

```bash
export OPENAI_API_KEY=sk-...
```

The constructor fails fast with `ErrOpenAINoAPIKey` when the variable is unset.
No partial output is produced.

### Invocation

```bash
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --provider openai \
  --enrich titles,rationale \
  --out ./dashboards
```

Default model is `gpt-5`; override with `--provider-model <id>`. The first run
contacts the OpenAI Chat Completions API for each requested enrichment function;
subsequent runs over the same inventory are served entirely from the on-disk
cache — **zero outbound traffic is issued**. The cache-hit path is verified by
`TestOpenAIEnricher_ClassifyUnknown_CacheHit` in
[`internal/enrich/openai_test.go`](../internal/enrich/openai_test.go).

`anthropic` and `openai` implement the same `enrich.Enricher` contract over the
same prompt templates. Switching `--provider anthropic` ↔ `--provider openai`
over the same inventory only changes title and rationale prose — never query,
verdict, or panel UID material.

### Failure modes

| Condition | Behavior |
|-----------|----------|
| `OPENAI_API_KEY` unset | Constructor fails fast; run aborts before any file is written. |
| Provider unreachable / network error | Logged at `warn`; run continues with deterministic-only output. |
| 429 or 5xx from the API | One short retry; if the retry also fails, falls back to deterministic output. Non-fatal. |
| Malformed JSON response | Logged, discarded; deterministic title and rationale are preserved unchanged. |
| AI response contains a `query` field | Field is dropped, warning logged; PromQL is always owned by the deterministic pipeline ([V0.2-PLAN.md §2.2](V0.2-PLAN.md)). |

## Enrichment flags

| Flag | Default | Description |
|------|---------|-------------|
| `--provider` | `off` | Provider name. `off` and the empty string are identical; both use the deterministic-only (noop) path. Unknown names fail fast with `ErrUnknownProvider`. |
| `--provider-model` | _(provider default)_ | Override the model ID. Empty means "use the provider default" (`claude-opus-4-7` for Anthropic, `gpt-5` for OpenAI). |
| `--enrich` | `none` when provider is `off`; `titles,rationale` when a provider is set | Comma-separated enrichment operations: `titles`, `rationale`, `classify`, `all`, `none`. |
| `--no-enrich-cache` | `false` | Bypass the on-disk cache and force a fresh API request. Intended for authoring and debugging only. |
| `--cache-dir` | `~/.cache/dashgen/enrich` | Override the cache directory (`$XDG_CACHE_HOME/dashgen/enrich` when `$XDG_CACHE_HOME` is set). |

## What gets sent to the provider

DashGen enforces a strict redaction contract before issuing any outbound request.
The contract is stated in [V0.2-PLAN.md §2.2](V0.2-PLAN.md) and enforced at
`ValidateBriefs` — called before every HTTP write in `internal/enrich/anthropic.go`.

**Sent to the provider:**

- Metric names (e.g. `api_http_requests_total`)
- Label names (e.g. `handler`, `method`, `status_code`)
- Metric help text (e.g. `"Total number of HTTP requests"`)
- Panel UIDs and section names (the stable, deterministic identifiers)

**Never sent:**

- Label values (e.g. `handler="/api/v1/login"`) — these can contain PII or internal IDs
- PromQL expressions
- Instance, pod, namespace, or any other actual series label values
- The Prometheus endpoint URL or any backend address

This contract is pinned by per-provider regression guards plus an end-to-end
smoke harness:

- `TestAnthropicEnricher_RedactionAtProxyBoundary` in
  [`internal/enrich/anthropic_test.go`](../internal/enrich/anthropic_test.go) — a
  proxy-capture canary that asserts no label values cross the outbound boundary
  for the Anthropic provider.
- `TestOpenAIEnricher_RedactionAtProxyBoundary` in
  [`internal/enrich/openai_test.go`](../internal/enrich/openai_test.go) — the
  same canary applied to the OpenAI provider's wire surface, proving the
  redaction contract holds for both shipped providers identically.
- `TestLogEnrichmentPayloads_NeverLogsLabelValues` in
  [`internal/enrich/payload_logger_test.go`](../internal/enrich/payload_logger_test.go)
  — extends the canary to the optional debug-only payload-logger callback
  (see ["Debug: payload preview logging"](#debug-payload-preview-logging) below)
  with anthropic and openai subtests.
- [`scripts/smoke-anthropic.sh`](../scripts/smoke-anthropic.sh) — an end-to-end smoke
  test against a live Anthropic backend that confirms the overall request shape.
  [`scripts/smoke-openai.sh`](../scripts/smoke-openai.sh) is the OpenAI mirror.

## What gets cached

Enrichment results are stored under `~/.cache/dashgen/enrich/` (or
`$XDG_CACHE_HOME/dashgen/enrich/` when `$XDG_CACHE_HOME` is set).

Each cache entry is keyed by the following tuple:

| Field | Description |
|-------|-------------|
| `InventoryHash` | SHA-256 prefix of the serialized call input (16 hex chars) |
| `Function` | `classify_unknown`, `enrich_titles`, or `enrich_rationale` |
| `ProviderID` | Provider name + model, e.g. `anthropic:claude-opus-4-7` |
| `PromptHash` | Hash of the canonical prompt templates in `internal/enrich/prompts.go` |
| `DashgenVersion` | Binary version string |

Cache entries are **automatically invalidated** when any of the following change:

- The prompt templates (`PromptHash` changes).
- The dashgen binary version (`DashgenVersion` changes).
- The inventory — a different metric set or recipe set produces a different `InventoryHash`.

To force a fresh API request without deleting the cache, pass `--no-enrich-cache`. To
wipe all cached enrichment results, delete the cache directory:

```bash
rm -rf ~/.cache/dashgen/enrich
```

## Determinism

With a populated cache, two consecutive `dashgen generate` runs over the same inventory
produce **byte-identical** `dashboard.json`, `rationale.md`, and `warnings.json`. No
network calls are issued on the second run. The only API-call surface is the first run
for each unique inventory.

`--provider off` (the default) is byte-identical to v0.1 output. AI enrichment never
silently affects operators who have not opted in.

## Debug: payload preview logging

`--log-enrichment-payloads` is a **debug-only** flag that emits one line per
outbound enrichment HTTP call to stderr in the form:

```
dashgen[enrich]: provider=openai fn=enrich_titles bytes=1234 preview="…"
```

The `preview` field is computed from the bytes the enricher is about to send
to the provider — never from pre-redaction caller input. Anything
`ValidateBriefs` would have rejected (label values, value-shaped label
matchers) cannot reach the preview by construction. This invariant is pinned
by `TestLogEnrichmentPayloads_NeverLogsLabelValues` for both Anthropic and
OpenAI.

The flag is **hidden from `--help`** unless `DASHGEN_DEBUG=1` is set in the
environment at command construction. This guards against the "debug paths
become product paths" drift pattern — operators must not start wiring
`--log-enrichment-payloads` into CI flows.

To surface and use the flag during local debugging:

```bash
DASHGEN_DEBUG=1 dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --provider openai \
  --enrich titles,rationale \
  --log-enrichment-payloads \
  --out ./dashboards 2>./enrich-trace.log
```

Do **not** ship CI scripts that pass `--log-enrichment-payloads`. The flag
exists for ad-hoc local diagnostics only.

## Adding a custom provider

`internal/enrich/factory.go` is the single extension point. Adding a provider —
hosted or local — requires exactly two changes inside the `enrich` package.
**Phase 4's OpenAI provider was a one-file addition** (`internal/enrich/openai.go`,
plus its sibling `_test.go`) — concrete proof that the registry contract holds:
the second hosted provider needed no changes outside the package, no edits to
`internal/app/generate`, no edits to `cmd/dashgen`, no new top-level interface.

1. Create `internal/enrich/<name>.go` with a constructor of type
   `func(Spec) (Enricher, error)` that builds the concrete enricher and calls
   `Register` from `init()`:

   ```go
   package enrich

   func newMyProvider(spec Spec) (Enricher, error) {
       // Read credentials from the environment, validate, return the enricher.
       return &myProvider{model: spec.Model}, nil
   }

   func init() {
       Register("myprovider", newMyProvider)
   }
   ```

2. Implement `enrich.Enricher` in the same file:

   ```go
   type Enricher interface {
       Describe() Description
       ClassifyUnknown(ctx context.Context, in ClassifyInput) (ClassifyOutput, error)
       EnrichTitles(ctx context.Context, in TitleInput) (TitleOutput, error)
       EnrichRationale(ctx context.Context, in RationaleInput) (RationaleOutput, error)
   }
   ```

Nothing in `internal/app/generate`, `cmd/dashgen`, or `internal/config` needs to
change. The CLI accepts any string that resolves through the registry; unknown names
return `ErrUnknownProvider` with the list of registered providers. See
[V0.2-PLAN.md §2.7](V0.2-PLAN.md) for the full extension contract and `Spec` field
semantics.

## Compliance and privacy

Hosted providers see your **metric names**. If your metric inventory contains sensitive
names, use `ignored_metrics` in your config file to exclude them before generation:

```yaml
# dashgen.yaml
ignored_metrics:
  - internal_payment_
  - pii_user_
```

The network boundary for each active provider is listed in the provider matrix above.
Review your egress firewall policy before enabling a hosted provider in a
network-restricted environment.
