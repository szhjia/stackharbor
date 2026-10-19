#!/usr/bin/env python3
"""Foreground source watcher for projects without a native development command."""

from __future__ import annotations

import argparse
import contextlib
import hashlib
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

IGNORED = {
    ".git",
    ".venv",
    "venv",
    "node_modules",
    "__pycache__",
    ".pytest_cache",
    ".mypy_cache",
    ".ruff_cache",
    ".local-run",
}


class Sources:
    def __init__(self, paths: list[Path]) -> None:
        self.paths = paths
        self.cache = {}

    def snapshot(self) -> tuple[tuple[str, bytes], ...]:
        files = set()
        for path in self.paths:
            if path.is_file():
                files.add(path)
            elif path.is_dir():
                for directory, names, entries in os.walk(path, followlinks=False):
                    names[:] = [name for name in names if name not in IGNORED and not name.startswith(".")]
                    files.update(Path(directory) / name for name in entries if not name.endswith((".pyc", ".pyo")))
        result = []
        retained = {}
        for path in sorted(files):
            if path.is_symlink():
                continue
            try:
                stat = path.stat()
                identity = (stat.st_mtime_ns, stat.st_ctime_ns, stat.st_size, stat.st_ino)
                previous = self.cache.get(path)
                digest = (
                    previous[1] if previous and previous[0] == identity else hashlib.sha256(path.read_bytes()).digest()
                )
            except FileNotFoundError:
                continue
            retained[path] = (identity, digest)
            result.append((str(path), digest))
        self.cache = retained
        return tuple(result)


def group_alive(process: subprocess.Popen) -> bool:
    process.poll()
    try:
        os.killpg(process.pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return process.poll() is None


def stop(processes: list[subprocess.Popen], timeout: float) -> None:
    for process in processes:
        if group_alive(process):
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGTERM)
    deadline = time.monotonic() + timeout
    while any(group_alive(process) for process in processes) and time.monotonic() < deadline:
        time.sleep(0.05)
    for process in processes:
        if group_alive(process):
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGKILL)
        process.wait()
    processes.clear()


def load(path: Path, name: str) -> tuple[dict, Path, float, Path | None]:
    document = json.loads(path.read_text())
    spec = document["services"][name]
    cwd = (path.parent / spec["cwd"]).resolve()
    if not cwd.is_dir():
        raise ValueError("development cwd does not exist")
    for key in ("run", "watch"):
        if (
            not isinstance(spec.get(key), list)
            or not spec[key]
            or not all(isinstance(item, str) and item for item in spec[key])
        ):
            raise ValueError(key + " must be a nonempty string array")
    if "build" in spec and (
        not isinstance(spec["build"], list)
        or not spec["build"]
        or not all(isinstance(item, str) and item for item in spec["build"])
    ):
        raise ValueError("build must be a nonempty argv array")
    timeout = float(spec.get("stop_timeout_seconds", 10))
    if not 0 < timeout <= 60:
        raise ValueError("stop_timeout_seconds must be in (0, 60]")
    output = document.get("output_root")
    output = (path.parent / output).resolve() if output else None
    output_base = output or Path(tempfile.gettempdir()).resolve()
    for item in spec["watch"]:
        watched = (cwd / item).resolve()
        if watched == output_base or watched in output_base.parents:
            raise ValueError("output directory must stay outside watched paths")
    return spec, cwd, timeout, output


def expand_command(argv: list[str], output: Path) -> list[str]:
    return [item.replace("{output}", str(output)).replace("{python}", sys.executable) for item in argv]


def run(path: Path, name: str, check: bool = False) -> int:
    spec, cwd, timeout, output = load(path, name)
    if check:
        print("Valid development watcher: " + name)
        return 0
    os.chdir(cwd)
    sources = Sources([(cwd / item).resolve() for item in spec["watch"]])
    children, builders = [], []
    pid_path = Path(os.environ[spec["pid_file_env"]]) if spec.get("pid_file_env") else None

    def interrupted(signum: int, _frame: object) -> None:
        raise SystemExit(128 + signum)

    for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, interrupted)
    if output:
        output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="stackharbor-dev-", dir=output) as temporary:
        current = None
        observed = None
        attempt = 0
        try:
            while True:
                if children and children[0].poll() is not None:
                    print("[dev-watch] process exited; waiting for a source edit", flush=True)
                    stop(children, timeout)
                latest = sources.snapshot()
                if latest != observed:
                    observed = latest
                    attempt += 1
                    candidate = Path(temporary) / str(attempt)
                    candidate.mkdir()
                    successful = True
                    if "build" in spec:
                        print("[dev-watch] building " + name, flush=True)
                        build = subprocess.Popen(expand_command(spec["build"], candidate), start_new_session=True)  # noqa: S603 -- project-owned argv, no shell
                        builders.append(build)
                        while build.poll() is None:
                            time.sleep(0.1)
                        builders.clear()
                        successful = build.returncode == 0
                    if not successful:
                        shutil.rmtree(candidate)
                        print(
                            "[dev-watch] build failed; retaining last good process and waiting for an edit", flush=True
                        )
                    else:
                        stop(children, timeout)
                        if current:
                            shutil.rmtree(current)
                        current = candidate
                        child_env = dict(os.environ, PYTHONPYCACHEPREFIX=str(candidate / "pycache"))
                        children.append(
                            subprocess.Popen(  # noqa: S603 -- project-owned argv, no shell
                                expand_command(spec["run"], candidate), start_new_session=True, env=child_env
                            )
                        )
                        if pid_path:
                            pid_path.parent.mkdir(parents=True, exist_ok=True)
                            pid_path.write_text(str(children[0].pid) + "\n")
                        print("[dev-watch] started " + name, flush=True)
                time.sleep(0.3)
        finally:
            stop(builders, min(timeout, 5))
            stop(children, timeout)
            if pid_path:
                pid_path.unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("config", type=Path)
    parser.add_argument("service")
    parser.add_argument("--check", action="store_true", help="Validate without starting commands")
    args = parser.parse_args()
    try:
        return run(args.config.resolve(), args.service, args.check)
    except (KeyError, ValueError, OSError) as error:
        print("dev-watch: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
