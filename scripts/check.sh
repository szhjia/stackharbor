#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root"
if [ "${1:-}" = "--print-root" ]; then printf '%s\n' "$root"; exit 0; fi
files=$(find cmd internal tests examples -type f -name '*.go')
unformatted=$(gofmt -l $files)
if [ -n "$unformatted" ]; then printf 'Unformatted Go files:\n%s\n' "$unformatted"; exit 1; fi
go vet ./...
go test ./... -count=1 -timeout 120s
go test -race ./... -count=1 -timeout 120s

# Each runnable example is an independent Go module; verify it too.
find examples -name go.mod -print | while IFS= read -r manifest; do
  (cd "$(dirname "$manifest")" && go test ./...)
done
