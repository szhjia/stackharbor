#!/bin/sh
# Download a verified release. Never uses sudo or replaces an existing command.
set -eu
if [ "${1:-}" = --help ]; then
  printf 'Usage: sh install-release.sh [VERSION [BIN_DIR]]\nDefaults: latest, ~/.local/bin. Supports macOS/Linux arm64/amd64.\n'
  exit 0
fi
if [ "$#" -gt 2 ]; then printf 'Expected [VERSION [BIN_DIR]]\n' >&2; exit 2; fi
version=${1:-latest}
bin_dir=${2:-"$HOME/.local/bin"}
repo=https://github.com/szhjia/stackharbor
case "$(uname -s)" in Darwin) platform=darwin;; Linux) platform=linux;; *) printf 'Unsupported operating system\n' >&2; exit 1;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) printf 'Unsupported architecture\n' >&2; exit 1;; esac
for tool in curl tar; do command -v "$tool" >/dev/null 2>&1 || { printf 'Required command: %s\n' "$tool" >&2; exit 1; }; done
if command -v sha256sum >/dev/null 2>&1; then checksum=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum=shasum
else printf 'Required command: sha256sum or shasum\n' >&2; exit 1; fi
if [ -e "$bin_dir/stackharbor" ] || [ -L "$bin_dir/stackharbor" ]; then
  printf '%s already exists; installation left it unchanged.\n' "$bin_dir/stackharbor" >&2; exit 1
fi
if [ "$version" = latest ]; then
  metadata=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' https://api.github.com/repos/szhjia/stackharbor/releases/latest)
  version=$(printf '%s\n' "$metadata" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
fi
version=${version#v}
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then printf 'Expected a stable version such as 0.1.0\n' >&2; exit 2; fi
stage=$(mktemp -d "${TMPDIR:-/tmp}/stackharbor-install.XXXXXXXX")
temporary=
trap 'rm -rf "$stage"; if [ -n "$temporary" ]; then rm -f "$temporary"; fi' EXIT HUP INT TERM
archive="stackharbor_${version}_${platform}_${arch}.tar.gz"
base="$repo/releases/download/v$version"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$base/$archive" -o "$stage/$archive"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' "$base/SHA256SUMS" -o "$stage/SHA256SUMS"
expected=$(awk -v name="$archive" '$2 == name {print $1}' "$stage/SHA256SUMS")
if ! printf '%s\n' "$expected" | LC_ALL=C grep -Eq '^[a-fA-F0-9]{64}$'; then printf 'Missing or invalid release checksum\n' >&2; exit 1; fi
if [ "$checksum" = sha256sum ]; then actual=$(sha256sum "$stage/$archive" | awk '{print $1}')
else actual=$(shasum -a 256 "$stage/$archive" | awk '{print $1}'); fi
if [ "$actual" != "$expected" ]; then printf 'Release checksum mismatch; nothing installed.\n' >&2; exit 1; fi
tar -xzf "$stage/$archive" -C "$stage" stackharbor
if [ ! -f "$stage/stackharbor" ] || [ -L "$stage/stackharbor" ]; then printf 'Invalid release binary\n' >&2; exit 1; fi
mkdir -p "$bin_dir"
temporary=$(mktemp "$bin_dir/.stackharbor.XXXXXXXX")
cp "$stage/stackharbor" "$temporary"
chmod 755 "$temporary"
ln "$temporary" "$bin_dir/stackharbor"
printf 'Installed StackHarbor %s to %s/stackharbor\n' "$version" "$bin_dir"
case ":$PATH:" in *":$bin_dir:"*) ;; *) printf 'Add this directory to PATH: export PATH="%s:$PATH"\n' "$bin_dir";; esac
printf 'Run stackharbor from your project root.\n'
