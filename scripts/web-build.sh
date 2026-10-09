#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root"
npm --prefix web ci
node scripts/frontend-licenses.mjs --check
if [ "${1:-}" = "--check" ]; then
 npm --prefix web audit --audit-level=high
 npm --prefix web run typecheck
 npm --prefix web test -- --run
fi
npm --prefix web run build
node scripts/web-assets.mjs validate
