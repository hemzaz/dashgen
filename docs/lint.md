# `dashgen lint`

`dashgen lint --in <bundle-dir>` audits an existing dashboard bundle —
the `dashboard.json` + `rationale.md` + `warnings.json` triple a previous
`dashgen generate` produced, or that an operator hand-edited — for
quality and safety regressions.

The command is **deterministic**: two runs over identical input produce
byte-identical JSON output. It runs offline; no Prometheus or AI calls.

## Synopsis

```
dashgen lint --in ./dashboards            # report to stdout
dashgen lint --in ./dashboards --out lint.json
```

`--in` is required and must point at a directory containing
`dashboard.json`. The `rationale.md` sibling is optional — if absent,
checks that depend on it (currently just `missing-rationale-row`) are
disabled rather than failing the run.

## Output schema

```json
{
  "source": "dashboards/dashboard.json",
  "issues": [
    {
      "code": "banned-label",
      "severity": "refuse",
      "panel_id": 12345,
      "panel_title": "Request rate: foo",
      "message": "panel uses banned label \"user_id\" in PromQL; ..."
    }
  ]
}
```

Issues are sorted by `(code, panel_id, message)` for deterministic diffs.

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | clean — no issues |
| `1`  | generic error (e.g. nil config) |
| `5`  | input error — missing or malformed `dashboard.json` |
| `6`  | render error — could not write the JSON report |
| `7`  | lint failure — at least one `severity=refuse` issue |

`severity=warn` issues do **not** fail the run today. (A future
`--strict` flag could; not yet shipped.)

## Check catalog

The seed corpus ships with **seven** check classes. Adding a check is
one new file in `internal/lint/` plus a one-line `Register` call — see
the registry pattern in [`internal/lint/checks.go`](../internal/lint/checks.go).

| Code | Severity | What it catches |
|------|----------|-----------------|
| `banned-label`         | `refuse` | PromQL references one of `request_id` / `session_id` / `trace_id` / `user_id` (high-cardinality identifiers SPECS forbids in matchers and groupings). |
| `empty-panel`          | `refuse` | Non-row panel has zero PromQL targets — render an empty box instead of dropping it (SPECS Rule 5 says drop). |
| `duplicate-panel`      | `refuse` | Two non-row panels share both title and primary target expression — almost always a hand-edit duplicate. (Does NOT key on `panel.id`; modulo collisions in the renderer's int32 ID are tracked separately.) |
| `without-grouping`     | `refuse` | PromQL contains a `without (...)` aggregation. The `without` operator inverts the cardinality calculus from "what we keep" to "what we drop"; recipes always use `by (...)` against an explicit allowlist so safety policy can bound cardinality. |
| `missing-rationale-row`| `warn`   | Panel title is absent from `rationale.md` as a `**<title>**` line. The mechanical rationale is the audit trail every panel ships with. Disabled when `rationale.md` is missing. |
| `rate-on-gauge`        | `refuse` | `rate(...)` or `irate(...)` wraps a metric whose name lacks a counter suffix (`_total` / `_count` / `_sum` / `_bucket`). PromQL `rate` requires monotonically-increasing input — applying it to a gauge produces meaningless numbers. |
| `suspicious-units`     | `warn`   | `histogram_quantile(...)` over a latency-shaped histogram name (`*_seconds_bucket`, `*duration*`, `*latency*`) when the panel unit is not in the time family (`s`, `ms`, `ns`, etc.). Tightly scoped — bytes-name vs unit checks are intentionally NOT shipped because recipes routinely emit `bytes/bytes` ratios with `percentunit`. |

## Adding a check

1. Implement `lint.Check`:
   ```go
   type Check interface {
       Code() string                // stable kebab-case id
       Run(*Input) []Issue          // observe, never mutate
   }
   ```
2. Add a `Register(...)` call from your file's `init()`.
3. Add table-driven tests covering at least one positive, one
   identifier-boundary negative, and the row-skip case.

Outputs are auto-sorted by `RunAll`, so checks need not worry about
ordering.

## Known limitations

- The lint command does **not** re-run the validate pipeline on every
  query (no parse-only / selector-only entry point yet). PromQL syntax
  errors in a hand-edited dashboard pass lint silently. Tracked for a
  follow-up that exposes a parse-only `validate.Check` API.
- Bytes-unit / non-bytes-unit mismatches are intentionally not flagged
  to avoid false-positiving on `bytes/bytes` ratios.
- Renderer `panel.id` modulo collisions across distinct UIDs are
  surfaced separately (see the renderer task in the in-flight plan).
