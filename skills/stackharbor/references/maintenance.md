# Installation, upgrades, and maintenance

The authoritative implementation of this skill is `skills/stackharbor/` in the StackHarbor repository. Maintain it together with the CLI, parser, and protocol references in the same change. Registration files in other projects depend only on the StackHarbor command and paths within their own project; they must not refer to absolute paths in the tool's source tree.

## User-level installation

Install from the public GitHub repository: `npx skills add szhjia/stackharbor --skill stackharbor -g`. This downloads the complete skill directory. Later, use `npx skills update stackharbor -g` to update only this skill; check for local customizations before upgrading. skills.sh displays repository skills, but its page index may lag behind source updates. Installing a link to the source, as described below, is another entry point.

Run this from the source root or a release extraction directory that you will **keep permanently**:

```sh
sh scripts/install-skill.sh
```

By default, this creates an absolute symlink from `~/.agents/skills/stackharbor` to `skills/stackharbor` in the current source directory. Installation requires no binary and installs no application dependencies. Relative reference links throughout the directory remain valid. Reinstalling from the same source succeeds; the installer refuses to overwrite an existing directory, file, or symlink to another source, including a broken symlink. Inspect the existing content first, then let the user decide whether to replace it.

Codex and compatible agents discover user skills through `~/.agents/skills`. If the actual runtime reads only a dedicated directory, pass that directory, for example:

```sh
sh scripts/install-skill.sh "$HOME/.codex/skills"
```

Choose the entry point your runtime actually uses and avoid maintaining multiple copied implementations. After installation, check with `readlink "$HOME/.agents/skills/stackharbor"` and `test -r "$HOME/.agents/skills/stackharbor/SKILL.md"`, then start a new session. Example trigger: “Use the stackharbor skill to integrate the current project with StackHarbor.”

## Upgrades

After a source update, the link immediately reads the new skill and references without copying them again; an existing agent session may need to reload them. **The skill link does not automatically update the binary**: update the binary separately according to the release instructions and check `stackharbor --version` and `--help`. Use an explicit release tag and checksum manifest; verify capabilities against the actual commands and validate/plan results.

Release installation: the skill is included in each release package. If you extract releases into a fixed directory, you can link that directory and update its contents during upgrades. If you use separate extraction directories for each version, explicitly move the skill entry point to the new directory. Do not delete the link's source or link to a temporary extraction directory. Moving the source tree breaks the old link; verify who owns the old entry point, then reinstall from the new source. The installer will not replace it automatically.

## Maintenance alongside the tool

1. When changing YAML fields, CLI commands, node IDs, exit codes, or lifecycles, update `SKILL.md`, the relevant `references/`, examples, and regression checks together.
2. Run baseline application scenarios before changing behavior, then have an independent agent use the skill to complete the same scenarios afterward. Pay particular attention to preserving existing workspaces, handling missing migration contracts, respecting read-only validation boundaries, and upgrade paths.
3. `sh scripts/check.sh` checks the actual installer, validate/plan for embedded YAML, and all repository tests; `sh scripts/build-release.sh VERSION` includes the same skill and installer in the releases for all four platforms.
4. Review and commit the relevant files; push or publish only with user authorization. Do not copy another protocol reference into other projects.
