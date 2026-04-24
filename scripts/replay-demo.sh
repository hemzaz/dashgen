#!/usr/bin/env bash
# replay-demo.sh — replay the captured demo fixture twice and confirm
# byte-identical output across runs (determinism smoke test on real-world
# data shape). Requires scripts/capture-prometheus.sh to have been run.
set -euo pipefail

FIXTURE="${FIXTURE_DIR:-testdata/fixtures/prometheus-demo}"
A="${A:-/tmp/dashgen-replay1}"
B="${B:-/tmp/dashgen-replay2}"

if [ ! -x ./dashgen ]; then
	echo "error: ./dashgen not found; run 'make build' first" >&2
	exit 1
fi
if [ ! -f "$FIXTURE/metadata.json" ]; then
	echo "error: fixture missing at $FIXTURE" >&2
	echo "run: scripts/capture-prometheus.sh https://prometheus.demo.prometheus.io $FIXTURE" >&2
	exit 1
fi

./dashgen generate --fixture-dir "$FIXTURE" --profile service --out "$A"
./dashgen generate --fixture-dir "$FIXTURE" --profile service --out "$B"

diff -q "$A/dashboard.json" "$B/dashboard.json"
diff -q "$A/rationale.md"   "$B/rationale.md"
diff -q "$A/warnings.json"  "$B/warnings.json"

printf 'replay-demo: deterministic across two runs (%s bytes)\n' "$(wc -c < "$A/dashboard.json" | tr -d ' ')"
