# Changelog

## 0.1.0 — Initial public release

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
