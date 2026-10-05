#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
if [ "$#" -gt 1 ]; then printf 'Usage: %s [BIN_DIR]\n' "$0" >&2; exit 2; fi
if [ "${1:-}" = --help ]; then printf 'Usage: %s [BIN_DIR] (default: ~/.local/bin)\n' "$0"; exit 0; fi
bin_dir=${1:-"$HOME/.local/bin"}
binary="$root/stackharbor"
if [ ! -x "$binary" ]; then binary="$root/dist/stackharbor"; fi
if [ ! -x "$binary" ]; then printf 'Build or extract the StackHarbor executable before installing.\n' >&2; exit 1; fi
mkdir -p "$bin_dir"
target="$bin_dir/stackharbor"
if [ -e "$target" ] || [ -L "$target" ]; then printf '%s already exists; installation left it unchanged.\n' "$target" >&2; exit 1; fi
temporary=$(mktemp "$bin_dir/.stackharbor.XXXXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
cp "$binary" "$temporary"
chmod 755 "$temporary"
# Publish without overwriting a command created concurrently.
ln "$temporary" "$target"
printf 'Installed %s\nRun stackharbor from your project root.\n' "$target"
case ":$PATH:" in *":$bin_dir:"*) ;; *) printf 'Add %s to PATH before running stackharbor.\n' "$bin_dir";; esac
