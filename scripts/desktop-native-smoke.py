#!/usr/bin/env python3
"""Exercise the real macOS WKWebView/native IPC against a private fixture.

Requires a debug .app (release hosts contain no test provider or completion
command). ServiceManagement is replaced only inside the explicitly selected
private debug fixture; no login item, LaunchAgent or real user integration is
registered. The Go engine and all installation files stay under a temporary
0700 HOME, and the signed app bundle is copied without modifying its resources.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import time


def run(command, **kwargs):
    return subprocess.run(command, check=True, capture_output=True, text=True, timeout=30, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--app', type=Path, default=Path('desktop/target/debug/bundle/macos/StackHarbor.app'))
    parser.add_argument('--output', type=Path, default=Path('_runtime_verification/desktop/native-smoke.json'))
    args = parser.parse_args()
    if sys.platform != 'darwin':
        parser.error('native smoke requires a macOS GUI session')
    source = args.app.resolve(strict=True)
    executable = source / 'Contents/MacOS/stackharbor-desktop'
    if run([str(executable), '--desktop-smoke-supported']).stdout.strip() != 'true':
        parser.error('build a debug test .app; release hosts cannot enable fixture instrumentation')
    run(['/usr/bin/codesign', '--verify', '--deep', '--strict', str(source)])
    root = Path(tempfile.mkdtemp(prefix='sh-native-', dir='/private/tmp')).resolve()
    root.chmod(0o700)
    engine = None
    application = None
    evidence = {'ok': False, 'app': str(source), 'service_management': 'debug-only isolated provider; real authorization unverified', 'phases': []}
    try:
        home = root / 'home'
        bundle = home / 'Applications/StackHarbor.app'
        shutil.copytree(source, bundle)
        for directory in [home / '.local/bin', home / '.codex', home / '.claude', root / 'runtime', root / 'engine-state', root / 'namespace', root / 'namespace-two']:
            directory.mkdir(parents=True, exist_ok=True, mode=0o700)
            directory.chmod(0o700)
        original_cli = home / '.local/bin/stackharbor'
        original_cli.write_text('fixture user-owned CLI\n')
        project = root / 'project'
        project.mkdir(mode=0o700)
        record = project / 'stackharbor.yaml'
        record.write_text('# fixture project must remain unchanged\n')
        env = {**os.environ, 'HOME': str(home), 'STACKHARBOR_NATIVE_SMOKE_DIR': str(root), 'STACKHARBOR_ENGINE_RUNTIME_DIR': str(root / 'runtime'), 'STACKHARBOR_ENGINE_STATE_DIR': str(root / 'engine-state'), 'STACKHARBOR_ENGINE_MODE': 'managed'}
        binary = bundle / 'Contents/MacOS/stackharbor'
        app_binary = bundle / 'Contents/MacOS/stackharbor-desktop'
        with (root / 'engine.log').open('w') as log:
            engine = subprocess.Popen([str(binary), 'engine', 'serve', '--idle-timeout', '300s'], env=env, stdout=log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + 15
        while True:
            result = subprocess.run([str(binary), 'engine', 'status', '--json'], env=env, capture_output=True, text=True, timeout=5)
            if result.returncode == 0:
                evidence['engine_pid'] = json.loads(result.stdout)['pid']
                break
            if engine.poll() is not None or time.monotonic() > deadline:
                raise AssertionError('isolated engine readiness failed: ' + result.stderr)
            time.sleep(0.05)
        for phase in ['install', 'uninstall']:
            config = root / 'smoke.json'
            config.write_text(json.dumps({'token': secrets.token_hex(32), 'phase': phase}))
            config.chmod(0o600)
            output = root / 'result.json'
            output.unlink(missing_ok=True)
            with (root / f'{phase}.log').open('w') as log:
                application = subprocess.Popen([str(app_binary), '--native-smoke', '--namespace', str(root / 'namespace')], env=env, stdout=log, stderr=subprocess.STDOUT)
            forwarded = False
            deadline = time.monotonic() + 60
            while not output.exists():
                if phase == 'install' and not forwarded and (root / 'forward-ready').is_file():
                    forwarding = ['/usr/bin/open', '-n', '-g', '-a', str(bundle)]
                    for name in ['HOME', 'STACKHARBOR_NATIVE_SMOKE_DIR', 'STACKHARBOR_ENGINE_RUNTIME_DIR', 'STACKHARBOR_ENGINE_STATE_DIR', 'STACKHARBOR_ENGINE_MODE']:
                        forwarding += ['--env', f'{name}={env[name]}']
                    forwarding += ['--args', '--native-smoke', '--namespace', str(root / 'namespace-two')]
                    run(forwarding, env=env)
                    forwarded = True
                if application.poll() is not None or time.monotonic() > deadline:
                    raise AssertionError(f'native {phase} did not complete: ' + (root / f'{phase}.log').read_text())
                time.sleep(0.05)
            result = json.loads(output.read_text())
            evidence['phases'].append(result)
            if not result.get('ok'):
                raise AssertionError(f'native {phase} failed: {result}')
            application.wait(timeout=10)
            if application.returncode != 0:
                raise AssertionError(f'native {phase} exited {application.returncode}')
            application = None
            if phase == 'install':
                assert engine.poll() is None, 'quitting native shell stopped the independent engine'
                assert result['namespace'] == str(root / 'namespace'), 'explicit namespace changed'
                assert forwarded, 'fixture did not request the second namespace'
                assert str(root / 'namespace-two') in result['window_namespaces'].values(), 'existing native process did not forward the second namespace'
                assert original_cli.is_symlink(), 'required CLI symlink absent'
                skill = home / '.agents/skills/stackharbor'
                assert (skill / 'SKILL.md').is_file() and (skill / 'references/installation.md').is_file(), 'full skill absent'
                assert (home / '.codex/skills/stackharbor').is_symlink()
                assert (home / '.claude/skills/stackharbor').is_symlink()
                for shell in ['.zprofile', '.bash_profile']:
                    profile = home / shell
                    assert profile.read_text().count('# >>> StackHarbor managed PATH >>>') == 1
                    with profile.open('a') as stream:
                        stream.write('export USER_FIXTURE=preserve\n')
                with (skill / 'SKILL.md').open('a') as stream:
                    stream.write('\nfixture-user-edit-preserve\n')
        assert not original_cli.exists() and not original_cli.is_symlink(), 'owned CLI was not removed'
        assert (home / '.agents/skills/stackharbor/SKILL.md').read_text().endswith('fixture-user-edit-preserve\n'), 'modified skill was removed'
        for shell in ['.zprofile', '.bash_profile']:
            text = (home / shell).read_text()
            assert 'USER_FIXTURE=preserve' in text and 'StackHarbor managed PATH' not in text, 'shell uninstall removed user edits'
        backups = home / 'Library/Application Support/com.xahacktech.stackharbor/backups'
        assert any(path.is_file() and path.read_text() == 'fixture user-owned CLI\n' for path in backups.iterdir()), 'migration backup was lost'
        assert record.read_text() == '# fixture project must remain unchanged\n', 'project files were changed'
        engine.wait(timeout=10)
        assert engine.returncode == 0, 'idle uninstall did not stop the fixture engine cleanly'
        evidence['ok'] = True
        evidence['checks'] = ['signed-bundle-resources', 'real-wkwebview-native-invoke', 'real-private-engine-http-sse', 'per-window-namespace', 'launchservices-new-instance-forwarding', 'shell-exit-preserves-engine', 'resumable-required-install', 'unchanged-owned-uninstall', 'modified-user-data-preserved']
    except Exception as error:
        evidence['error'] = str(error)
        evidence['fixture'] = str(root)
        raise
    finally:
        if application is not None and application.poll() is None:
            application.terminate()
            application.wait(timeout=10)
        if engine is not None and engine.poll() is None:
            # This process was created by this fixture and never starts services.
            engine.terminate()
            engine.wait(timeout=10)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(evidence, indent=2) + '\n')
        if evidence['ok']:
            shutil.rmtree(root)
        print(json.dumps(evidence, indent=2))


if __name__ == '__main__':
    main()
