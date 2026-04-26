#!/usr/bin/env bash
#
# scripts/smoke-anthropic.sh
#
# End-to-end smoke test for the Anthropic enricher backend.
#
# Behavior:
#   - When ANTHROPIC_API_KEY is unset → exits 0 with a skip message. This
#     is the CI-friendly default; nobody should ever be charged for a
#     test run that wasn't asked for.
#   - When the cobra binary lacks a `--provider` flag → exits 0 with a
#     skip message. The flag wiring lives in v0.2 Step 4.2; until that
#     ships this script can't drive the anthropic path through the CLI.
#   - When both prerequisites are present → runs `dashgen generate
#     --provider anthropic --fixture-dir testdata/fixtures/service-basic
#     --profile service --out <tmpdir>` twice and asserts byte-identical
#     output. The second run MUST hit the disk cache (V0.2-PLAN §2.4)
#     and produce identical bytes.
#
# Why test idempotency: the load-bearing v0.2 invariant is "AI mush ⇒
# deterministic output unchanged". Two consecutive runs with the same
# fixture and the same provider must converge to the same bytes; if the
# second run differs, either the cache is broken or the enricher is
# leaking nondeterminism into the IR.

set -euo pipefail

if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
    echo "[smoke-anthropic] ANTHROPIC_API_KEY unset; skipping smoke." >&2
    exit 0
fi

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

if ! go run ./cmd/dashgen generate --help 2>&1 | grep -q -- '--provider'; then
    echo "[smoke-anthropic] --provider flag not yet wired in cmd/dashgen (v0.2 Step 4.2); skipping CLI smoke." >&2
    exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

OUT1="$TMP/run1"
OUT2="$TMP/run2"
mkdir -p "$OUT1" "$OUT2"

echo "[smoke-anthropic] First run (cold cache, real Anthropic API call)..."
go run ./cmd/dashgen generate \
    --provider anthropic \
    --fixture-dir testdata/fixtures/service-basic \
    --profile service \
    --out "$OUT1"

echo "[smoke-anthropic] Second run (must hit cache and produce byte-identical output)..."
go run ./cmd/dashgen generate \
    --provider anthropic \
    --fixture-dir testdata/fixtures/service-basic \
    --profile service \
    --out "$OUT2"

echo "[smoke-anthropic] Diffing outputs..."
fail=0
for f in dashboard.json rationale.md warnings.json; do
    if ! cmp -s "$OUT1/$f" "$OUT2/$f"; then
        echo "[smoke-anthropic] FAIL: $f differs between runs (cache miss or non-determinism)" >&2
        diff -u "$OUT1/$f" "$OUT2/$f" || true
        fail=1
    fi
done

if (( fail )); then
    echo "[smoke-anthropic] FAIL: idempotency violated; AI off-parity contract broken." >&2
    exit 1
fi

echo "[smoke-anthropic] PASS: outputs are byte-identical across runs."
