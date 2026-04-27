# Recipe Testdata Schema

Each YAML recipe in `internal/recipes/data/**/*.yaml` may have a companion
fixture file at the same path with a `.testdata.json` suffix:

```
data/service/service_http_rate.yaml
data/service/service_http_rate.testdata.json   ← companion fixture
```

The harness `TestRecipesYAML_MatchAndBuild` (in `internal/recipes/`) walks
`data/**/*.yaml` in deterministic order and, for each recipe, loads the
companion fixture if present (skips with a log message if absent).

---

## Schema

```jsonc
{
  // positive_metrics: metrics that MUST match the recipe.
  // Every entry here must produce recipe.Match(view) == true.
  // Include realistic look-alike examples — the harness detects regressions.
  "positive_metrics": [
    {
      "name":   "http_requests_total",     // metric name (string, required)
      "type":   "counter",                 // "counter"|"gauge"|"histogram"|"summary"
      "labels": ["job", "route"],          // label NAMES ([]string)
      "traits": ["service_http"]           // classifier traits ([]string)
    }
  ],

  // negative_metrics: metrics that MUST NOT match the recipe.
  // Prefer look-alikes: same name prefix, wrong type; correct type, missing
  // trait; etc. These are how look-alike regressions are caught loudly.
  "negative_metrics": [
    {
      "name":   "http_request_duration_seconds_bucket",
      "type":   "histogram",
      "labels": ["job", "le"],
      "traits": ["service_http", "latency_histogram"]
    }
  ],

  // expected_panels: substring assertions on the panels emitted by
  // BuildPanels when fed all positive_metrics as a snapshot.
  // At least one produced panel must satisfy ALL non-empty substrings.
  "expected_panels": [
    {
      "title_contains": "HTTP request rate",  // substring of panel.Title
      "query_contains": "rate(",              // substring of any query Expr
      "kind":           "timeseries"          // exact PanelKind string (or "" to skip)
    }
  ]
}
```

---

## Field Semantics

### `positive_metrics` / `negative_metrics`

| Field    | Type       | Required | Description |
|----------|------------|----------|-------------|
| `name`   | `string`   | yes      | Metric family name (ASCII). |
| `type`   | `string`   | yes      | One of `counter`, `gauge`, `histogram`, `summary`. |
| `labels` | `[]string` | no       | Label NAMES (never values — invariant I2). |
| `traits` | `[]string` | no       | Classifier traits the recipe may gate on. |

`positive_metrics` must include **at least 1** entry; **at least 1 look-alike
negative** is strongly recommended to catch type- or trait-gate regressions.

### `expected_panels`

| Field            | Type     | Required | Description |
|------------------|----------|----------|-------------|
| `title_contains` | `string` | no       | Substring of `ir.Panel.Title`. Empty ⇒ skip check. |
| `query_contains` | `string` | no       | Substring of any `ir.QueryCandidate.Expr`. Empty ⇒ skip check. |
| `kind`           | `string` | no       | Exact `PanelKind` value (`timeseries`, `stat`, `graph`). Empty ⇒ skip check. |

Each `expected_panel` entry must be satisfied by **at least one** panel in the
BuildPanels output. The harness fails loudly naming the unsatisfied entry.

---

## Example

`testdata/example.testdata.json` is a worked example for `service_http_rate`.

---

## Adding fixtures (Phase 1B onward)

When migrating a recipe to YAML (Phase 1B / 4A / 5 / 6A):

1. Add the `.yaml` file under `data/<profile>/`.
2. Add the companion `.testdata.json` with ≥2 positives, ≥1 look-alike
   negative, and ≥1 expected_panel per panel template.
3. Run `go test ./internal/recipes/...` — the harness picks it up automatically.

Recipes without a companion fixture are skipped by the harness but produce a
visible log line, making gaps easy to spot in CI output.
