"""Isolated native acceptance helpers. Only fixture-owned PIDs are signalled."""
import fcntl
import http.cookiejar
import json
import os
import pathlib
import pty
import socket
import struct
import subprocess
import tempfile
import termios
import threading
import time
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
EVIDENCE = ROOT / "_runtime_verification/desktop"
EVIDENCE.mkdir(parents=True, exist_ok=True)


def until(fn, timeout=30):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            value = fn()
            if value:
                return value
        except (OSError, ValueError, RuntimeError, AssertionError) as error:
            last = error
        time.sleep(.1)
    raise AssertionError(f"Native acceptance timed out: {last}")


def native_master_pids():
    rows = subprocess.check_output(["ps", "-axo", "pid=,comm="], text=True).splitlines()
    return [int(row.split(maxsplit=1)[0]) for row in rows if len(row.split(maxsplit=1)) == 2 and pathlib.Path(row.split(maxsplit=1)[1].strip()).name == "stackharbor-desktop"]


class Harness:
    def __init__(self):
        if os.uname().sysname == "Darwin":
            # Native acceptance owns a whole master lifecycle. Never bypass the
            # macOS singleton or quit an already-running user's application.
            running = native_master_pids()
            if running:
                raise RuntimeError("Native macOS acceptance requires a dedicated session with no running StackHarbor app; existing master preserved (PIDs " + ", ".join(map(str, running)) + ").")
        self.temporary = tempfile.TemporaryDirectory(prefix="sh-desktop-")
        self.root = pathlib.Path(self.temporary.name).resolve()
        target = (ROOT / "desktop/generated/target").read_text().strip()
        release = ROOT / "desktop/target" / target / "release"
        self.app = release / "stackharbor-desktop"
        if os.uname().sysname == "Darwin":
            self.app = release / "bundle/macos/StackHarbor.app/Contents/MacOS/stackharbor-desktop"
        if os.environ.get("STACKHARBOR_DESKTOP_TEST_APP"):
            self.app = pathlib.Path(os.environ["STACKHARBOR_DESKTOP_TEST_APP"]).resolve()
        self.cli = self.app.parent / "stackharbor"
        assert self.app.is_file() and self.cli.is_file(), "Run make desktop first"
        self.version = subprocess.check_output([self.app, "--desktop-version"], text=True).strip()
        self.namespace = self.root / "cache/stackharbor"
        self.runtime = self.root / "run"
        self.namespace.mkdir(parents=True, mode=0o700)
        self.runtime.mkdir(mode=0o700)
        self.env = os.environ.copy()
        for key in ("SSH_CONNECTION", "SSH_TTY"):
            self.env.pop(key, None)
        self.env.update(STACKHARBOR_CACHE_DIR=str(self.namespace), STACKHARBOR_DESKTOP_RUNTIME_DIR=str(self.runtime),
                        STACKHARBOR_DESKTOP_APP=str(self.app), XDG_CACHE_HOME=str(self.root / "cache"), NO_COLOR="1", TERM="xterm-256color")
        self.slaves = []
        self.children = []
        self.jar = http.cookiejar.CookieJar()
        self.http = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.log = open(EVIDENCE / "native.log", "a")

    def command(self, *args, check=True):
        result = subprocess.run([self.cli, *args], env=self.env, capture_output=True, text=True, timeout=15, check=False)
        if check and result.returncode:
            raise AssertionError(f"CLI {args} exited {result.returncode}: {result.stderr}\n{result.stdout}")
        return result

    def request(self, action="inspect", background=True, namespace=None, write_delay=0):
        request = dict(protocol_version=1, build_version=self.version, namespace=str(namespace or self.namespace), background=background, action=action)
        with socket.socket(socket.AF_UNIX) as client:
            client.settimeout(30)
            client.connect(str(self.runtime / "master.sock"))
            if write_delay:
                time.sleep(write_delay)
            client.sendall(json.dumps(request).encode() + b"\n")
            result = json.loads(client.makefile("rb").readline(16385))
        assert not result.get("error"), result
        assert result["build_version"] == self.version and result["protocol_version"] == 1
        return result

    def master(self):
        process = subprocess.Popen([self.app, "--background"], env=self.env, stdout=self.log, stderr=self.log)
        self.children.append(process)
        until(lambda: self.request())
        return process

    def gateway(self):
        return until(lambda: next((g for g in self.request()["state"]["gateways"] if g["namespace"] == str(self.namespace)), None))

    def inventory(self, url=None):
        url = url or self.gateway()["url"]
        self.http.open(url + "api/v1/auth/session", timeout=5).close()
        with self.http.open(url + "api/v1/inventory", timeout=5) as response:
            result = json.load(response)
        assert not result.get("error"), result
        return result["data"]

    def slave(self, name, desktop=False):
        root = self.root / name
        root.mkdir(mode=0o700)
        registration = {"version": 1, "project": {"id": "desktop", "name": name}, "services": {
            "api": {"name": "Desktop API", "run": {"command": ["/bin/sh", "-c", "printf 'desktop-fixture-ready\\n'; exec sleep 240"]}, "stop": {"timeout_seconds": 1}}}}
        (root / "stackharbor.yaml").write_text(json.dumps(registration))
        master, slave = pty.openpty()
        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 120, 0, 0))
        args = [self.cli, "run", "--root", str(root)]
        if not desktop:
            args.append("--no-desktop")
        process = subprocess.Popen(args, env=self.env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)
        item = {"process": process, "pty": master, "root": root, "output": bytearray()}
        self.slaves.append(item)
        def drain():
            try:
                while True:
                    data = os.read(master, 8192)
                    if not data:
                        return
                    item["output"].extend(data)
            except OSError:
                pass
        threading.Thread(target=drain, daemon=True).start()
        until(lambda: b"Dashboard" in item["output"] or (_ for _ in ()).throw(RuntimeError(item["output"][-3000:].decode(errors="replace"))))
        return item

    def start_service(self, slave):
        self.command("start", "--root", str(slave["root"]), "--target", "desktop/api", "--yes")

    def assert_service(self, slave):
        result = json.loads(self.command("status", "--root", str(slave["root"]), "--json").stdout)
        assert result["processes"], result
        return result

    def end_slave(self, slave):
        os.write(slave["pty"], b"q")
        assert slave["process"].wait(timeout=12) == 0, slave["output"][-3000:]

    def cleanup(self):
        try:
            self.request("quit")
            until(lambda: not (self.runtime / "master.sock").exists(), 10)
        except (OSError, ValueError, AssertionError):
            pass
        for item in self.slaves:
            process = item["process"]
            if process.poll() is None:
                try:
                    self.end_slave(item)
                except (OSError, subprocess.TimeoutExpired, AssertionError):
                    process.terminate()
                    try:
                        process.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()
            os.close(item["pty"])
        for process in self.children:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
        self.log.close()
        self.temporary.cleanup()
