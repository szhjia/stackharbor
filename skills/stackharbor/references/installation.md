# Install StackHarbor from GitHub

Canonical source: https://github.com/szhjia/stackharbor. Supported systems are
macOS 14+ and Linux, arm64/amd64. Running a release needs neither Go nor Node;
building from source needs Go 1.26+ and the documented Node toolchain. This
reference installs StackHarbor and its integrations.

1. Inspect `command -v stackharbor`, `stackharbor --version` and `stackharbor --help`
   when present. Check `uname -s` and `uname -m`. Preserve existing commands,
   integrations and active workspace sessions; an upgrade is not permission to
   terminate projects or overwrite unrelated files.
2. **macOS:** download the signed/notarized DMG for the architecture from official
   GitHub Releases. Move StackHarbor.app to `/Applications` or `~/Applications`
   and open that installed bundle, not the mounted DMG. The app displays a
   versioned agreement before external writes. Setup installs both the matching
   `~/.local/bin/stackharbor` CLI and the complete canonical
   `~/.agents/skills/stackharbor` skill, plus detected Codex/Claude compatibility
   links. Login startup is optional and defaults off. Preview managed shell PATH
   changes; unowned files/directories/symlinks require explicit backed-up migration.
   Report denied macOS background authorization honestly and complete any required
   user action in System Settings. Do not bypass Gatekeeper or quarantine checks.
3. **Linux:** inspect and run the official checksum-verifying installer:

```sh
curl -fsSL https://raw.githubusercontent.com/szhjia/stackharbor/main/scripts/install-release.sh -o /tmp/stackharbor-install.sh
sh /tmp/stackharbor-install.sh
```

It downloads the matching stable archive and SHA256 manifest over HTTPS, verifies
its checksum and installs into `~/.local/bin` without sudo. Existing commands or
symlinks cause refusal. Use `sh /tmp/stackharbor-install.sh VERSION BIN_DIR` for a
pin/destination. Checksums detect corruption, not a compromised publisher. On
macOS this script only prints the corresponding DMG release page and writes
nothing. Historical binary-only installers remain on their original release tags.
A standalone skill download does not include the repository's root scripts.

4. Verify `~/.local/bin/stackharbor --version` and `--help`. If PATH lacks that
   directory, use the absolute path immediately. On Linux explain
   `export PATH="$HOME/.local/bin:$PATH"`; do not silently edit unrelated shell
   configuration. On macOS use the app's previewed managed PATH option. A user
   request to install StackHarbor authorizes the installation; do not request
   duplicate confirmation merely because this reference describes installation.
5. macOS upgrades retain verified versioned engine clients for current, previous
   and live-engine versions. Switch engines only when idle or after explicit
   project shutdown. An incompatible running engine keeps an old-client recovery
   path; do not signal its shared PID. Use the app's explicit integration uninstall
   after stopping projects. Only owned/unmodified entries are removed; project
   YAML and default history remain. Dragging the app to Trash runs no uninstaller.
   Linux upgrades retain an explicit backup and verify the chosen replacement.
6. Installation does not install project runtimes. Continue with discovery and
   registration. If Docker is needed, follow [Docker readiness](docker.md).
   Installed StackHarbor alone does not prove the project is runnable.

macOS app setup already installs its skill. Standalone skill distribution remains
`npx skills add szhjia/stackharbor --skill stackharbor`; update with
`npx skills update stackharbor -g`. Linux/source symlink installation is described
in [maintenance](maintenance.md).
