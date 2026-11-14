# Desktop installation and shared engine

macOS 14+ uses the signed/notarized StackHarbor app as its installation entry
point, for Apple Silicon and Intel. A single unprivileged Go engine owns workspace
sessions independently of the app, windows and terminal clients. Linux retains
its existing CLI/Web and optional Tauri desktop behavior. Windows is unsupported.

## Install on macOS

Download the matching DMG from the official release, then move `StackHarbor.app`
to `/Applications` or `~/Applications` before opening it. Setup cannot run from a
mounted DMG. Public builds must pass signing and notarization verification;
internal ad-hoc builds are test artifacts and do not establish that release gate.

First launch shows the versioned agreement and the external integrations before
writing them. Setup requires both the matching CLI in `~/.local/bin/stackharbor`
and the full skill in `~/.agents/skills/stackharbor`. Detected Codex/Claude
compatibility locations link to that canonical skill. Login startup is optional
and defaults **off**. PATH changes are previewed as a reversible managed shell
block. Existing unowned files, directories and dangling symlinks are conflicts;
only an explicitly accepted, backed-up migration may replace them. A partially
completed setup records its stage and can be repaired; success requires verified
CLI and skill integrations.

The bundle ID is `com.xahacktech.stackharbor`. The user LaunchAgent label is
`com.xahacktech.stackharbor.engine`, registered through SMAppService without sudo
or a privileged daemon. CLI demand launch uses this registered service. Setup,
background authorization and login-startup status are separate states; macOS may
require the user to enable a denied background item in System Settings.

## Daily lifecycle

Adding or opening a workspace creates no project processes. The app and CLI can
open sessions and submit explicit start operations independently:

```sh
stackharbor engine ensure --json
stackharbor start --root /path/to/workspace --target app/service/api --dry-run --json
stackharbor start --root /path/to/workspace --target app/service/api --yes --json
stackharbor run --root /path/to/workspace
stackharbor attach --root /path/to/workspace
stackharbor engine status --json
```

In managed macOS sessions, `q` and Ctrl-C detach the TUI. Closing a terminal,
window or the app keeps accepted operations and engine-owned services running.
Closing a window saves navigation/filter state and destroys its WebView; a
lightweight tray shell remains. Cmd-Q exits that shell. Closing the app does not
stop projects. Explicit service stop or workspace close retains the existing
plan, confirmation, dependency and process-identity protections:

```sh
stackharbor stop --root /path/to/workspace --target app/service/api --yes --json
stackharbor kill --root /path/to/workspace --yes --json
stackharbor engine stop --json
```

Workspace close affects only the selected canonical root and discovery namespace.
`engine stop` refuses while projects or operations are active: stop/close them
through their normal controls first. Never signal a workspace using the shared
engine PID. Use a root or session ID to select/attach with
`stackharbor sessions --focus SESSION_ID`; PID-based window focus applies to
legacy terminal owners only.

The engine exits after 60 seconds without owned processes, in-flight operations
or connected clients. Optional login startup never replays project starts.
Computer restart does not automatically start services. An unclean engine exit
marks recorded workspaces `recovery_required`; it does not adopt old processes,
replay accepted writes or restart projects. Keep the reported identities and
resolve the recovery explicitly before another launch.

Inspect an unclean workspace with the matching cached CLI:

```sh
stackharbor engine recover --root /path/to/workspace --dry-run --json
```

After reviewing the recorded identities and authorizing their stop, use
`engine recover --root /path/to/workspace --yes`. This stops only verified recorded
processes and leaves recovery blocked. Inspect unknown or unrecorded descendants
separately. Only after that manual review, acknowledge with
`engine recover --root /path/to/workspace --acknowledge --reviewed-unknown --yes`.
Acknowledgement never starts services; open/start the workspace explicitly.
Do not acknowledge merely to clear a blocked state or replay an unknown write.

Each session freezes its launch environment. CLI-created sessions use that
invocation's environment; app-created sessions use a bounded (10 second) default
shell export in the workspace. YAML overrides that environment. Different
workspaces can use different toolchains without changing process-global cwd or
PATH. Environment secrets stay in memory.

## Upgrades and uninstall

App setup caches verified signed Go binaries under Application Support
`versions/<version>-<sha>` and retains current, previous and live-engine versions.
An upgrade switches engines only after explicit stop or verified idle. If Finder
replaces the app while an incompatible engine is running, retain its old cached
client recovery route and close its projects explicitly; do not terminate a
shared engine to make a new bundle connect.

Use the app's explicit uninstall action after stopping projects. It removes only
owned, unmodified integrations, preserving project YAML and default user history.
Edited/unowned entries remain for review. Dragging the app to Trash does not run
an uninstaller and does not establish that projects or integrations are removed.
MIT and third-party notices ship with the app. No telemetry is collected.

## Linux and source development

Linux keeps native x64/arm64 `.deb` desktop packages and checksum-verified CLI
archives. Ubuntu 22.04 and Debian 12 are the baseline. Package-manager installs
resolve WebKitGTK/GTK/tray dependencies; launchers work without a system tray.
The optional desktop master embeds the existing browser console. Its project
sessions remain owned by foreground terminals: `q`/Ctrl-C closes that legacy
session, while quitting desktop leaves independent sessions running.

Source Go builds retain legacy behavior unless
`STACKHARBOR_ENGINE_MODE=managed` is set. The registered macOS app is required for
production demand launch; a raw source binary does not install that service.
For isolated development, start `engine serve` directly and use private
`STACKHARBOR_ENGINE_STATE_DIR`, `STACKHARBOR_ENGINE_RUNTIME_DIR` and
`STACKHARBOR_CACHE_DIR` directories. These overrides never authorize touching a
real user's installation. Existing Linux desktop overrides
`STACKHARBOR_DESKTOP_APP` and `STACKHARBOR_DESKTOP_RUNTIME_DIR` remain available.

## Build and verification

Build natively with the project's Go/Node requirements, Rust 1.95 and native
Tauri dependencies:

```sh
make desktop
make desktop-check
python3 scripts/engine-lifecycle-smoke.py --binary dist/stackharbor
python3 scripts/engine-performance.py --binary dist/stackharbor --duration 600 --workspaces 0 --services 0 --output /tmp/stackharbor-performance.json
# Linux legacy WebView coverage, with tauri-driver and WebKitWebDriver:
dbus-run-session -- xvfb-run -a python3 scripts/desktop-webdriver.py
```

macOS CI builds a separate debug app with an isolated ServiceManagement test
provider for real WebView smoke. That provider is absent from release builds;
it does not prove actual production login-item authorization. Linux retains its
legacy native smoke.

`make desktop` packages the matching Go binary, React resources, skill, helper and
licenses. Public macOS packaging requires
`STACKHARBOR_DESKTOP_DISTRIBUTION=release`, `APPLE_SIGNING_IDENTITY` and valid
notarization credentials. Tauri consumes certificate/keychain configuration;
secrets stay outside the repository. Internal builds are ad-hoc signed and
unnotarized. Packaging/compilation does not verify native background-item
registration, an actual login session or Finder replacement.

Lifecycle smoke uses an existing binary, temporary state and configured sleeping
fixture processes. It checks client detach, CLI exit, workspace isolation,
explicit shutdown and crash refusal without installing into a real profile.
Performance sampling also uses temporary fixtures. It records kernel per-process
CPU/RSS, waited observer-helper CPU, sampler overhead, PIDs, duration and scope.
Use `--native-pid PID` only for a separately prepared idle release app; the sampler
observes it and never signals it. The report distinguishes engine-only sampling
from combined native/engine measurement and never treats fixture applications as
StackHarbor overhead. Short samples do not establish the 10 minute acceptance.

Targets are mean idle background CPU **≤1% of one logical core** and no-service
engine+native RSS **≤150 MiB**. Release status must include the actual 600-second
report, observer helpers and native process scope, plus signature/notarization,
login/reboot, install repair/uninstall and real WebView acceptance. Targets remain
unverified until those measurements and native checks pass.
