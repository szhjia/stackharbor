#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
# This disposable container is independent of the parent project's Compose stack.
docker run --rm --init -v "$root:/src:ro" golang:1.26-bookworm sh -eu -c '
  apt-get update -qq
  apt-get install -y -qq lsof procps >/dev/null
  mkdir -p /tmp/stackharbor/source
  cp -R /src/. /tmp/stackharbor/source/
  cd /tmp/stackharbor/source
  ./scripts/check.sh
  archive=dist/stackharbor_0.1.0_linux_arm64.tar.gz
  if [ "$(go env GOARCH)" = amd64 ]; then archive=dist/stackharbor_0.1.0_linux_amd64.tar.gz; fi
  if [ -f "$archive" ]; then
    mkdir -p /tmp/stackharbor/bundle
    tar -xzf "$archive" -C /tmp/stackharbor/bundle
    /tmp/stackharbor/bundle/stackharbor --version
    SH_TEST_BINARY=/tmp/stackharbor/bundle/stackharbor go test ./tests -count=1 -timeout 120s
  fi
'
