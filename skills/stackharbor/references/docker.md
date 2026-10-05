# Docker preflight and recovery

Use this for any app whose startup scripts or registrations depend on Docker/Compose. Skip it for apps with no Docker dependency. Distinguish four layers: installed CLI, Compose plugin, reachable engine, ready containers.

## Read-only preflight

1. Read the actual startup scripts and Compose files. Record required service names, Compose file/project identity, healthchecks, and whether resources are managed or observed. Do not print expanded Compose environment values or secrets.
2. Check `command -v docker` and `docker compose version`. A missing executable, a missing Compose plugin, and an unreachable daemon require different remedies.
3. Inspect `docker context show` and the selected endpoint (accounting for `DOCKER_CONTEXT` and `DOCKER_HOST`) without dumping credentials. Verify it is the intended local runtime. Do not switch contexts, unset endpoint variables, or start a local engine to hide a remote connection failure.
4. Probe `docker info --format '{{.ServerVersion}}'`. Use the execution tool's timeout: at most 5 seconds per probe, interrupt a hanging probe, and allow at most 60 seconds total for startup polling. Avoid full `docker info` output. Failure does not always mean stopped: classify connection refused/missing socket, permissions, TLS, and remote connectivity separately.
5. Once the engine is reachable, inspect only the declared Compose identity with `docker compose -f FILE -p PROJECT ps --all --format json`. Use `available: healthy` only when a real healthcheck exists. Engine readiness does not prove that a database is healthy, migrations succeeded, or an app is ready.

## Recovery within the requested scope

- **Registration or inspection only:** keep operations read-only. Report missing runtime prerequisites and the exact next action; YAML can still be prepared and validated.
- **User requested local application startup:** start the established prerequisite runtime when it is known and already installed. On macOS with Docker Desktop confirmed, use `open -a Docker`, then bounded engine probes. The window opening or process existing is not success. Do not install or switch to another runtime as a side effect.
- **Colima, OrbStack, or another existing runtime:** use that runtime's documented startup command after confirming which runtime owns the selected endpoint. Never launch competing runtimes blindly.
- **Missing installation or first-run setup:** explain the specific missing dependency. Installation requires user intent covering that dependency; first-run legal agreements and authentication remain user decisions. Do not accept terms automatically.
- **Linux:** report the actual service/socket failure and follow the established engine management mechanism. Do not assume passwordless sudo, alter socket permissions, or add the user to privileged groups as a repair.
- **Startup timeout or probe error:** stop the sequence, report the last classified error and the blocked applications, and do not run migrations or dependent services.

## Register dependencies so future starts work

For v2, declare each real Compose dependency as a resource with exact file/project/service, `control`, `lifetime`, and availability; connect service/task `requires` edges to `resource/NAME` with `condition: available`. Follow [the full v2 contract](v2.md), including explicit registration of transitive Compose dependencies. Databases commonly use `lifetime: persistent`; do not invent healthchecks or identity values. Observed resources are not started or stopped by StackHarbor.

For v1, use the existing `docker_depends_on` contract when appropriate. Do not leave database/Redis prerequisites only in prose. Managed declared containers are started in dependency order by StackHarbor, but the CLI itself does not launch Docker Desktop. `stackharbor doctor` is not a generic engine checker; it executes project-defined task checks. `validate` and `plan` remain read-only declaration checks.

After engine readiness, let the declared plan start only the required managed resources and wait for their readiness before tasks/services. Do not run an unscoped `docker compose up -d` that also starts unrelated apps or migration services. Do not stop Docker Desktop or delete volumes when quitting StackHarbor.

## Container resource metrics (v0.2.0+)

StackHarbor samples running container IDs with batched `docker stats --no-stream --no-trunc --format '{{json .}}' ID...`. In v2, only registered Compose resources are sampled. It triggers the background observer every two seconds; command time affects the actual interval. Container events indicate lifecycle changes, not CPU/memory changes, so this version uses periodic samples rather than a continuous stream.

For missing values, first verify the executable version and declared Compose identity. Inspect the scoped `compose ps` output for a running container ID, then run the stats command with those explicit IDs and the same selected context/endpoint. Keep the command bounded and inspect the metric error shown in the TUI. Do not omit IDs and accidentally sample every container on the engine. Sampling failure clears stale metrics but does not change independently observed health or availability.

`—` means no valid current sample; `0 B` and `0.0%` are valid readings. Memory follows Docker CLI working-set accounting, which subtracts cache on Linux and is distinct from host process RSS; CPU can exceed 100%. Compare against the same `docker stats` accounting rather than Docker Desktop's total VM memory. See [Docker stats](https://docs.docker.com/reference/cli/docker/container/stats/).

## Completion evidence

Report CLI/Compose availability, intended local context, successful engine probe or exact failure, required resource states, application readiness, and any unverified migration contract. For a registration-only task, explicitly say that actual resource/application startup was not performed.

Review scenarios: CLI missing; Compose missing; Desktop installed but stopped; engine slow to start; engine timeout; wrong/remote context; permission denied; engine up but database unhealthy; observed resource unavailable; no Docker dependency. A successful CLI version command must never satisfy these cases by itself.

References: [Docker info](https://docs.docker.com/reference/cli/docker/system/info/), [Docker Desktop for Mac](https://docs.docker.com/desktop/setup/install/mac-install/).
