# Session control and browser recovery

Verify the actual binary's `--version` and `--help`. macOS app sessions belong to
the shared user engine. `run`/`attach` opens or connects to the workspace without
starting applications. `q`/Ctrl-C, terminal closure and app closure only detach.
`start --root ROOT --target NODE --yes` can create the managed session and submit
an action without a TUI; accepted writes survive client exit. Linux and default
source builds retain terminal-owned sessions, so keep their foreground terminal
running. `STACKHARBOR_ENGINE_MODE=managed` opts source development into the engine.

Observe with `status --root ROOT --json`, `status --all --json`,
`logs --target NODE --follow` and `operations --id ID`. Mutation requires the
user's authorized scope and noninteractive `--yes`. Preview
`start|stop|restart --root ROOT --target NODE --dry-run --json`; inspect the exact
session/workspace, targets, affected nodes, warnings and 60-second expiry.
409 means changed state/identity and 410 means expiry: request a new plan and
confirmation. After timeout or lost response, query the operation ID; never
blindly repeat an unknown submission.

Managed selection uses canonical roots or session IDs with `sessions --focus`.
The shared engine PID is never a workspace shutdown target. Explicit
`kill --root ROOT --yes` closes only that workspace and preserves persistent or
observe-only resources. `engine ensure --json` demand-starts the registered
macOS service, `engine status --json` observes it and `engine stop --json` refuses
active projects/operations. Stop or close those scopes normally first. Login and
reboot never replay project starts. An unclean exit marks workspaces
`recovery_required`; preserve evidence and resolve recovery explicitly before
launching again. Inspect `engine recover --root ROOT --dry-run --json` with the
matching cached CLI. Within authorized stop scope, `engine recover --root ROOT
--yes` stops only recorded verified identities. Recovery remains blocked until
unknown/unrecorded descendants have been manually reviewed; only then use the
separate `--acknowledge --reviewed-unknown --yes` action. Never add that flag merely
to clear a block. No automatic adoption or action replay is permitted.

Legacy reopening still shows an occupied workspace's owner/close plan and closes
it only after confirmation. CLI `start`/`restart` previews a combined
`release-start`/`release-restart` plan with declared-port PID/command warnings.
`--yes` authorizes release and launch together. Changed listeners, protected
forwarders and unknown identities are refused. Opening a workspace starts
nothing in either mode.

`stackharbor web` opens the loopback browser gateway at 127.0.0.1:16800;
`--port 0 --no-open` prints an ordinary OS-assigned local URL. Any browser on the
machine can open it. The UI establishes an HttpOnly, SameSite=Strict session and
CSRF token automatically. Idle/restarted sessions reconnect; an unknown write is
never retried. A valid existing gateway is reused, while conflicting explicit
ports/development modes are refused. Stopping the gateway preserves independent
sessions. Browser access is not an OS-user authentication boundary.

Use the same `STACKHARBOR_CACHE_DIR` as the target session. Canonical roots and
namespaces isolate workspaces. CLI uses private 0700 runtime directories and
0600 Unix sockets, through the engine in managed mode or per-session sockets in
legacy mode, independently of Web/TCP. Native macOS uses a private request/stream
proxy; native system commands are unavailable to ordinary browsers. Legacy
unsupported endpoints stay read-only. Stale/partial/unavailable state and log
cursor gaps remain explicit; unknown metrics are not zero. Explicit resource
mutation checks other workspaces' active consumers and physical identities.
Volumes are never deleted.

No remote network access/binding, automatic project restart, browser terminal,
configuration editor or persistent-data deletion is provided. Source builds need
Node 26.9.0/npm and Go 1.26+; supported build/check paths validate the real embedded
assets. Public macOS distribution uses signed/notarized app DMGs; Linux uses CLI
archives and its legacy desktop packages. See packaged `docs/web-control.md` for
Vite exact-origin opt-in and full limits.
