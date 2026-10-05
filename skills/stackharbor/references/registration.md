# Registration protocols

For explicit resources, migration tasks and dependency conditions, use [v2](v2.md). Legacy registrations below remain supported.

# StackHarbor registration protocol v1

`stackharbor.yaml`:

```yaml
version: 1
project: {id: shop, name: Shop}
services:
  api:
    cwd: .
    run: {command: [uv, run, server.py]}
    ports: [{name: http, port: 8000}]
    ready: {http: "http://127.0.0.1:8000/health", timeout_seconds: 60}
    stop: {signal: TERM, timeout_seconds: 5}
  web:
    cwd: ../web
    run: {command: [pnpm, dev]}
    env: {PORT: "3000"}
    ports: [{name: http, port: 3000}]
    depends_on: [shop/api]
    ready: {tcp: "127.0.0.1:3000"}
    open: "http://127.0.0.1:3000/"
```

IDs and service keys match `[a-z][a-z0-9_-]*`. Services inherit process environment plus explicit string values. Never put secrets in committed examples. Omit unknown ports rather than making them up. Port declarations detect conflicts but do not set application ports. `cwd` resolves relative to the registration and must be an existing directory inside the selected root. Default cwd is the registration directory.

Readiness has exactly one HTTP(S) URL or TCP address, restricted to loopback. HTTP 200–399 is ready; redirects are also restricted to loopback. Timeout range 1–600 seconds, default 60. Without a probe, state only means the root process is alive. Probe timeout retains the process as unready; subsequent success releases waiting dependencies.

Stop signal TERM or INT, timeout range 1–30 seconds, default 5. The manager records observed descendants with PID and creation time. Use foreground commands; detached unobservable daemons are unsupported. Duplicate IDs/ports, missing dependencies, cycles, aliases, unknown fields and extra YAML documents are errors.

Optional `stackharbor.workspace.yaml`:

```yaml
version: 1
root: .
discover: true
exclude: [vendor, generated]
registrations: [tools/custom.yaml]
```

Root and registration paths resolve relative to the workspace file. Explicit registrations may live outside the target root, but require cwd inside it. Explicit `--root` must agree with workspace root. Discovery skips symlink directories, `.git`, `node_modules`, caches, build output and Python environments. Config maximum 256 KiB, 1000 projects/services, 10000 directories.

CLI: `discover [--json]`, `validate [--json]`, `init --dry-run`, `init --write`, `run`. All accept `--root` and `--workspace`. `init --project <candidate-directory>` limits the draft. init mode is mandatory; ambiguous candidates remain unwritten. Exit codes: 0 success, 2 configuration/usage error, 1 runtime/internal error. JSON schema_version is 1; environment values are excluded.
