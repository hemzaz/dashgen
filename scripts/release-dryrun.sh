#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

echo "==> Running goreleaser snapshot dry-run..."
goreleaser release --snapshot --clean

echo ""
echo "==> Produced artifacts in dist/:"
find dist/ \( -name "*.tar.gz" -o -name "checksums.txt" \) | sort
