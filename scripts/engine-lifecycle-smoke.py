#!/usr/bin/env python3
"""Release-binary engine lifecycle acceptance in temporary, uninstalled state."""
import argparse
import fcntl
import hashlib
import json
import os
import pathlib
import pty
import signal
import struct
import subprocess
import termios
import threading
import time
from engine_fixture import Fixture, same_process, until


def detach(fixture, root, key):
    master, slave = pty.openpty()
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 120, 0, 0))
    process = subprocess.Popen([str(fixture.binary), "attach", "--root", str(root)],
                               env=fixture.env, cwd=fixture.root, stdin=slave, stdout=slave,
                               stderr=slave, start_new_session=True)
    os.close(slave)
    output = bytearray()
    def drain():
        try:
            while True:
                data = os.read(master, 8192)
                if not data:
                    break
                output.extend(data)
        except OSError:
            pass
    reader = threading.Thread(target=drain, daemon=True)
    reader.start()
    try:
        until(lambda: b"Dashboard" in output or b"StackHarbor" in output)
        os.write(master, key)
        assert process.wait(timeout=10) == 0, output[-4000:].decode(errors="replace")
    finally:
        # This is our direct child, never the shared engine/workspace owner PID.
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
        os.close(master)
        reader.join(timeout=1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, help="Existing release Go binary; never builds or installs")
    parser.add_argument("--output", type=pathlib.Path, help="Write JSON evidence in addition to stdout")
    parser.add_argument("--skip-crash", action="store_true", help="Report crash-recovery scope as unverified")
    args = parser.parse_args()
    fixture = Fixture(args.binary)
    report = {"schema_version": 1, "binary": str(fixture.binary), "binary_sha256": hashlib.sha256(fixture.binary.read_bytes()).hexdigest(), "scope": "isolated direct engine serve; no native install/login/signature acceptance", "passed": [], "unverified": []}
    try:
        report["version"] = fixture.command("--version").stdout.strip()
        endpoint = fixture.start_engine()
        report["engine_pid"] = endpoint["pid"]
        first = fixture.workspace("alpha")
        second = fixture.workspace("beta")
        fixture.request("POST", "/api/v1/workspaces/open", {"root": str(first)})
        assert not list(first.glob("*.pid")), "Opening a workspace started a fixture process"
        report["passed"].append("workspace open starts no services")
        alpha = fixture.start_services(first)
        beta = fixture.start_services(second)
        before = json.loads(fixture.command("sessions", "--json").stdout)
        assert len(before) == 2, before
        assert len({item["session_id"] for item in before}) == 2, before
        assert {item["pid"] for item in before} == {endpoint["pid"]}, before
        _, identity = fixture.request("GET", "/engine/v1/identity")
        assert identity["active_processes"] >= 2, identity
        time.sleep(.3)
        assert fixture.alive() and fixture.engine.poll() is None
        report["passed"].append("short-lived CLI start creates separate sessions; engine and same service PIDs survive CLI exit")
        for key in (b"q", b"\x03"):
            detach(fixture, first, key)
            assert fixture.alive()
            after = json.loads(fixture.command("sessions", "--json").stdout)
            assert sorted(item["session_id"] for item in after) == sorted(item["session_id"] for item in before)
        report["passed"].append("TUI q and Ctrl-C detach without closing or replacing sessions")
        refused = fixture.command("engine", "stop", "--json", check=False)
        assert refused.returncode != 0 and fixture.alive() and fixture.engine.poll() is None, refused
        report["passed"].append("engine stop refuses active project processes")
        fixture.close(first)
        assert fixture.alive(beta), "Closing alpha harmed independent beta"
        report["passed"].append("explicit workspace close stops only selected alpha; beta keeps the same process identity")
        # An acknowledged close is already terminal: do not hide a publication
        # race by polling service PIDs or inserting a scheduling gap here.
        fixture.close(second, wait=False)
        fixture.command("engine", "stop", "--json")
        assert fixture.engine.wait(timeout=10) == 0
        assert not any(same_process(item["pid"], item["identity"]) for item in beta)
        report["passed"].append("idle engine accepts stop immediately after acknowledged final workspace close")
        if args.skip_crash:
            report["unverified"].append("SIGKILL recovery explicitly skipped")
        else:
            fixture.start_engine()
            crashed = fixture.workspace("crash")
            survivors = fixture.start_services(crashed)
            recorded_marker = (crashed / "sleep0.pid").read_text()
            fixture.engine.kill()
            fixture.engine.wait(timeout=5)
            assert fixture.alive(survivors), "Unexpected automatic service stop on engine SIGKILL"
            fixture.start_engine()
            _, catalogue = fixture.request("GET", "/api/v1/workspaces")
            entry = next(item for item in catalogue["workspaces"] if item["root"] == str(crashed))
            assert entry["state"] == "recovery_required", entry
            rejected = fixture.command("start", "--root", crashed, "--target", "crash/sleep0", "--yes", "--json", check=False)
            assert rejected.returncode != 0, rejected
            assert fixture.alive(survivors)
            assert (crashed / "sleep0.pid").read_text() == recorded_marker, "Recovery launched another fixture process"
            report["passed"].append("engine SIGKILL preserves recorded service identity; restart reports recovery_required and refuses launch replay")
        report["result"] = "passed"
    except Exception as error:
        report.update(result="failed", error=str(error))
        fixture.log.flush()
        report["engine_log_tail"] = (fixture.root / "engine.log").read_text()[-6000:]
    finally:
        report["cleanup_issues"] = fixture.cleanup()
    encoded = json.dumps(report, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded)
    print(encoded, end="")
    return 0 if report["result"] == "passed" and not report["cleanup_issues"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
