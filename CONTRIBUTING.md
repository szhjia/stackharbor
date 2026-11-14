# Contributing

StackHarbor welcomes reproducible bug reports, focused fixes, and improvements to documentation and agent skills.

## Development

Use macOS or Linux with Go 1.26 or newer, Git, and Make. Install `lsof` for port ownership checks; Linux also needs `procps`. Docker is optional and needed only for Compose integration.

```sh
git clone https://github.com/szhjia/stackharbor.git
cd stackharbor
make demo
make check
```

`make demo` builds the CLI and opens the sample workspace. Services start only when requested. `make check` runs formatting checks, vet, unit/integration tests, and the race detector. Tests create disposable processes and listeners. PostgreSQL/Docker integration coverage requires the corresponding test environment; a skipped integration test is not runtime verification.

See [Development and repository layout](docs/development.md) for directory responsibilities, the native Go test framework, and local artifact conventions. Keep package tests beside their implementation; reserve `tests/` for CLI and cross-package integration coverage.

## Changes

Open an issue describing the problem before proposing a large feature. Keep patches scoped, add a regression test for behavioral fixes, and update both README languages when changing user-facing instructions. Protocol changes must update examples and `skills/stackharbor/references/` together. Do not commit credentials, machine-specific paths, generated binaries, or runtime logs.

Pull requests should describe the problem, behavior after the change, and verification performed. Preserve explicit lifecycle actions, process ownership checks, bounded cleanup, and truthful unknown states.

## Releases

Maintain `CHANGELOG.md` as the source of user-facing release notes: put upcoming changes under `Unreleased`, then move them into a dated version entry when preparing a release. Group entries by `Added`, `Changed`, `Fixed`, `Deprecated`, `Removed`, or `Security` as applicable. Include compatibility, upgrade steps, and known limitations when they affect users. Preserve earlier release entries and add comparison links. The entry date must match the intended publication date; refresh it if publication is delayed.

Update `internal/buildinfo/version.go` to the same version. `make release` reads that source version by default; `make release VERSION=0.2.0` makes the intended version explicit. Packaging rejects a mismatched version and generates `dist/RELEASE_NOTES.md` from only the selected changelog entry. That file is included in each archive and used for the GitHub Release body. Preview notes without building using `sh scripts/release-notes.sh 0.2.0`.

Run `make check`, package both Linux CLI archives, run the opt-in distribution tests in [the development guide](docs/development.md#tests-and-checks), and verify `SHA256SUMS`. Public macOS delivery is app-only: build native Apple Silicon and Intel DMGs with the existing Developer ID team, verify signatures and notarization tickets, and complete [native installation/lifecycle/performance acceptance](docs/desktop.md). Internal CI artifacts are ad-hoc test builds and never public release inputs.

The tag release workflow waits for a protected `macos-release` environment job to produce both verified signed DMGs before creating the GitHub Release with Linux archives, native metadata and combined checksums. Configure that environment with required reviewers, tag restrictions and the six documented Apple secrets; a workflow file cannot itself create or prove those repository protections. Missing credentials fail the release rather than falling back to internal artifacts. The checked workflow imports a temporary CI keychain and removes it afterward. Ordinary PR/branch desktop CI receives no signing secrets and stays internal.

Commit reviewed release contents before tagging. Maintainers publish `vMAJOR.MINOR.PATCH` tags only after required gates pass. Changing a version or building locally does not publish. Do not bypass Gatekeeper or notarization checks.

Contributions are licensed under the repository's MIT license.
