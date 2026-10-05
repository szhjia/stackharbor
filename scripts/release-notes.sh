#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  printf 'Usage: sh release-notes.sh VERSION [CHANGELOG_FILE]\n' >&2
  exit 2
fi
version=$1
changelog=${2:-"$root/CHANGELOG.md"}
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  printf 'Expected a release version in MAJOR.MINOR.PATCH format\n' >&2
  exit 2
fi
# Buffer the section until validation succeeds: never emit partial notes for a
# missing, duplicate, undated or empty release entry.
awk -v version="$version" '
  /^## / {
    selected = ($2 == "[" version "]")
    if (selected) {
      matches++
      if ($3 != "-" || $4 !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) invalid = 1
    }
    next
  }
  selected {
    if ($0 ~ /^\[[^]]+\]:/) next
    body = body $0 "\n"
    if ($0 ~ /[^[:space:]]/ && $0 !~ /^### /) content = 1
  }
  END {
    if (matches != 1 || invalid || !content) {
      print "Expected one dated, non-empty changelog entry for " version > "/dev/stderr"
      exit 1
    }
    sub(/^\n+/, "", body)
    sub(/\n+$/, "", body)
    print "# StackHarbor v" version "\n"
    print body
    print "\nFull changelog: https://github.com/szhjia/stackharbor/blob/v" version "/CHANGELOG.md"
  }
' "$changelog"
