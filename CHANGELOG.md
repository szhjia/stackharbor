# Changelog

Notable user-facing changes are recorded here, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and version numbers follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). StackHarbor remains in
the 0.x development phase.

## [Unreleased]

## [0.2.0] - 2026-10-05

This release adds live container resource metrics and commands for finding and
closing existing workspace sessions. Existing v1 and v2 registrations continue
to work without a YAML migration.

### Added

- Docker memory and CPU usage on the Dashboard, infrastructure details, and
  Docker panel. Samples target running container IDs in batches; v2 sampling is
  limited to registered Compose resources.
- `stackharbor sessions` and `sessions --json` to list active workspace sessions
  with their workspace path, PID, terminal, TTY, and start time when observable.
- `stackharbor sessions --focus PID` to locate an active session in macOS Terminal.
- `stackharbor kill` and `kill --root PATH` to request normal shutdown of the
  selected workspace's session and its session-owned services.

### Changed

- Opening an occupied workspace reports its existing session and attempts to
  locate its macOS Terminal tab. Other terminals and Linux receive session
  details for manual navigation.
- GitHub release notes contain only the selected version's changes and upgrade
  guidance. Download archives include these notes alongside the full changelog.
- Release packaging uses the source version by default and rejects a mismatched
  version or a missing, duplicate, empty, or undated changelog entry.
- Bundled agent guidance covers session management, scoped container metrics
  troubleshooting, and selective skill updates from the public repository.

### Fixed

- Available Docker resources no longer show blank memory and CPU fields simply
  because they have no host process tree. Valid zero values remain visible;
  failed samples clear stale values and report a reason without changing the
  separately observed container health or availability.
- Closed sessions and leftover lock files are excluded from the active-session
  list. Session shutdown verifies process identity and lock ownership before
  sending a signal.

### Upgrade notes

- Keep existing workspace and service YAML files; this release does not change
  their schema. Restart StackHarbor to load the new executable.
- The installer intentionally refuses to overwrite an existing executable.
  Preserve the old binary in a backup location before installing v0.2.0, or
  install into a separate directory and update your PATH.
- The release provides macOS and Linux archives for arm64 and amd64, plus a
  `SHA256SUMS` manifest. Verify the matching archive before installation.

### Known limitations

- Docker metrics use periodic `docker stats --no-stream` sampling. The background
  observer triggers every two seconds; command duration affects the actual
  interval. This release does not maintain a continuous Docker stats stream.
- Docker CLI memory usage differs from host process RSS: on Linux, the CLI
  subtracts cache from container memory usage. CPU usage can exceed 100%.
- Automatic window location supports macOS Terminal; other terminals and Linux
  can use the reported PID and TTY. Window automation may require macOS permission.
- Windows, background supervision, automatic restart, and hot reload remain
  unsupported. macOS binaries are not Apple-notarized.

## [0.1.0] - 2026-10-05

Initial public release.

### Added

- Protocol v2 explicit resource/task/service DAG, dependency conditions, task completion and blocked results.
- Checked migrations with PostgreSQL target locks, verification, frozen inputs and structured drift diagnostics.
- Dependency-order navigation, scoped persistent-resource controls and bounded operation/attempt history.
- Read-only plan and explicit doctor checks and explicit task run/history commands; v1 registrations remain supported.

- Shared up/down project and Docker navigation; left/right selects containers.
- Docker engine preflight and recovery guidance in the agent skill.
- English and Chinese guides with project philosophy and real macOS Terminal screenshots.
- Runnable Harbor Café example and a sidebar divider for terminals without background colors.
- Verified GitHub release installer, Make build/demo commands, and automated release publishing.

- One-time user command installer; launch with `stackharbor` from a project root and use its default workspace automatically.
- macOS/Linux foreground service manager with project YAML registrations and optional workspace imports.
- Bounded monorepo discovery, dependency ordering, manual start/stop/restart and exclusive root sessions.
- Dashboard, project/service logs, port ownership, process RSS/CPU and loopback readiness.
- Owned process identity checks, observed descendant cleanup, bounded logs and terminal exit restoration.
- Read-only discovery/validation JSON, safe init drafts, portable demos and reusable registration skill.
- Four platform archives; native macOS and Linux runtime verification. Detached daemons and container ownership are outside this release.

[Unreleased]: https://github.com/szhjia/stackharbor/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/szhjia/stackharbor/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/szhjia/stackharbor/releases/tag/v0.1.0
