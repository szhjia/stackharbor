"""Hermetic contracts for acceptance tooling; no real StackHarbor profile."""
import importlib.util
import contextlib
import http.server
import io
import json
import socketserver
import threading
import os
import pathlib
import subprocess
import sys
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock
from engine_fixture import Fixture, same_process, until, usage


def load(name):
    spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).with_name(name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FixtureTests(unittest.TestCase):
    def test_environment_is_private_and_parent_environment_unchanged(self):
        before = dict(os.environ)
        fixture = Fixture(sys.executable)
        try:
            self.assertEqual(os.environ, before)
            for key in ("HOME", "XDG_CACHE_HOME", "STACKHARBOR_CACHE_DIR", "STACKHARBOR_ENGINE_RUNTIME_DIR", "STACKHARBOR_ENGINE_STATE_DIR"):
                directory = pathlib.Path(fixture.env[key])
                self.assertTrue(directory.is_relative_to(fixture.root))
                self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
            self.assertEqual(fixture.env["STACKHARBOR_ENGINE_MODE"], "managed")
            for name in ("ZDOTDIR", "BASH_ENV", "ENV"):
                self.assertNotIn(name, fixture.env)
        finally:
            fixture.cleanup()

    def test_public_workspace_request_uses_namespace_header_and_minimal_body(self):
        fixture = Fixture(sys.executable)
        recorded = {}
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                recorded.update(path=self.path, namespace=self.headers.get("X-StackHarbor-Namespace"),
                                body=json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"data":{"opened":true}}')

            def log_message(self, *args):
                pass
        path = fixture.root / "request.sock"
        server = socketserver.UnixStreamServer(str(path), Handler)
        fixture.endpoint = {"socket_path": str(path)}
        worker = threading.Thread(target=server.handle_request, daemon=True)
        worker.start()
        try:
            status, response = fixture.request("POST", "/api/v1/workspaces/open", {"root": str(fixture.root)})
            self.assertEqual(status, 200)
            self.assertTrue(response["opened"])
            self.assertEqual(recorded, {"path": "/api/v1/workspaces/open",
                                       "namespace": fixture.env["STACKHARBOR_CACHE_DIR"],
                                       "body": {"root": str(fixture.root)}})
        finally:
            server.server_close()
            worker.join(timeout=2)
            fixture.cleanup()

    def test_cleanup_only_signals_exact_fixture_process_identity(self):
        fixture = Fixture(sys.executable)
        owned = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        fixture.services = [{"pid": owned.pid, "identity": usage(owned.pid)["identity"], "root": str(fixture.root)},
                            {"pid": unrelated.pid, "identity": usage(unrelated.pid)["identity"] + 1, "root": str(fixture.root)}]
        try:
            fixture.cleanup()
            owned.wait(timeout=4)
            self.assertIsNone(unrelated.poll())
        finally:
            for child in (owned, unrelated):
                if child.poll() is None:
                    child.terminate()
                child.wait(timeout=4)

    def test_service_records_kernel_identity_for_failure_cleanup(self):
        fixture = Fixture(sys.executable)
        root = fixture.workspace("example")
        import json
        config = json.loads((root / "stackharbor.yaml").read_text())
        command = config["services"]["sleep0"]["run"]["command"]
        child = subprocess.Popen(command)
        marker = root / "sleep0.pid"
        try:
            until(lambda: marker.exists())
            recorded = json.loads(marker.read_text())
            self.assertEqual(recorded["pid"], child.pid)
            self.assertTrue(same_process(recorded["pid"], recorded["identity"]))
            # Simulate a CLI timeout before start_services recorded its result.
            fixture.cleanup()
            child.wait(timeout=4)
        finally:
            if child.poll() is None:
                child.terminate()
                child.wait(timeout=4)

    def test_cpu_includes_short_lived_waited_child(self):
        before = usage(os.getpid())["children_cpu"]
        subprocess.run([sys.executable, "-c", "import time; end=time.process_time()+.08\nwhile time.process_time()<end: pass"], check=True)
        after = usage(os.getpid())["children_cpu"]
        self.assertGreater(after - before, .04)

    def test_sampling_excludes_fixture_services_and_retains_pid_accounting(self):
        performance = load("engine-performance")
        child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        try:
            own = usage(os.getpid())
            result = performance.sample({os.getpid(): own["identity"]}, {child.pid})
            self.assertIn(os.getpid(), result["processes"])
            self.assertNotIn(child.pid, result["processes"])
            self.assertGreaterEqual(result["rss"], own["rss"])
        finally:
            child.terminate()
            child.wait(timeout=4)

    def test_explicit_observer_is_measured_but_survives_fixture_cleanup(self):
        performance = load("engine-performance")
        fixture = Fixture(sys.executable)
        owned = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        observer = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        fixture.services = [{"pid": owned.pid, "identity": usage(owned.pid)["identity"], "root": str(fixture.root)}]
        identity = usage(observer.pid)["identity"]
        try:
            roots = {}
            # Only the platform-signature verifier is replaced. Identity,
            # sampling and fixture cleanup operate on independent real PIDs.
            with mock.patch.object(performance, "inspect_apple_webkit", return_value={"pid": observer.pid, "role": "test observer"}):
                metadata = performance.bind_observers(roots, [(observer.pid, identity)])
            self.assertEqual(metadata[0]["identity"], identity)
            measured = performance.sample(roots, set())
            self.assertIn(observer.pid, measured["processes"])
            fixture.cleanup()
            owned.wait(timeout=4)
            self.assertIsNone(observer.poll(), "External observer was incorrectly treated as a fixture cleanup target")
        finally:
            for child in (owned, observer):
                if child.poll() is None:
                    child.terminate()
                child.wait(timeout=4)

    def test_wrong_observer_birth_refused_before_platform_verification(self):
        performance = load("engine-performance")
        observer = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        try:
            with mock.patch.object(performance, "inspect_apple_webkit") as verifier:
                with self.assertRaisesRegex(RuntimeError, "changed identity"):
                    performance.bind_observers({}, [(observer.pid, usage(observer.pid)["identity"] + 1)])
                verifier.assert_not_called()
            self.assertIsNone(observer.poll())
        finally:
            observer.terminate()
            observer.wait(timeout=4)

    def test_explicit_root_exit_invalidates_measurement(self):
        performance = load("engine-performance")
        observer = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        identity = usage(observer.pid)["identity"]
        observer.terminate()
        observer.wait(timeout=4)
        with self.assertRaisesRegex(RuntimeError, "exited or changed identity"):
            performance.sample({observer.pid: identity}, set())

    def test_observer_requires_native_pid_before_creating_fixture(self):
        performance = load("engine-performance")
        with mock.patch.object(sys, "argv", ["engine-performance", "--binary", sys.executable, "--observer", "123:456"]):
            with mock.patch.object(performance, "Fixture", side_effect=AssertionError("Fixture must not start")):
                with contextlib.redirect_stderr(io.StringIO()):
                    with self.assertRaises(SystemExit) as error:
                        performance.main()
                self.assertEqual(error.exception.code, 2)

    def test_observer_verifier_refuses_user_copies_and_wrong_owner(self):
        performance = load("engine-performance")
        for metadata in ({"uid": os.getuid(), "executable": sys.executable},
                         {"uid": os.getuid() + 1, "executable": "/System/Library/Frameworks/WebKit.framework/Versions/A/XPCServices/com.apple.WebKit.Networking.xpc/Contents/MacOS/com.apple.WebKit.Networking"}):
            with self.subTest(metadata=metadata):
                with mock.patch.object(performance.sys, "platform", "darwin"):
                    with mock.patch.object(performance, "process_metadata", return_value=metadata):
                        with self.assertRaisesRegex(RuntimeError, "allowlisted"):
                            performance.inspect_apple_webkit(123)

    def test_observer_requires_kernel_valid_and_apple_platform_flags(self):
        performance = load("engine-performance")
        metadata = {"uid": os.getuid(), "executable": "/System/Library/Frameworks/WebKit.framework/Versions/A/XPCServices/com.apple.WebKit.Networking.xpc/Contents/MacOS/com.apple.WebKit.Networking"}
        for flags in (0, 1, 0x04000000):
            with self.subTest(flags=flags):
                with mock.patch.object(performance.sys, "platform", "darwin"), mock.patch.object(performance, "process_metadata", return_value=metadata), mock.patch.object(pathlib.Path, "stat", return_value=SimpleNamespace(st_uid=0)), mock.patch.object(performance, "kernel_code_signing_flags", return_value=flags):
                    with self.assertRaisesRegex(RuntimeError, "valid Apple platform signature"):
                        performance.inspect_apple_webkit(123)

    def test_background_guard_allows_existing_hidden_window_removal(self):
        performance = load("engine-performance")
        with mock.patch.object(performance, "native_windows", side_effect=[
                [{"id": 10, "onscreen": False}], []]):
            guard = performance.NativeBackgroundGuard(123)
            guard.check()
        self.assertEqual(guard.report()["checks"], 2)

    def test_background_guard_refuses_initial_visible_window(self):
        performance = load("engine-performance")
        with mock.patch.object(performance, "native_windows", return_value=[{"id": 10, "onscreen": True}]):
            with self.assertRaisesRegex(RuntimeError, "onscreen"):
                performance.NativeBackgroundGuard(123)

    def test_background_guard_refuses_later_visible_or_minimized_new_window(self):
        performance = load("engine-performance")
        for visible in (True, False):
            with self.subTest(visible=visible):
                with mock.patch.object(performance, "native_windows", side_effect=[[], [{"id": 10, "onscreen": visible}]]):
                    guard = performance.NativeBackgroundGuard(123)
                    with self.assertRaisesRegex(RuntimeError, "precondition changed"):
                        guard.check()

    def test_background_guard_failure_cleans_private_fixture_and_reports_failure(self):
        performance = load("engine-performance")
        fixture = SimpleNamespace(binary=pathlib.Path(sys.executable), engine=SimpleNamespace(pid=123),
                                  engine_identity=456, services=[], command=mock.Mock(return_value=SimpleNamespace(stdout="StackHarbor")),
                                  start_engine=mock.Mock(), cleanup=mock.Mock(return_value=[]))
        output = io.StringIO()
        argv = ["engine-performance", "--binary", sys.executable, "--native-pid", "789",
                "--require-native-background", "--warmup", "0", "--duration", ".1"]
        with mock.patch.object(sys, "argv", argv), mock.patch.object(performance.sys, "platform", "darwin"), \
                mock.patch.object(performance, "Fixture", return_value=fixture), \
                mock.patch.object(performance, "process_metadata", return_value={"uid": os.getuid(), "executable": "/test/stackharbor-desktop", "sha256": "test"}), \
                mock.patch.object(performance, "usage", return_value={"identity": 999}), \
                mock.patch.object(performance, "sample", return_value={"at": 1}), \
                mock.patch.object(performance, "native_windows", side_effect=[[], [{"id": 10, "onscreen": True}]]), \
                contextlib.redirect_stdout(output):
            self.assertEqual(performance.main(), 1)
        fixture.cleanup.assert_called_once()
        report = json.loads(output.getvalue())
        self.assertEqual(report["result"], "failed")
        self.assertIn("precondition changed", report["error"])
        self.assertEqual(report["cleanup_issues"], [])

    def test_background_guard_requires_native_pid_before_creating_fixture(self):
        performance = load("engine-performance")
        with mock.patch.object(sys, "argv", ["engine-performance", "--binary", sys.executable, "--require-native-background"]):
            with mock.patch.object(performance, "Fixture", side_effect=AssertionError("Fixture must not start")):
                with contextlib.redirect_stderr(io.StringIO()):
                    with self.assertRaises(SystemExit) as error:
                        performance.main()
                self.assertEqual(error.exception.code, 2)


if __name__ == "__main__":
    unittest.main()
