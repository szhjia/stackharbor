# Changelog

Notable user-facing changes are recorded here, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and version numbers follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). StackHarbor remains in the 0.x development phase. YAML protocol versions remain independent of executable release versions.

## [Unreleased]

## [0.6.0] - 2026-10-07

### Added

- Console settings for English or Chinese, three text sizes (12px, 13px and
  14px), and light, dark or system-following appearance. Browser preferences
  persist across reloads; compact 12px text is the default.
- `make clean` for generated root `dist/` output.

### Changed

- Consolidated everyday console typography into three sizes and tightened
  navigation, table rows and page spacing to show more data.
- Successful builds keep only the newest rollback binary in `dist/`.
  Successful releases remove archives from other versions after packaging.

### Upgrade notes

- Existing registrations and session protocols remain compatible. Replace and
  restart the installed binary to use the new console; preferences are stored
  locally in each browser. The bundled skill remains at version 0.4.0.

## [0.5.0] - 2026-10-07

### Added

- Bookmarkable console routes for Overview, Workspaces, Resources, Operations
  and workspace tabs, with browser Back/Forward and persistent log targets.
- Shared design tokens and reusable cards, metric lists, entity details,
  responsive tables and console layout, with design-system maintenance guidance.
- StackHarbor brand assets and favicon in the embedded browser console.

### Changed

- Local browsers open the ordinary gateway URL without a one-use launch link.
  Request-protection sessions and CSRF tokens are established and renewed
  automatically; uncertain operation submissions are never automatically retried.
- Workspace and resource tables use compact rows with expandable ownership and
  shared detail popovers. Node actions use menus and retain plan confirmation.

### Fixed

- Closing a node action review restores focus to its initiating action button,
  including when the action menu closes while the review opens.

### Upgrade notes

- Exact Host/Origin checks, cross-site Fetch Metadata rejection and CSRF checks
  remain enforced. The local control gateway is accessible to other local users
  and programs; it does not provide an OS-user authentication boundary.
- Existing YAML registrations and session protocols remain supported. Replace
  the binary to use the new console; running processes retain their loaded
  version until restarted. The bundled skill remains at version 0.4.0.

## [0.4.0] - 2026-10-06

### Added

- Ordered Compose `files`, `project_directory`, and `env_files` for v2 resources,
  with backward-compatible single `file` input and a runnable two-file example.
- Official Compose model validation shared with runtime scope, physical resource
  locking, and configuration drift checks before container actions.

### Changed

- v2 Compose validation now requires the Docker CLI and Compose plugin, even for
  single-file resources; parsing does not require a running engine.
- Compose input or resolved-model changes require reopening the workspace and
  requesting a new plan. Resolved credentials and raw CLI error output are not
  included in public diagnostics.

### Compatibility

- Upgrade the binary and skill together. Published 0.3.0 binaries do not accept
  the new fields; 0.4.0 supports them. The skill checks actual tool capabilities.
- Compose include, profiles, and extends remain unsupported in any input file.
  Projects whose inputs contain these features still need their existing adapter.

## [0.3.0] - 2026-10-06

This release restores the intended 0.x version sequence. The previously published
`v2.0.1` was misnumbered; it did not mark a 2.x stability or compatibility milestone.
`v0.3.0` includes those fixes and is the supported successor to `v0.2.0`.

### Added

- A local React and TypeScript browser console with shadcn/ui components. Run
  `stackharbor web` to open it on 127.0.0.1:16800; use `--port` to choose a port
  or `--port 0 --no-open` for an available port without opening the browser.
- Independent CLI control of existing workspace sessions: status, start, stop,
  restart, explicit port release, logs and operation lookup. CLI control uses
  private Unix sockets and does not require the browser gateway.
- Workspace and shared-resource inventories, live metrics, bounded event/log
  streaming, operation progress and explicit stale or unavailable state.
- Expiring action plans, single-operation confirmation, identity revalidation,
  cross-workspace resource locking and durable close-result lookup.

### Fixed

- Browser credential rotation cannot be overwritten by late responses. Browser
  writes require JSON, same-origin validation and CSRF protection.
- Close confirmation discloses affected nodes and preserved resources; shared
  resource confirmation includes known references from other workspaces.
- Manual sampling tests no longer race the background observer.
- The complete published skill, including protocol and maintenance references,
  is now in English. Skill version 0.3.0 is tracked independently of the executable.

### Upgrade notes

- Existing v1 and v2 YAML registrations remain supported. Restart workspace
  sessions with the new binary to enable control; older sessions are read-only.
- Noninteractive mutations require `--yes`; use `--dry-run` to review a live
  plan. After an uncertain response, query the operation ID before acting again.
- Closing the Web gateway leaves workspace applications running. Closing a
  workspace preserves persistent and observe-only resources and never deletes
  volumes. Sessions remain foreground processes; this release adds no daemon.
- Release binaries embed the browser assets and need neither Node nor Go at
  runtime. Building from source now requires Node 26.9.0 as well as Go 1.26+.
- Frontend production dependency audit reported no advisories during validation;
  seven high build-tool advisory paths remain documented in docs/web-control.md.

## [2.0.1] - 2026-10-05

**Misnumbered historical release.** Its GitHub release and tag are withdrawn;
`v0.3.0` includes these fixes. Continue using the 0.x release sequence.

### Fixed

- External services with successful readiness probes display `Running ext` on
  the Dashboard, sidebar, and project page. A listener without a readiness
  contract displays `Listening`; failed and unknown observations remain distinct.
- The Dashboard separates the total running service count from the number
  managed by the current session. Observation never adopts process ownership.
- Docker / Colima / SSH forwarded endpoints associate with a unique running
  container by TCP published port within the workspace's configured Compose
  scopes. Its memory and CPU join the existing batched stats sample, even when
  that application container is not registered as an infrastructure resource.
- Ambiguous or replicated container matches, incompatible host bindings, and unrelated host listeners are excluded from
  container attribution; failed stats clear stale metrics independently of readiness.

### Upgrade notes

- Existing v1 and v2 YAML registrations continue to work. Preserve the installed
  binary as a backup, install v2.0.1, and restart StackHarbor to load it.
- External Docker metrics describe the container serving the published endpoint,
  not a sum of workers or other containers in the application.
- Missing listeners remain stopped; installation does not start applications.

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

[Unreleased]: https://github.com/szhjia/stackharbor/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/szhjia/stackharbor/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/szhjia/stackharbor/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/szhjia/stackharbor/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/szhjia/stackharbor/compare/v0.2.0...v0.3.0
[2.0.1]: https://github.com/szhjia/stackharbor/commit/4b690f938c3cf4aab52f9cd6e7bce562d50dae90
[0.2.0]: https://github.com/szhjia/stackharbor/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/szhjia/stackharbor/releases/tag/v0.1.0
