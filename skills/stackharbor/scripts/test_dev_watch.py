"""Exercise the bundled development watcher without application databases."""
import json
import os
import py_compile
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


WATCHER = Path(os.environ.get("STACKHARBOR_TEST_WATCHER", str(Path(__file__).with_name("dev_watch.py"))))


class DevelopmentWatcherTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "src").mkdir()
        (self.root / "src/value.txt").write_text("one")
        (self.root / "build.py").write_text(
            "from pathlib import Path\nimport sys\n"
            "value=Path('src/value.txt').read_text()\n"
            "with Path('builds').open('a') as f:f.write(value+'\\n')\n"
            "if value=='broken':sys.exit(2)\n"
            "Path(sys.argv[1], 'app.py').write_text("
            "\"import os,time\\nfrom pathlib import Path\\n\""
            "+\"with Path('runs').open('a') as f:f.write(str(os.getpid())+' '+\""
            "+repr(value)+\"+'\\\\n')\\nwhile True:time.sleep(.1)\\n\")\n"
        )
        self.service = {
            "cwd": ".", "watch": ["src"],
            "build": [sys.executable, "build.py", "{output}"],
            "run": [sys.executable, "{output}/app.py"],
            "stop_timeout_seconds": 1,
        }

    def start(self):
        config = self.root / "dev.json"
        config.write_text(json.dumps({"services": {"fixture": self.service}}))
        log = (self.root / "watcher.log").open("w")
        self.addCleanup(log.close)
        process = subprocess.Popen([sys.executable, str(WATCHER), str(config), "fixture"],
                                   cwd="/", stdout=log, stderr=log)
        def cleanup():
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=5)
        self.addCleanup(cleanup)
        return process

    def wait_for(self, predicate):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(.05)
        self.fail((self.root / "watcher.log").read_text())

    def runs(self):
        path = self.root / "runs"
        return path.read_text().splitlines() if path.exists() else []

    def assert_dead(self, pid):
        with self.assertRaises(ProcessLookupError):
            os.kill(pid, 0)

    def check_config(self, output):
        config = self.root / "dev.json"
        config.write_text(json.dumps({"output_root": output, "services": {"fixture": self.service}}))
        return subprocess.run(
            [sys.executable, str(WATCHER), str(config), "fixture", "--check"],
            cwd="/", capture_output=True, text=True,
        )

    def test_check_rejects_build_output_inside_watched_sources(self):
        for output in ("src", "src/build"):
            with self.subTest(output=output):
                result = self.check_config(output)
                self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
                self.assertIn("output directory must stay outside watched paths", result.stderr)
                self.assertFalse((self.root / "builds").exists())

    def test_check_accepts_separate_output_without_creating_it_or_running_commands(self):
        result = self.check_config("output")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.root / "output").exists())
        self.assertFalse((self.root / "builds").exists())
        self.assertFalse(self.runs())

    def test_rebuild_replaces_process_and_stop_cleans_up(self):
        process = self.start()
        self.wait_for(lambda: len(self.runs()) == 1)
        old_pid = int(self.runs()[0].split()[0])
        (self.root / "src/value.txt").write_text("two")
        self.wait_for(lambda: len(self.runs()) == 2)
        self.assertTrue(self.runs()[1].endswith(" two"))
        self.assert_dead(old_pid)
        new_pid = int(self.runs()[1].split()[0])
        process.terminate()
        self.assertEqual(process.wait(timeout=5), 143)
        self.assert_dead(new_pid)

    def test_compile_error_retains_last_good_process_then_recovers(self):
        self.start()
        self.wait_for(lambda: len(self.runs()) == 1)
        original = self.runs()[0]
        (self.root / "src/value.txt").write_text("broken")
        self.wait_for(lambda: "broken" in (self.root / "builds").read_text())
        time.sleep(.4)
        self.assertEqual(self.runs(), [original])
        os.kill(int(original.split()[0]), 0)
        (self.root / "src/value.txt").write_text("fixed")
        self.wait_for(lambda: len(self.runs()) == 2)
        self.assertTrue(self.runs()[1].endswith(" fixed"))

    def test_initial_build_failure_waits_for_fix(self):
        (self.root / "src/value.txt").write_text("broken")
        process = self.start()
        self.wait_for(lambda: (self.root / "builds").exists())
        self.assertIsNone(process.poll())
        (self.root / "src/value.txt").write_text("fixed")
        self.wait_for(lambda: len(self.runs()) == 1)

    def test_embedded_asset_addition_and_removal_reload_but_cache_does_not(self):
        self.start()
        self.wait_for(lambda: len(self.runs()) == 1)
        cache = self.root / "src/__pycache__"
        cache.mkdir()
        (cache / "value.pyc").write_bytes(b"ignored")
        time.sleep(.6)
        self.assertEqual(len(self.runs()), 1)
        asset = self.root / "src/template.html"
        asset.write_text("new asset")
        self.wait_for(lambda: len(self.runs()) == 2)
        asset.unlink()
        self.wait_for(lambda: len(self.runs()) == 3)

    def test_python_mode_reimports_source_in_a_new_process(self):
        source = self.root / "src/app.py"
        def content(value):
            return "import time\nfrom pathlib import Path\nwith Path('runs').open('a') as f:f.write('"+value+"\\n')\nwhile True:time.sleep(.1)\n"
        source.write_text(content("before"))
        self.service.pop("build")
        self.service["run"] = ["{python}", "-B", "src/app.py"]
        self.start()
        self.wait_for(lambda: self.runs() == ["before"])
        source.write_text(content("after"))
        self.wait_for(lambda: self.runs() == ["before", "after"])

    def test_python_reload_does_not_reuse_stale_same_timestamp_bytecode(self):
        module = (self.root / 'src/settings.py').resolve()
        module.write_text("value='old'\n")
        cache = str(self.root / 'bytecode')
        previous_prefix = sys.pycache_prefix
        try:
            sys.pycache_prefix = cache
            py_compile.compile(str(module), doraise=True)
        finally:
            sys.pycache_prefix = previous_prefix
        stat = module.stat()
        (self.root / 'src/app.py').write_text("import settings,time\nfrom pathlib import Path\nwith Path('runs').open('a') as f:f.write(settings.value+'\\n')\nwhile True:time.sleep(.1)\n")
        self.service.pop('build')
        self.service['run'] = [sys.executable, '-B', 'src/app.py']
        previous_env = os.environ.get('PYTHONPYCACHEPREFIX')
        os.environ['PYTHONPYCACHEPREFIX'] = cache
        try:
            self.start()
        finally:
            if previous_env is None:
                os.environ.pop('PYTHONPYCACHEPREFIX', None)
            else:
                os.environ['PYTHONPYCACHEPREFIX'] = previous_env
        self.wait_for(lambda: self.runs() == ['old'])
        module.write_text("value='new'\n")
        os.utime(module, ns=(stat.st_atime_ns, stat.st_mtime_ns))
        self.wait_for(lambda: self.runs() == ['old', 'new'])

    def test_signal_during_compilation_stops_compiler(self):
        (self.root / "slow.py").write_text("import os,time\nfrom pathlib import Path\nPath('compiler').write_text(str(os.getpid()))\ntime.sleep(30)\n")
        self.service["build"] = [sys.executable, "slow.py"]
        process = self.start()
        self.wait_for(lambda: (self.root / "compiler").exists())
        compiler = int((self.root / "compiler").read_text())
        process.send_signal(signal.SIGTERM)
        self.assertEqual(process.wait(timeout=5), 143)
        self.assert_dead(compiler)

    def test_stop_cleans_descendant_that_outlives_group_leader(self):
        (self.root / "src/parent.py").write_text(
            "import subprocess,sys,time\n"
            "subprocess.Popen([sys.executable,'-c',\"import os,signal,time;from pathlib import Path;signal.signal(signal.SIGTERM,signal.SIG_IGN);Path('grandchild').write_text(str(os.getpid()));time.sleep(60)\"])\n"
            "while True:time.sleep(.1)\n"
        )
        self.service.pop("build")
        self.service["run"] = [sys.executable, "src/parent.py"]
        process = self.start()
        self.wait_for(lambda: (self.root / "grandchild").exists())
        descendant = int((self.root / "grandchild").read_text())
        def cleanup():
            try:
                os.kill(descendant, signal.SIGKILL)
            except ProcessLookupError:
                pass
        self.addCleanup(cleanup)
        process.terminate()
        self.assertEqual(process.wait(timeout=5), 143)
        def gone():
            try:
                os.kill(descendant, 0)
                result = subprocess.run(['ps', '-o', 'stat=', '-p', str(descendant)], capture_output=True, text=True)
                return result.stdout.strip().startswith('Z')
            except ProcessLookupError:
                return True
        self.wait_for(gone)

    def test_published_child_pid_tracks_reload_and_is_removed_on_stop(self):
        pointer = self.root / "child.pid"
        self.service["pid_file_env"] = "STACKHARBOR_FIXTURE_CHILD_PID"
        previous = os.environ.get("STACKHARBOR_FIXTURE_CHILD_PID")
        os.environ["STACKHARBOR_FIXTURE_CHILD_PID"] = str(pointer)
        try:
            process = self.start()
        finally:
            if previous is None:
                os.environ.pop("STACKHARBOR_FIXTURE_CHILD_PID", None)
            else:
                os.environ["STACKHARBOR_FIXTURE_CHILD_PID"] = previous
        self.wait_for(lambda: len(self.runs()) == 1 and pointer.exists())
        first = int(pointer.read_text())
        self.assertEqual(first, int(self.runs()[0].split()[0]))
        (self.root / "src/value.txt").write_text("changed")
        self.wait_for(lambda: len(self.runs()) == 2 and int(pointer.read_text()) != first)
        self.assertEqual(int(pointer.read_text()), int(self.runs()[1].split()[0]))
        process.terminate()
        self.assertEqual(process.wait(timeout=5), 143)
        self.assertFalse(pointer.exists())


if __name__ == "__main__":
    unittest.main()
