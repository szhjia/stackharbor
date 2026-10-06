# Development and repository layout

StackHarbor is a Go foreground application with an embedded React/TypeScript/Vite browser console. Source builds need Node 26.9.0/npm (see `.node-version`) and Go 1.26+. Its module root is the repository
root, and its implementation uses `cmd/` and `internal/`, following the
[official Go module layout guidance](https://go.dev/doc/modules/layout).
There is no requirement for a `src/` wrapper or a public `pkg/` directory.

## Directory responsibilities

```text
stackharbor/
├── go.mod, go.sum          # Module identity and dependency checksums
├── cmd/stackharbor/        # Executable entry point
├── internal/              # Application packages and their unit tests
│   ├── buildinfo/          # Source version and build metadata
│   ├── cli/                # Commands, flags, and output
│   ├── config/             # YAML loading and validation
│   ├── discovery/          # Workspace and project discovery
│   ├── docker/             # Compose control and container metrics
│   ├── graph/              # Dependency planning
│   ├── logs/               # Bounded, sanitized log storage
│   ├── model/              # Shared configuration and runtime types
│   ├── observe/            # Host process metrics, ports, and health
│   ├── process/            # Process inspection
│   ├── runner/             # Foreground process ownership and cleanup
│   ├── supervisor/         # Session lifecycle and coordination
│   ├── control/sessionapi/sessionhost/ # Shared controller and private session transport
│   ├── inventory/web/      # Physical inventory and authenticated loopback gateway
│   └── tui/                # Terminal interaction and rendering
├── web/                   # Locked frontend sources, tests and shadcn/ui components
├── tests/                 # Cross-package, CLI, PTY, installer and release tests
├── docs/                  # Usage, protocol, development docs and illustrations
├── examples/              # Runnable demos and registration examples
├── scripts/               # Checks, packaging and installation
├── skills/stackharbor/     # Distributed agent skill and its references
├── licenses/              # Third-party license texts
├── .github/               # CI, release workflow and contribution templates
└── dist/                  # Ignored build and release outputs
```

`README.md`, `README.zh-CN.md`, `LICENSE`, `CHANGELOG.md`, `CONTRIBUTING.md`,
`SECURITY.md`, `THIRD_PARTY_NOTICES`, and `Makefile` are intentional root entry
points. `.stackharbor-tool` is a functional discovery marker that also travels
with release archives. Moving these files solely to reduce the root file count
would obscure their purpose or require changes to installation and packaging.

Go unit tests stay beside the package they test as `*_test.go`. The top-level
`tests/` package exercises the built CLI and behavior across package boundaries;
it is not a replacement for package tests. The demos have their own modules so
they can run independently; the check script also checks these modules.

## Tests and checks

Go includes the standard `testing` framework. No extra test framework needs to
be installed. Run a focused package test while working, then the repository's
checks before proposing a release:

```sh
make web-build
go test ./internal/docker ./internal/supervisor ./internal/tui -count=1
make check
```

`make check` installs locked npm dependencies, checks frontend types/tests, builds and validates matching embedded assets/licenses, then verifies Go formatting, runs `go vet`, runs the main module's tests
and race detector, then tests every example module. Integration tests create
disposable processes and listeners. Some environment-dependent checks, including
release archive and PostgreSQL checks, are opt-in; see each test's skip message.

After packaging, verify the distribution as well:

```sh
make release VERSION=0.2.0
STACKHARBOR_RELEASE_TEST=1 STACKHARBOR_RELEASE_VERSION=0.2.0 \
  go test ./tests -run '^TestRelease' -count=1
(cd dist && shasum -a 256 -c SHA256SUMS)
```

The local Docker Linux check is `sh scripts/test-linux.sh`. It uses a disposable
container independently of the application's Compose stack. Cross-compilation
alone does not verify Linux runtime behavior.

## Local files and document organization

The visible checkout can contain more directories than the tracked repository:

| Local directory | Purpose | Version control |
| --- | --- | --- |
| `dist/` | Executables, platform archives, checksums and generated release notes | Ignored |
| `.superpowers/`, `.v2c/`, `.video_agent/` | Agent or plugin working files | Ignored |
| `_runtime_verification/` | Local runtime verification code | Ignored |
| `docs/verification/` | Local verification evidence | Ignored |
| `docs/superpowers/` | Agent design and implementation notes | Ignored |

Keep generated files in `dist/` and session state in the OS user-cache location
(or `STACKHARBOR_CACHE_DIR`). Local verification artifacts should converge on one
ignored directory, such as `.cache/verification/`, when their owners and
consumers can be migrated together. Do not delete existing evidence as part of a
layout change.

The protocol specification, proposal, implementation plan, and local design
notes currently coexist under `docs/`. A future document cleanup can put stable
design explanations under `docs/architecture/` and historical proposals under
`docs/archive/`, while retaining direct usage and protocol entry points. Any
relocation must update README links, script inputs, and archive contents. The
current directory review does not relocate those files.

Do not move the implementation into `src/`: that would add a path layer to
imports, build targets and integration-test commands without improving the
existing package boundaries. Add a public package or separate module only when
there is an actual external consumer.

## Embedded frontend and integration verification

See [Web/CLI control](web-control.md) for source builds and the exact Vite proxy opt-in. `make build/check/release` are supported freshness paths. `node scripts/web-assets.mjs validate` refuses stale source/version/protocol or modified bundle files. `node scripts/frontend-licenses.mjs --check` validates retained texts; regenerate with the same command without `--check` when dependencies change. react-remove-scroll-bar 2.3.8 omits its license from npm: the retained MIT text comes from upstream LICENSE snapshot `8ca9ba5ea52de03308fe8ced94f7b159a44d28ff` (npm release gitHead is unavailable upstream).

`TestWebControlTwoPTYWorkspacesWithoutGateway` runs two real PTYs, CLI control, gateway inventory, plan conflicts, accepted-request disconnect recovery, legacy read-only metadata, gateway-only teardown and terminal restoration. `STACKHARBOR_CAFE_TEST=1` enables original Harbor Café lifecycle on 18281/18282 and refuses occupied ports. `STACKHARBOR_DOCKER_TEST=1` enables a unique Compose Redis fixture, named-volume persistence and container-replacement conflict; only that generated project is removed. Default checks skip those two opt-in environmental integrations. Neither uses existing developer containers or data.
