#!/usr/bin/env bash
# run-demo.sh — run dashgen against the public Prometheus demo.
# Usage: scripts/run-demo.sh [out-dir]
set -euo pipefail

PROM="${PROM_URL:-https://prometheus.demo.prometheus.io}"
OUT="${1:-/tmp/dashgen-demo}"

if [ ! -x ./dashgen ]; then
	echo "error: ./dashgen not found; run 'make build' first" >&2
	exit 1
fi

mkdir -p "$OUT"
./dashgen generate --prom-url "$PROM" --profile service --out "$OUT"
ls -la "$OUT"
