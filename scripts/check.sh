#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
unformatted=$(gofmt -l . | grep -v '^vendor/' || true)
if [ -n "$unformatted" ]; then echo "gofmt 不通过:"; echo "$unformatted"; exit 1; fi
go vet ./...
CGO_ENABLED=0 go build ./...
go test ./...
if command -v node >/dev/null 2>&1; then
  node --check internal/server/web/app.js
  tmpjs=$(mktemp).js
  sed -n '/<script>/,/<\/script>/p' internal/server/web/join.html | sed '1d;$d' > "$tmpjs"
  node --check "$tmpjs"
  rm -f "$tmpjs"
fi
echo "✅ 地板全绿"
