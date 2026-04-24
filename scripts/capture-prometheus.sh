#!/usr/bin/env bash
# capture-prometheus.sh — snapshot a live Prometheus-compatible backend into
# a DashGen fixture directory (compatible with --fixture-dir).
#
# Usage: scripts/capture-prometheus.sh <prom-url> <fixture-dir>
#
# Requires: dashgen binary built at ./dashgen, curl, python3.

set -euo pipefail

PROM="${1:?prom-url required: scripts/capture-prometheus.sh <prom-url> <fixture-dir>}"
OUT="${2:?fixture-dir required: scripts/capture-prometheus.sh <prom-url> <fixture-dir>}"

if [ ! -x ./dashgen ]; then
	echo "error: ./dashgen not found; run 'make build' first" >&2
	exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$OUT/instant"

echo "==> metadata"
curl -sS --fail --max-time 30 "$PROM/api/v1/metadata" \
	| python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d['data'], indent=2, sort_keys=True))" \
	> "$OUT/metadata.json"

echo "==> series (all __name__!=\"\")"
curl -sS --fail --max-time 60 -G "$PROM/api/v1/series" \
	--data-urlencode 'match[]={__name__!=""}' \
	| python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d['data'], indent=2, sort_keys=True))" \
	> "$OUT/series.json"

echo "==> generate against live to learn queries"
./dashgen generate --prom-url "$PROM" --profile service --out "$TMP/gen"

echo "==> extract query expressions from rationale.md"
python3 - "$TMP/gen/rationale.md" > "$TMP/exprs.txt" <<'PY'
import re, sys, pathlib
rat = pathlib.Path(sys.argv[1]).read_text()
exprs = sorted(set(re.findall(r'^\s*- query: `([^`]+)`', rat, flags=re.M)))
for e in exprs:
    print(e)
PY
n=$(wc -l < "$TMP/exprs.txt" | tr -d ' ')
echo "    found $n unique expressions"

echo "==> capture instant query results"
i=0
while IFS= read -r expr; do
	[ -z "$expr" ] && continue
	hash=$(printf '%s' "$expr" | python3 -c "import hashlib,sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest()[:16])")
	file="$OUT/instant/$hash.json"
	resp=$(curl -sS --fail --max-time 30 -G "$PROM/api/v1/query" --data-urlencode "query=$expr" || echo '{}')
	python3 - "$resp" > "$file" <<'PY'
import json, sys
try:
    raw = json.loads(sys.argv[1])
except Exception:
    raw = {}
data = (raw.get("data") or {})
result = data.get("result") or []
warnings = raw.get("warnings") or None
out = {
    "ResultType": data.get("resultType", "vector"),
    "NumSeries": len(result),
    "Warnings": warnings,
}
print(json.dumps(out, indent=2, sort_keys=True))
PY
	i=$((i+1))
done < "$TMP/exprs.txt"
echo "    wrote $i instant files"

echo "==> fixture ready at $OUT"
echo "    metadata metrics: $(python3 -c "import json; print(len(json.load(open('$OUT/metadata.json'))))")"
echo "    series: $(python3 -c "import json; print(len(json.load(open('$OUT/series.json'))))")"
echo "    instant files: $(ls "$OUT/instant" | wc -l | tr -d ' ')"
