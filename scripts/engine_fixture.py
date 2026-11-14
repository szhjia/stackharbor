"""Private fixtures for release engine acceptance; never uses a user's namespace."""
import ctypes
import http.client
import json
import os
import pathlib
import signal
import socket
import subprocess
import sys
import tempfile
import time


def until(check, timeout=20):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            value = check()
            if value:
                return value
        except (OSError, ValueError, AssertionError, RuntimeError) as error:
            last = error
        time.sleep(.1)
    raise AssertionError(f"Engine fixture timed out: {last}")


class DarwinUsage(ctypes.Structure):
    # SDK sys/resource.h, rusage_info_v2. CPU counters use Mach absolute ticks.
    _fields_ = [("uuid", ctypes.c_uint8 * 16)] + [(name, ctypes.c_uint64) for name in (
        "user", "system", "idle_wakeups", "interrupt_wakeups", "pageins", "wired",
        "resident", "footprint", "started", "exited", "child_user", "child_system",
        "child_idle_wakeups", "child_interrupt_wakeups", "child_pageins", "child_elapsed",
        "disk_read", "disk_write")]


_DARWIN = None
_DARWIN_TICK_SECONDS = None


class MachTimebase(ctypes.Structure):
    _fields_ = [("numer", ctypes.c_uint32), ("denom", ctypes.c_uint32)]


def usage(pid):
    """Kernel identity, cumulative own/waited-child CPU, and current RSS."""
    if sys.platform == "darwin":
        global _DARWIN, _DARWIN_TICK_SECONDS
        if _DARWIN is None:
            _DARWIN = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
            _DARWIN.proc_pid_rusage.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_void_p]
            _DARWIN.proc_pid_rusage.restype = ctypes.c_int
            system = ctypes.CDLL("/usr/lib/libSystem.B.dylib")
            system.mach_timebase_info.argtypes = [ctypes.POINTER(MachTimebase)]
            system.mach_timebase_info.restype = ctypes.c_int
            scale = MachTimebase()
            if system.mach_timebase_info(ctypes.byref(scale)) or not scale.denom:
                raise RuntimeError("Cannot determine Mach CPU timebase")
            _DARWIN_TICK_SECONDS = scale.numer / scale.denom / 1e9
        item = DarwinUsage()
        if _DARWIN.proc_pid_rusage(pid, 2, ctypes.byref(item)) or item.exited:
            raise ProcessLookupError(pid)
        return {"identity": item.started, "cpu": (item.user + item.system) * _DARWIN_TICK_SECONDS,
                "children_cpu": (item.child_user + item.child_system) * _DARWIN_TICK_SECONDS, "rss": item.resident}
    if sys.platform.startswith("linux"):
        raw = pathlib.Path(f"/proc/{pid}/stat").read_text()
        fields = raw[raw.rindex(")") + 2:].split()
        if fields[0] == "Z":
            raise ProcessLookupError(pid)
        ticks = os.sysconf("SC_CLK_TCK")
        return {"identity": int(fields[19]), "cpu": (int(fields[11]) + int(fields[12])) / ticks,
                "children_cpu": (int(fields[13]) + int(fields[14])) / ticks,
                "rss": int(fields[21]) * os.sysconf("SC_PAGE_SIZE")}
    raise RuntimeError("Engine acceptance supports macOS and Linux only")


def same_process(pid, identity):
    try:
        return usage(pid)["identity"] == identity
    except (OSError, ValueError):
        return False


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=10)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


class Fixture:
    def __init__(self, binary):
        self.binary = pathlib.Path(binary).expanduser().resolve(strict=True)
        if not self.binary.is_file() or not os.access(self.binary, os.X_OK):
            raise RuntimeError("--binary must identify an existing executable release binary")
        self.temporary = tempfile.TemporaryDirectory(prefix="sh-engine-", dir="/tmp")
        self.root = pathlib.Path(self.temporary.name).resolve()
        self.env = os.environ.copy()
        # Public workspace-open obtains a shell environment. Prevent inherited
        # shell startup overrides from reading a real user's startup files.
        for name in ("ZDOTDIR", "BASH_ENV", "ENV"):
            self.env.pop(name, None)
        # All writes/discovery/engine launch are restricted to this private fixture.
        for name in ("HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME",
                     "STACKHARBOR_CACHE_DIR", "STACKHARBOR_ENGINE_STATE_DIR", "STACKHARBOR_ENGINE_RUNTIME_DIR"):
            target = self.root / name.lower()
            target.mkdir(mode=0o700)
            self.env[name] = str(target)
        self.env.update(STACKHARBOR_ENGINE_MODE="managed", NO_COLOR="1", TERM="xterm-256color",
                        PYTHONPYCACHEPREFIX=str(self.root / "python-cache"), TMPDIR=str(self.root))
        self.engine = None
        self.engine_identity = None
        self.endpoint = None
        self.workspaces = []
        self.services = []
        self.log = open(self.root / "engine.log", "w+")

    def command(self, *args, check=True, timeout=30):
        result = subprocess.run([str(self.binary), *map(str, args)], env=self.env, cwd=self.root,
                                capture_output=True, text=True, timeout=timeout, check=False)
        if check and result.returncode:
            raise AssertionError(f"CLI {args} exited {result.returncode}: {result.stderr}\n{result.stdout}")
        return result

    def start_engine(self):
        self.engine = subprocess.Popen([str(self.binary), "engine", "serve", "--idle-timeout", "24h"],
                                       cwd=self.root, env=self.env, stdout=self.log, stderr=self.log)
        self.engine_identity = usage(self.engine.pid)["identity"]
        def ready():
            if self.engine.poll() is not None:
                self.log.flush()
                raise RuntimeError((self.root / "engine.log").read_text()[-4000:])
            result = self.command("engine", "status", "--json", check=False, timeout=5)
            if not result.returncode:
                endpoint = json.loads(result.stdout)
                assert endpoint["pid"] == self.engine.pid, "Connected to an engine outside the fixture"
                assert pathlib.Path(endpoint["socket_path"]).resolve().is_relative_to(self.root)
                return endpoint
        self.endpoint = until(ready)
        return self.endpoint

    def request(self, method, path, body=None, check=True):
        client = UnixHTTP(self.endpoint["socket_path"])
        try:
            payload = None if body is None else json.dumps(body)
            client.request(method, path, body=payload, headers={"Content-Type": "application/json", "X-StackHarbor-Namespace": self.env["STACKHARBOR_CACHE_DIR"]})
            response = client.getresponse()
            value = json.loads(response.read())
            if check:
                assert 200 <= response.status < 300, (response.status, value)
                assert not value.get("error"), value
            return response.status, value.get("data", value)
        finally:
            client.close()

    def workspace(self, name, services=1):
        root = self.root / name
        root.mkdir(mode=0o700)
        nodes = {}
        for number in range(services):
            key = f"sleep{number}"
            marker = root / f"{key}.pid"
            # A foreground interpreter records its exact PID. No detached process,
            # user project, port, Docker container or installed profile is touched.
            nodes[key] = {"run": {"command": [sys.executable, str(pathlib.Path(__file__).resolve()), str(marker)]},
                          "stop": {"timeout_seconds": 1}}
        registration = {"version": 1, "project": {"id": name, "name": name}, "services": nodes}
        (root / "stackharbor.yaml").write_text(json.dumps(registration))
        self.workspaces.append(root)
        return root

    def start_services(self, root):
        config = json.loads((root / "stackharbor.yaml").read_text())
        for key in config["services"]:
            marker = root / f"{key}.pid"
            try:
                self.command("start", "--root", root, "--target", f"{root.name}/{key}", "--yes", "--json")
            finally:
                if marker.exists():
                    self.record_service(root, key, marker)
            until(lambda: marker.exists())
            self.record_service(root, key, marker)
        return [item for item in self.services if item["root"] == str(root)]

    def record_service(self, root, key, marker):
        item = json.loads(marker.read_text())
        item.update(root=str(root), target=f"{root.name}/{key}")
        if item not in self.services:
            self.services.append(item)
        return item

    def alive(self, services=None):
        return all(same_process(item["pid"], item["identity"]) for item in (services if services is not None else self.services))

    def close(self, root, wait=True):
        self.command("kill", "--root", root, "--yes", "--json")
        if wait:
            until(lambda: not any(same_process(item["pid"], item["identity"]) for item in self.services if item["root"] == str(root)))

    def cleanup(self):
        errors = []
        for root in self.workspaces:
            for marker in root.glob("sleep*.pid"):
                try:
                    self.record_service(root, marker.stem, marker)
                except (OSError, ValueError) as error:
                    errors.append(str(error))
        cleanup_deadline = time.monotonic() + 15
        if self.engine and self.engine.poll() is None:
            for root in self.workspaces:
                remaining = cleanup_deadline - time.monotonic()
                if remaining <= 0:
                    break
                try:
                    self.command("kill", "--root", root, "--yes", "--json", check=False, timeout=min(4, remaining))
                except (OSError, subprocess.TimeoutExpired) as error:
                    errors.append(str(error))
        # Fallback can target only exact fixture-created identities. Never kill a
        # shared engine, search by name, signal a process group or sweep a namespace.
        for item in self.services:
            if same_process(item["pid"], item["identity"]):
                try:
                    os.kill(item["pid"], signal.SIGTERM)
                except ProcessLookupError:
                    pass
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline and any(same_process(i["pid"], i["identity"]) for i in self.services):
            time.sleep(.1)
        for item in self.services:
            if same_process(item["pid"], item["identity"]):
                try:
                    os.kill(item["pid"], signal.SIGKILL)
                except ProcessLookupError:
                    pass
        if self.engine and self.engine.poll() is None and (self.engine_identity is None or same_process(self.engine.pid, self.engine_identity)):
            self.engine.terminate()
            try:
                self.engine.wait(timeout=5)
            except subprocess.TimeoutExpired:
                if same_process(self.engine.pid, self.engine_identity):
                    self.engine.kill()
                self.engine.wait(timeout=5)
        self.log.close()
        self.temporary.cleanup()
        return errors


if __name__ == "__main__":
    # Invoked only as a configured service in a freshly-created private fixture.
    marker = pathlib.Path(sys.argv[1])
    marker.write_text(json.dumps({"pid": os.getpid(), "identity": usage(os.getpid())["identity"]}))
    time.sleep(86400)
