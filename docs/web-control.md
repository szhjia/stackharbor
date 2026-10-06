# Local browser and CLI control

StackHarbor keeps each workspace in its own foreground terminal session. The
terminal, CLI and browser use the same session controller. Opening the browser
starts no application. Keep the workspace terminal open; no daemon, automatic
restart, interactive browser terminal, configuration editor, or data deletion is
provided. macOS and Linux run with the current user's permissions.

## Start a session, then control it

```sh
stackharbor --root /path/to/workspace
# In another terminal (no Web process is required):
stackharbor status --root /path/to/workspace --json
stackharbor start --root /path/to/workspace --target app/service/api --dry-run --json
stackharbor start --root /path/to/workspace --target app/service/api --yes --json
stackharbor logs --root /path/to/workspace --target app/service/api --follow
stackharbor operations --root /path/to/workspace --json
stackharbor operations --root /path/to/workspace --id OPERATION_ID --json
stackharbor kill --root /path/to/workspace --yes
```

Targets come from `discover --json`; v2 services use `project/service/key`, tasks
use `project/task/key`, resources use `resource/key`. v1 retains its original
IDs. `start`, `stop`, `restart`, `release`, and `kill` control an existing session.
`plan start` is a configuration preview; `start --dry-run` asks the live session
for its concrete 60-second plan. Review exact targets, affected dependencies,
workspace/session identity, warnings and expiry. Noninteractive mutation requires
`--yes`; `--dry-run` never executes. An affirmative answer approves only that
plan. State or identity changes return conflict (409), requiring a new explicit
plan; expired plans return 410. Resource mutation can be blocked by another
workspace's active consumers. Stop those consumers and replan.

`release --target NODE --port PORT` releases only an explicitly declared external
port after confirmation and current process-identity checks; it does not start
the service. Observe-only nodes cannot mutate. `--timeout 10m` limits waiting,
not acceptance. A timeout/disconnect may leave an accepted operation running.
Keep its ID and query `operations --id`; do not repeat the action because its
response was lost. CLI exit codes: 0 success, 1 runtime error, 2 invalid input or
confirmation/conflict refusal, 3 unresolved/expired operation result. Each
session retains up to 1000 terminal operations for at most 24 hours; queued and
running operations remain available. The queue accepts at most 100 waiting
operations. Logs are bounded and explicitly report cursor gaps.

## Browser gateway

```sh
stackharbor web
stackharbor web --port 0 --no-open
stackharbor status --all --json
```

The gateway binds only `127.0.0.1`, default port **16800**. `--port` accepts
0–65535; 0 asks the OS for a free port. An occupied port is an error. One gateway
per discovery namespace is reused after owner/endpoint verification. A different
explicit occupied instance port is refused. `--no-open` prints the URL instead
of opening a browser. The process that starts a gateway stays in the foreground;
a reuse invocation prints a fresh URL and exits. Ctrl-C in its owning terminal
stops the gateway and leaves independent workspace sessions/applications running.

The printed URL carries a one-use credential in its fragment, valid for 60
seconds. The UI removes the fragment before exchanging it for an HttpOnly,
SameSite=Strict cookie; write requests require the browser's CSRF token. Reopening
replaces that browser's previous session atomically. The cookie lasts for the
browser session and may be dropped when the browser closes. The server expires
a session after eight idle hours; authenticated requests extend that idle window
without changing the cookie. Gateway restart invalidates all credentials. Run
`stackharbor web` and open its new URL to recover after browser closure, expiry
or 401. Tokens are absent from normal
URL queries and storage; do not share a launch URL.

Overview, Workspaces, Resources and Operations show actual session snapshots.
Metrics with no measurement show Unknown. Browser data refreshes every second;
session discovery refreshes every two seconds. More than five seconds without a
valid snapshot marks it stale and disables mutation. Legacy/unsupported sessions
show read-only metadata. Logs, cursor gaps, transport loss, partial inventory,
unknown submission outcomes and disappeared sessions remain explicit. Browser
refresh can reuse a valid cookie; stream reconnection checks authentication and
revalidates state. A lost acceptance response never authorizes automatic retry.

## Namespaces, ownership and persistence

`STACKHARBOR_CACHE_DIR` selects the discovery namespace for sessions and gateways;
all control commands must use the same value as their workspace terminals.
Workspace identity uses its canonical real root. Another cache namespace is an
independent registry. Private runtime directories are 0700 and Unix sockets are
0600; long paths use a private shortened socket location. The CLI connects
straight to the session's Unix socket and does not depend on TCP/Web availability.
Existing PID records are observed, never automatically adopted as managed apps.

Close stops owned processes using the shared 30-second cleanup budget, restores
the terminal, and records a bound completion result. Persistent and observe-only
Compose resources are retained. Explicit stop/restart of a managed resource has
its own confirmation and shared-consumer checks. StackHarbor never deletes
volumes. Docker identity includes engine endpoint, Compose project, service and
container identity; container replacement invalidates a prior plan. Resource
coverage may be partial when another session or identity is unavailable.

This is a local control surface, with exact Host/Origin checks and loopback
readiness probes. Remote browser/network access, wildcard origins, network
binding and remote session transport are unsupported. A Docker engine endpoint
is checked separately from browser transport; a remote Docker context does not
make the browser remotely accessible. Review the selected engine before mutation.

## Build from source and develop the UI

Release binaries embed the actual React console and need neither Node nor Go at
runtime. Source builds require Go 1.26+, Make, and Node **26.9.0** (locked in
`.node-version`) with npm. `web/package-lock.json` pins npm dependencies and
copied shadcn/ui components reside in `web/src/components/ui`.

```sh
make build
make check
make release VERSION=0.3.0  # must match source version/release notes; packages only
```

Supported Make/scripts paths run `npm ci`, build the real Vite bundle, and
validate its generated manifest (source digest, release version, session
protocol, index and bundle SHA256). `make check` also runs TypeScript, frontend
tests, Go formatting/vet/all tests/race, and independent example modules. Missing
assets fail Go embedding; mismatched manifests fail validation. Raw `go build`
can reuse an existing bundle and does not prove source freshness; always use the
supported pipeline for delivery. Packaging builds the frontend once before
cross-compiling darwin/linux × arm64/amd64. Cross-compilation verifies packaging,
not another platform's runtime behavior. License texts ship in `licenses/`.

For UI development start an independent gateway, then Vite with its exact port:

```sh
stackharbor web --port 0 --no-open --dev-origin http://127.0.0.1:5173
# Read the actual gateway port from its owning registration/production URL.
STACKHARBOR_WEB_PROXY=http://127.0.0.1:GATEWAY_PORT npm --prefix web run dev
```

For a convenient known port, use `--port 16800` and
`STACKHARBOR_WEB_PROXY=http://127.0.0.1:16800`. Vite binds 127.0.0.1:5173 with a
strict fixed port. Open the fragment URL printed by the opt-in gateway. Vite
proxies `/api` to that exact backend; browser Origin is preserved. Only an exact
`http://127.0.0.1:PORT` development origin is accepted. An existing gateway with a
different explicit development origin refuses reuse; stop its owning process and
restart with the intended mode. Omitting `--dev-origin` reopens its real backend
URL. Production retains same-origin checks.

Current reproducible frontend tooling has a build-only advisory path through
shadcn 4.21.1 and braces 3.0.3 (GHSA-vfj7-8cjw-p6xm). The implementation audit
reported zero production npm advisories and seven high build-tool paths; these
counts are dated verification evidence, not a promise about future advisories.
No forced major downgrade was applied. Inspect `npm --prefix web audit` and
`npm --prefix web audit --omit=dev` when updating the lockfile.
