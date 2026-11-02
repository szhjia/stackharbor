# Desktop master (v1)

The optional Tauri desktop application embeds the existing StackHarbor console.
Go continues to own each project session in its foreground terminal. Opening a
window starts no project services. Windows backend support is not implemented.

## Using it

Install the matching desktop package, then run `stackharbor` in a project terminal.
The master starts in the background without taking focus. Open its console from
the application menu, menu bar/tray, launcher, or `stackharbor desktop`.
`stackharbor run --no-desktop` opts out. Queries, version checks, SSH and headless
environments never require the desktop. Standalone CLI installation continues
to work; running the CLI never downloads or installs desktop software.

Closing a window hides it. An empty master remains running until explicitly quit.
Quitting master stops only gateways it owns, leaving project terminals and
services running. Project q/Ctrl-C closes that project using existing cleanup
policy; persistent/observe-only resources remain. Existing slaves do not relaunch
a manually quit master. A new interactive run can launch it again.

Workbench resource totals and controls are the same in browsers and WebViews.
Physical resources are deduplicated, process RSS and container working set stay
separate, missing/stale data stays explicit, and writes use existing confirmation
and identity checks. Create slaves in a terminal; desktop does not create offline
sessions. Existing UI components are unchanged.

Discovery namespaces (`STACKHARBOR_CACHE_DIR`) remain isolated. Master can hold a
separate gateway/window per namespace. The application menu opens the default
namespace; `stackharbor desktop` opens the invoking CLI's namespace. Totals are
never implicitly combined across namespaces.

## Installation

- macOS 14+: arm64 and Intel DMG. Move StackHarbor.app to `/Applications` or
  `~/Applications`. **Install command line tool…** in the application menu creates
  `~/.local/bin/stackharbor` pointing to the bundled CLI. Existing entries,
  including dangling symlinks, are never overwritten. Add `~/.local/bin` to PATH.
- Linux: native x64/arm64 `.deb` with CLI and desktop launcher. Ubuntu 22.04 and
  Debian 12 are the baseline. Install through the package manager to resolve
  WebKitGTK, GTK, tray and observation dependencies. Launchers work without a
  system tray. Project commands retain their terminal's argv/cwd/environment.
- CLI/desktop versions must match. Incompatible/development gateways are left
  running when desktop integration is refused; independent CLI/Web still work.

Development may select an absolute executable with `STACKHARBOR_DESKTOP_APP`.
`STACKHARBOR_DESKTOP_RUNTIME_DIR` selects an absolute 0700 directory for native
test isolation or unusually long home paths; it does not change service ownership.
On macOS it never creates another application instance: all projects and discovery
namespaces share one master. Native lifecycle acceptance requires a dedicated
login session without a running StackHarbor app; tests preserve an existing app
and refuse to bypass the singleton.

## Build and verification

Install Go/Node from this project's requirements, Rust 1.95 and native Tauri build
dependencies. Build natively on the target system/architecture:

```sh
make desktop
make desktop-check
python3 scripts/desktop-smoke.py
# Linux, with tauri-driver and WebKitWebDriver:
dbus-run-session -- xvfb-run -a python3 scripts/desktop-webdriver.py
```

`make desktop` rebuilds/validates the frontend, bundles a matching Go executable,
collects licenses and produces installers, version/commit/frontend metadata and
SHA256SUMS in `dist/desktop/`. The regular CLI check/release pipeline remains
independent of Rust. CI builds macOS arm64/Intel and Linux x64/arm64, retains full
`make check`, and adds Rust, native lifecycle and real Linux WebView controls.
Menu/focus/tray behavior needs interactive macOS and representative Linux desktop
acceptance; compilation alone is not desktop environment acceptance.

Default installers are **internal test builds**, ad-hoc signed on macOS and not
notarized. Public macOS packaging requires `STACKHARBOR_DESKTOP_DISTRIBUTION=release`,
`APPLE_SIGNING_IDENTITY`, and notarization credentials (`APPLE_ID`, `APPLE_PASSWORD`,
`APPLE_TEAM_ID` or API key credentials). Tauri consumes certificate/keychain
configuration; secrets are not stored in the repository. CI uploads internal
artifacts only and never publishes releases.

Startup/recovery is bounded and does not replay writes. Parent pipe EOF cleans
owned gateways. Independently started gateways are never terminated by master.
Replacement gateways update the existing WebView's verified origin before
navigation; recovery stays inside the app. Linux process identities use kernel
start ticks and a boot-ID-bound epoch in the private, user-owned
`/tmp/stackharbor-clock-<uid>` directory. This keeps identities stable across wall
clock corrections; unsafe or unavailable clock metadata causes refusal, not a
weaker PID check. An older container session recorded with the previous
unstable estimate may require an explicit terminal restart; mismatching owners
are preserved and never adopted or signalled.
