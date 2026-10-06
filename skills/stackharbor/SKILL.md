---
name: stackharbor
description: Use when a repository or subproject needs StackHarbor integration, YAML registration, workspace onboarding, migration from an older protocol, Docker readiness or resource metrics troubleshooting, finding or controlling workspace sessions, browser console authentication, service orchestration, skill upgrades, or installation and upgrade of StackHarbor on macOS/Linux from GitHub releases.
metadata:
  version: "0.3.0"
---

# StackHarbor installation and workspace integration

Skill version: **0.3.0**. This skill version is independent of the StackHarbor executable version.

Generate registrations from the project's actual startup contract. The skill and protocol references are maintained together in the StackHarbor repository. StackHarbor supports local foreground processes on macOS/Linux, with distinct lifecycles for resources, tasks, and services.

## Install the application

When the user asks to install StackHarbor, follow [GitHub release installation](references/installation.md). Skill installation and binary installation are separate. Detect macOS/Linux and architecture, install a checksum-verified release without sudo, verify `--version` and `--help`, and report the actual binary path. Do not assume a skill downloaded by `npx skills add` includes the repository's root scripts.

## Docker readiness — do not skip this for Docker-backed apps

Before reporting that a Docker-backed app is ready to start, follow [Docker preflight and recovery](references/docker.md). Check the CLI, Compose plugin, selected context/endpoint, reachable engine, and required containers separately. `docker --version` does not prove the engine is running. `validate` and `plan` do not prove Docker readiness; `doctor` only runs project-declared checks.

When the user has requested starting the local app and its established runtime is Docker Desktop on macOS, starting that existing runtime is part of the requested prerequisite work: launch it, wait for a successful bounded engine probe, then proceed with the declared dependencies. Registration-only work stays read-only. Do not repeatedly ask for the same already-authorized prerequisite. Missing installation, first-run agreements, a remote endpoint, or an unknown runtime must be reported and resolved explicitly. A stopped container and a stopped Docker engine are different failures.

## Integration steps

1. Identify the target project root and read project instructions, existing workspace/registration files, manifests, startup scripts, and Compose configuration. Locate the binary on PATH or at the user-specified path, then run `--version` and `--help` to verify its capabilities. YAML can still be prepared without the binary, but report that validation is incomplete. Installing tool dependencies requires authorization for that action.
2. Run discover and inspect diagnostics, registered nodes, and registration candidates. Trace the actual command invocation chain to confirm the package manager, foreground entry point, cwd, port sources, health paths, stop signals, and dependencies. Record only environment variable names and file paths; avoid reading or printing secret values. Leave unknown modules, ports, or migration states pending confirmation.
3. **Default to v2 for new integrations**: read the [complete v2 contract](references/v2.md) before writing YAML. For existing v1 configurations, preserve the protocol and IDs according to the [v1 reference](references/registration.md); update all dependency references during a full migration. v2 node IDs are `project/service/key`, `project/task/key`, and `resource/key`, with dependency conditions ready/started, succeeded, and available respectively.
4. Prefer a `stackharbor.yaml` alongside each subproject. Place centralized configuration in the root `.stackharbor/` directory and import it explicitly through the workspace. cwd is relative to the registration file and must stay within the target root; workspace root/registrations are relative to the workspace file. Preserve existing imports, exclusions, and discovery settings. When `discover: false`, add each new registration path explicitly. Include configuration in the target project's version control by default; exclude it only when the user requests local-only configuration.
5. Before splitting scripts that combine multiple operations, verify independent foreground services, Compose identities, and check entry points. Register a schema-write task only when actual check/run/verify commands, required_scope, inputs, and the PostgreSQL target lock contract are all available. If any are missing, list the integration gaps. Preserve the existing wrapper, including its startup side effects, or integrate only verified independent services. Do not invent migration checks or modify startup scripts as an incidental change. Use observe for external processes/containers, and specify control/lifetime explicitly for managed resources.
6. For projects using Docker, perform [Docker preflight checks](references/docker.md) and establish the engine and container states. Register actual prerequisites such as databases and Redis in the DAG before validating application services. Skip this step for projects that do not use Docker.
7. Run validate; for v2, also run plan for the target. Check the dependency closure, conditions, and actions individually, and correct configuration errors. Report file changes, nodes/commands/ports, validation results, Docker engine and dependency readiness, unresolved items, and the TUI startup command. Successful configuration validation proves only that the declarations are valid. Mark actual startup/readiness as unverified if it has not been exercised.

## Quick reference

| Purpose | Command (replace ROOT and NODE with actual values) |
|---|---|
| Discovery | `stackharbor discover --root ROOT --json` |
| Candidate draft | `stackharbor init --root ROOT --project DIR --dry-run` |
| Declaration validation | `stackharbor validate --root ROOT --json` |
| v2 read-only plan | `stackharbor plan start --root ROOT --target NODE --json` |
| Active sessions | `stackharbor sessions --json`; add `--root ROOT` to filter by workspace |
| Focus an existing window | `stackharbor sessions --focus PID` (macOS Terminal) |
| Close workspace sessions | `stackharbor kill --root ROOT --yes` (explicit noninteractive close; preserves persistent resources) |
| Interactive entry point | `stackharbor --root ROOT`, or run `stackharbor` in the project root |

For a non-default workspace, add `--workspace FILE` to every command; ROOT must match the workspace root. init currently generates v1 drafts. Use `--write` only when the user accepts v1 and the candidate is confirmed; do not overwrite existing files. Generate new v2 integrations manually, then validate them.

Keep registration work read-only: use discover/init --dry-run/validate/plan and Docker preflight observation when needed. doctor executes check scripts; task run executes tasks and their resource prerequisites. Starting services, running migrations, installing dependencies, and freeing ports used by external processes require authorization for those actions. StackHarbor does not provide a permission sandbox.

## Session and container metrics troubleshooting (v0.2.0+)

First verify `--version` and `--help` on the actual binary. `sessions` lists sessions without modifying them. If the workspace already has a session, use the reported PID/TTY to locate its window. `--focus` supports only macOS Terminal; locate sessions manually from the list on other terminals and Linux. Closing sessions requires a user request to stop or close that workspace. `kill --root ROOT` triggers a normal exit and cleans up services owned by the sessions. Do not use it for routine registration or observation, or as a substitute for freeing ports used by external processes.

The Dashboard, resource details, and Docker panel display container memory/CPU usage. v2 samples registered Compose resources and external application containers uniquely matched by TCP published ports within the same configuration scope. Engine/container readiness observation and metrics sampling are independent. Running ext means an external endpoint is ready; Listening confirms only a listener; Session separately indicates management by the current session. Observation does not take control of processes. External container metrics cover only the matched endpoint container, without aggregating workers. When metrics are missing, follow [Docker metrics troubleshooting](references/docker.md#container-resource-metrics-v020) to check the selected context, container IDs, and `docker stats`, and retain error evidence. `—` means there is no valid current sample; `0` is a valid zero value. Missing metrics alone do not prove that a container has stopped.

## Minimal v2 example

After verifying that the project uses `pnpm dev` and the application is configured to listen on port 3000, place this file in the application directory:

```yaml
version: 2
project: {id: web, name: Web}
context: {cwd: .}
services:
  dev:
    run: {command: [pnpm, dev]}
    ports: [{name: http, port: 3000}]
    ready: {tcp: "127.0.0.1:3000"}
    open: "http://127.0.0.1:3000/"
```

`web/service/dev` is the plan target. Commands use argv arrays; shell syntax requires an explicit shell invocation.

## Installation and upgrades

Install from the public repository with `npx skills add szhjia/stackharbor --skill stackharbor`. Update an existing global skill individually with `npx skills update stackharbor -g`. From the StackHarbor source tree or a release extracted to a permanent directory, you can also run `sh scripts/install-skill.sh` to link the entire directory to `~/.agents/skills/stackharbor`; new sessions discover the skill. Upgrade the skill and binary separately. See the [maintenance reference](references/maintenance.md) for installation entry points, conflict handling, upgrades, and maintenance checks.

## Common mistakes

| Mistake | Correct contract |
|---|---|
| Assuming the Docker CLI alone makes the app ready to start | Verify the Compose plugin, current engine reachability, and declared container health |
| Starting the app as soon as Docker Desktop opens | Wait for a successful engine probe, then start resources in DAG order and wait for readiness |
| Treating `—` memory/CPU as proof that a container has stopped | Check container state and metrics errors separately; preserve valid zero values |
| Mentioning databases/Redis only in documentation | In v2, register resources explicitly and connect requires; in v1, use docker_depends_on |
| Treating ports declarations as application configuration | Application arguments/environment determine the actual ports |
| Turning candidate scripts directly into executable nodes | Read the actual invocation chain, register the nodes, and validate them first |
| Treating migrations as persistent services or inventing checkers | Use verified v2 task contracts and report gaps explicitly |
| Adding only a local YAML file when discovery is disabled | Update the existing workspace's explicit imports |
| Expecting a copied skill to follow source upgrades automatically | Install a symlink to the entire directory and retain the source directory |

## Browser and session CLI control

For existing foreground sessions, follow [control and recovery](references/web-control.md). Verify the actual binary help before using new commands. CLI control goes directly to the session Unix socket. Browser gateway startup is `stackharbor web` (loopback16800; `--port 0 --no-open` for a free port and launch URL), and stopping it preserves applications. Noninteractive mutation requires `--yes` within user-authorized scope; `--dry-run` previews a live60-second plan. Use `operations --id` after timeout or disconnect and never retry an unknown outcome blindly. Keep cache namespace, canonical root, persistence/observe boundaries, plan expiry and shared consumers explicit.
