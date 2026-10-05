#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root"
version=${1:-0.1.0}
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then printf 'Invalid release version\n' >&2; exit 2; fi
commit=$(git rev-parse --short HEAD)
mkdir -p dist
stage=$(mktemp -d "${TMPDIR:-/tmp}/stackharbor-release.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  goos=${target%/*}; goarch=${target#*/}
  archive="stackharbor_${version}_${goos}_${goarch}.tar.gz"
  printf 'Building %s\n' "$archive"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "-s -w -X github.com/szhjia/stackharbor/internal/buildinfo.Version=$version -X github.com/szhjia/stackharbor/internal/buildinfo.Commit=$commit" -o "$stage/stackharbor" ./cmd/stackharbor
  cp .stackharbor-tool LICENSE README.md README.zh-CN.md CHANGELOG.md CONTRIBUTING.md SECURITY.md THIRD_PARTY_NOTICES "$stage/"
  cp -R skills examples licenses "$stage/"
  mkdir -p "$stage/scripts" "$stage/docs"
  cp docs/protocol-v2.md docs/usage.zh-CN.md "$stage/docs/"
  cp -R docs/diagrams docs/screenshots "$stage/docs/"
  cp scripts/install.sh scripts/install-skill.sh scripts/install-release.sh "$stage/scripts/"
  COPYFILE_DISABLE=1 tar -czf "dist/$archive" -C "$stage" stackharbor .stackharbor-tool LICENSE README.md README.zh-CN.md CHANGELOG.md CONTRIBUTING.md SECURITY.md THIRD_PARTY_NOTICES skills examples licenses scripts docs
  if [ "$goos" = "$(go env GOOS)" ] && [ "$goarch" = "$(go env GOARCH)" ]; then native=$(mktemp dist/.stackharbor.XXXXXXXX); cp "$stage/stackharbor" "$native"; chmod 755 "$native"; mv -f "$native" dist/stackharbor; fi
done
(cd dist; if command -v sha256sum >/dev/null 2>&1; then sha256sum "stackharbor_${version}_"*.tar.gz > SHA256SUMS; else shasum -a 256 "stackharbor_${version}_"*.tar.gz > SHA256SUMS; fi)
