#!/bin/sh
# Run inside the dedicated local verification container. /source is read-only.
set -eu
export PATH=/usr/local/sbin:/usr/sbin:/sbin:$PATH
tar -C /source --exclude='./desktop/target' --exclude='./desktop/node_modules' --exclude='./desktop/binaries' --exclude='./desktop/generated' --exclude='./web/node_modules' --exclude='./.superpowers' --exclude='./.cache' --exclude='./dist' --exclude='./_runtime_verification' --exclude='./.env*' --exclude='./.aws' --exclude='./.codex' -cf - . | tar -C /work --no-same-owner -xf -
cd /work
export CARGO_BUILD_JOBS=2 GOMAXPROCS=2 GOCACHE=/work/.cache/go-build GOMODCACHE=/work/.cache/go-mod
if [ "${1:-}" != --desktop-only ]; then make check; fi
make desktop
sh scripts/desktop-check.sh
dpkg -i dist/desktop/*/*.deb
export STACKHARBOR_DESKTOP_TEST_APP=/usr/bin/stackharbor-desktop
# Only this isolated root container disables the WebKit namespace sandbox;
# release apps and hosted-runner tests retain their normal sandbox behavior.
export WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1
dbus-run-session -- xvfb-run -a python3 scripts/desktop-smoke.py
cargo install tauri-driver --version 2.1.0 --locked --root /work/.cache/tauri-driver
export PATH=/work/.cache/tauri-driver/bin:$PATH
dbus-run-session -- xvfb-run -a python3 scripts/desktop-webdriver.py
mkdir -p /evidence/runtime /evidence/installers
cp -R _runtime_verification/desktop/. /evidence/runtime/
cp -R dist/desktop/. /evidence/installers/
