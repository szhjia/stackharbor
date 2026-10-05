# Install StackHarbor from GitHub

Canonical source: https://github.com/szhjia/stackharbor. Supported binaries: macOS/Linux, arm64/amd64. The binary does not need Go; building from source needs Go 1.26+. This reference installs StackHarbor, not arbitrary unrelated Mac applications.

1. Check `command -v stackharbor`, `stackharbor --version`, and `stackharbor --help` when available. Inspect an existing installation before replacing it. Never overwrite a user command incidentally.
2. Check `uname -s` and `uname -m`. Use the official release installer below. If downloaded as a standalone skill, do not refer to nonexistent `../../scripts` files.
3. Download the installer to a temporary file and inspect it before running. The user asking to install StackHarbor authorizes this installation; do not ask again merely because the skill mentions installation.

```sh
curl -fsSL https://raw.githubusercontent.com/szhjia/stackharbor/main/scripts/install-release.sh -o /tmp/stackharbor-install.sh
sh /tmp/stackharbor-install.sh
```

The script resolves the latest stable GitHub release, downloads the matching archive and SHA256 manifest over HTTPS, verifies the checksum, and installs into `~/.local/bin` without sudo. It fails if an existing command or symlink occupies the destination. A pinned release can be selected with `sh /tmp/stackharbor-install.sh 0.1.0`. Checksums detect corruption, not a compromised publisher.

4. Verify `~/.local/bin/stackharbor --version` and `~/.local/bin/stackharbor --help`. If the directory is missing from PATH, use the absolute path immediately and explain `export PATH="$HOME/.local/bin:$PATH"`; do not silently edit unrelated shell configuration. For source installs use `make build`, then `sh scripts/install.sh` in the checkout.
5. For upgrades, establish which binary is used, retain an explicit backup, then install and verify the replacement. Do not delete arbitrary files. Do not globally disable Gatekeeper or remove quarantine indiscriminately; binaries are not Apple-notarized.
6. Installing StackHarbor does not install a project's runtimes. Continue with discovery/registration. If Docker is required, follow [Docker readiness](docker.md); do not claim the app is runnable merely because StackHarbor installed successfully.

Skill distribution: `npx skills add szhjia/stackharbor --skill stackharbor`; update with `npx skills update stackharbor`. Source/release symlink installation is documented in [maintenance](maintenance.md).
