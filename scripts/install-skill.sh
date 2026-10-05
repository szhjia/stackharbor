#!/bin/sh
set -eu
if [ "$#" -gt 1 ]; then printf 'Usage: sh %s [SKILLS_DIR]\n' "$0" >&2; exit 2; fi
if [ "${1:-}" = --help ]; then
  printf 'Usage: sh %s [SKILLS_DIR] (default: ~/.agents/skills)\nLinks to this checkout or permanent extracted release; updates follow that directory.\n' "$0"
  exit 0
fi
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
source="$root/skills/stackharbor"
for file in SKILL.md references/registration.md references/v2.md references/maintenance.md references/installation.md references/docker.md; do
  if [ ! -f "$source/$file" ]; then
    printf 'Incomplete StackHarbor skill: missing %s\n' "$source/$file" >&2
    exit 1
  fi
done
skills_dir=${1:-"$HOME/.agents/skills"}
mkdir -p "$skills_dir"
skills_dir=$(CDPATH= cd -- "$skills_dir" && pwd -P)
target="$skills_dir/stackharbor"
if [ -L "$target" ] && [ "$(readlink "$target")" = "$source" ]; then
  printf 'Already linked: %s -> %s\n' "$target" "$source"
  exit 0
fi
if [ -e "$target" ] || [ -L "$target" ]; then
  printf '%s already exists; installation left it unchanged.\n' "$target" >&2
  exit 1
fi
# Use the parent directory as destination: a concurrent stackharbor directory
# must cause EEXIST, not redirect the link into that directory.
ln -s "$source" "$skills_dir/"
printf 'Installed %s -> %s\nKeep the source directory; open a new agent session to discover the skill.\n' "$target" "$source"
