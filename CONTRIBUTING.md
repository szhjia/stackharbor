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

## Changes

Open an issue describing the problem before proposing a large feature. Keep patches scoped, add a regression test for behavioral fixes, and update both README languages when changing user-facing instructions. Protocol changes must update examples and `skills/stackharbor/references/` together. Do not commit credentials, machine-specific paths, generated binaries, or runtime logs.

Pull requests should describe the problem, behavior after the change, and verification performed. Preserve explicit lifecycle actions, process ownership checks, bounded cleanup, and truthful unknown states.

## Releases

Maintainers publish stable `vMAJOR.MINOR.PATCH` tags after the checks on `main` pass. The release workflow tests on macOS and Linux, builds four platform archives, verifies their contents, and uploads them with `SHA256SUMS`. Use `make release VERSION=0.1.0` to reproduce the packaging locally. Binaries are not Apple-notarized; do not disable Gatekeeper globally.

Contributions are licensed under the repository's MIT license.
