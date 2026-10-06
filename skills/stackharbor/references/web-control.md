# Existing-session control and browser recovery

Verify `stackharbor --version` and `--help` on the actual binary. Keep the workspace
foreground terminal running. Use `status --root ROOT --json`, `status --all --json`,
`logs --target NODE --follow`, and `operations --id ID` for observation. Starting,
stopping, restarting, releasing a declared port and closing a session require
user-authorized scope; noninteractive mutations require `--yes`. First preview
`start|stop|restart --root ROOT --target NODE --dry-run --json`; review exact
workspace/session, targets/affected nodes, warnings and60-second expiry.
409 means changed identity/state;410 means expired plan. Request a new plan and
confirmation. Timeout/disconnect can leave an accepted action running; query its
operation ID and retain unresolved evidence, never blindly reexecute.

Run `stackharbor web` to open a loopback browser gateway (127.0.0.1:16800).
`--port 0 --no-open` prints an OS-assigned launch URL. Its fragment credential is
one-use,60 seconds. UI removes it and exchanges a private HttpOnly cookie plus
CSRF token. Reopen replaces that browser's old session. Only exchange sets the
browser-session cookie; closing the browser may drop it. Authenticated requests
extend server expiry to eight idle hours without renewing the cookie. Browser
closure, idle expiry or gateway restart require a new launch URL via the same command. Stopping the gateway
preserves independent sessions/apps. A valid existing gateway is reused; an
explicit conflicting port/development mode is refused.

All commands must share `STACKHARBOR_CACHE_DIR` with target terminals. Canonical
roots isolate workspaces; different cache directories isolate discovery. CLI
uses private0700 runtime directories/0600 Unix sockets and works without Web.
Legacy sessions show read-only browser metadata; unsupported endpoints cannot
execute new control requests. Browser stale/unavailable/partial data and log gaps
must remain explicit. Unknown metrics are not zero. Persistent/observe Compose
resources survive session close; volumes are never deleted. Explicit resource
mutations check active consumers in other workspaces and physical identities.

No remote network binding/access, daemon hosting, automatic restart, browser
terminal, config editor, or persistent-data deletion is supported. Source builds
require Node26.9.0/npm plus Go1.26+; `make build/check/release` validates real
embedded assets against version/protocol/source/bundle hashes. Releases run with
neither Node nor Go. See packaged `docs/web-control.md` for Vite exact-origin
opt-in, recovery and full limits.
