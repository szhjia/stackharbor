"""Native master lifecycle, real CLI/PTY slaves and ownership acceptance."""
import concurrent.futures
import json
import os
import signal
import subprocess
import urllib.error
import urllib.request
from desktop_test_support import Harness, EVIDENCE, until, native_master_pids

def no_process(pid):
    try:
        os.kill(pid, 0)
        return False
    except ProcessLookupError:
        return True

def serving(url):
    try:
        urllib.request.urlopen(url, timeout=.5).close()
        return True
    except (OSError, urllib.error.URLError):
        return False

def native_window_at(h, url):
    return any(w["visible"] and w["url"].startswith(url) for w in h.request()["state"]["windows"])

def reopen_replacement(h, previous_url):
    h.request("", background=False)
    current = h.gateway()
    return current if current["url"] != previous_url else None

h = Harness()
passed = []
try:
    with concurrent.futures.ThreadPoolExecutor(2) as pool:
        slaves = list(pool.map(lambda name: h.slave(name, desktop=True), ["alpha", "beta"]))
    state = until(lambda: h.request())
    first_master_pid = state["pid"]
    assert not state["state"]["windows"], state
    assert len(h.request()["state"]["gateways"]) == 1
    if os.uname().sysname == "Darwin":
        assert native_master_pids() == [first_master_pid], native_master_pids()
        alternate = h.root / "alternate-runtime"
        alternate.mkdir(mode=0o700)
        attempt = subprocess.Popen([h.app, "--background"], env=dict(h.env, STACKHARBOR_DESKTOP_RUNTIME_DIR=str(alternate)), stdout=h.log, stderr=h.log)
        h.children.append(attempt)
        assert attempt.wait(timeout=12) == 0
        assert not (alternate / "master.sock").exists()
        assert native_master_pids() == [first_master_pid], native_master_pids()
        passed.append("macOS runtime override cannot bypass the single application instance")
    passed.append("concurrent cold CLI launches: one background master, no window")
    for slave in slaves:
        h.start_service(slave)
    until(lambda: len(h.inventory()["sessions"]) == 2)
    until(lambda: h.inventory()["totals"]["processes"]["count"] >= 2)
    passed.append("global inventory includes both terminal-owned applications")
    h.command("desktop")
    until(lambda: any(w["visible"] for w in h.request()["state"]["windows"]))
    separate = h.root / "separate-namespace"
    separate.mkdir(mode=0o700)
    separate_env = dict(h.env, STACKHARBOR_CACHE_DIR=str(separate))
    subprocess.run([h.cli, "desktop"], env=separate_env, capture_output=True, text=True, timeout=15, check=True)
    state = until(lambda: h.request() if len(h.request()["state"]["windows"]) == 2 else None)
    assert state["pid"] == first_master_pid and len(state["state"]["gateways"]) == 2
    isolated = next(g for g in state["state"]["gateways"] if g["namespace"] == str(separate))
    assert not h.inventory(isolated["url"])["sessions"] and len(h.inventory()["sessions"]) == 2
    h.request("close-window", namespace=separate)
    passed.append("one master maintains isolated namespace gateways, windows and totals")
    h.request("close-window")
    until(lambda: h.request()["state"]["windows"] and not any(w["visible"] for w in h.request()["state"]["windows"]))
    passed.append("lazy window creation and close-to-hide")
    previous = h.gateway()
    os.kill(previous["pid"], signal.SIGTERM)
    until(lambda: not serving(previous["url"]))
    h.command("desktop")
    replacement = until(lambda: reopen_replacement(h, previous["url"]))
    until(lambda: native_window_at(h, replacement["url"]))
    passed.append("owned gateway replacement updates the existing native WebView origin")
    h.request("quit")
    until(lambda: no_process(first_master_pid), 12)
    for slave in slaves:
        h.assert_service(slave)
    passed.append("explicit master quit preserves both slaves and services")
    h.command("desktop")
    until(lambda: len(h.inventory()["sessions"]) == 2)
    passed.append("CLI launches a fresh master and rediscovers existing slaves")
    for slave in slaves:
        h.end_slave(slave)
    until(lambda: len(h.inventory()["sessions"]) == 0)
    assert h.request()["state"]["gateways"]
    passed.append("last slave exit leaves the empty master running")
    h.request("quit")
    until(lambda: not (h.runtime / "master.sock").exists())
    # A production desktop must reject a development gateway and report the
    # failure to the CLI while keeping both its owner and the terminal usable.
    development = subprocess.Popen([h.cli, "web", "--port", "0", "--no-open", "--dev-origin", "http://127.0.0.1:5173"], env=h.env, stdout=subprocess.PIPE, stderr=h.log, text=True)
    h.children.append(development)
    assert development.stdout.readline().strip().startswith("http://127.0.0.1:")
    rejected = h.command("desktop", check=False)
    assert rejected.returncode != 0 and "development gateway" in rejected.stderr, rejected
    assert not h.request()["state"]["windows"]
    fallback = h.slave("unavailable-desktop", desktop=True)
    assert b"Desktop:" in fallback["output"] and b"development gateway" in fallback["output"], fallback["output"]
    assert development.poll() is None and fallback["process"].poll() is None
    h.end_slave(fallback)
    h.request("quit")
    until(lambda: not (h.runtime / "master.sock").exists())
    development.terminate()
    assert development.wait(timeout=10) == 0
    passed.append("gateway readiness failure reaches CLI, preserves TUI and independent owner, creates no window")
    # Independent web owner remains alive after desktop reuses it and quits.
    web = subprocess.Popen([h.cli, "web", "--port", "0", "--no-open"], env=h.env, stdout=subprocess.PIPE, stderr=h.log, text=True)
    h.children.append(web)
    external_url = web.stdout.readline().strip()
    assert external_url.startswith("http://127.0.0.1:")
    master = h.master()
    h.command("desktop")
    until(lambda: native_window_at(h, external_url))
    assert not h.gateway()["owned"] and h.gateway()["url"] == external_url
    h.request("quit")
    assert master.wait(timeout=12) == 0
    assert web.poll() is None
    urllib.request.urlopen(external_url, timeout=3).close()
    passed.append("reused independent web gateway survives master quit")
    master = h.master()
    h.command("desktop")
    until(lambda: native_window_at(h, external_url))
    web.terminate()
    assert web.wait(timeout=10) == 0
    h.command("desktop")
    replacement = until(lambda: reopen_replacement(h, external_url))
    until(lambda: native_window_at(h, replacement["url"]))
    passed.append("reused gateway replacement recovers inside the same native WebView")
    h.request("quit")
    assert master.wait(timeout=12) == 0
    master = h.master()
    h.request("", background=True)
    owned_url = h.gateway()["url"]
    os.kill(master.pid, signal.SIGKILL)
    master.wait(timeout=5)
    def stopped():
        try:
            urllib.request.urlopen(owned_url, timeout=.5).close()
            return False
        except (OSError, urllib.error.URLError):
            return True
    until(stopped, 10)
    passed.append("master crash closes parent pipe and cleans its owned gateway")
    # A stale private socket is safely recovered by the next master.
    master = h.master()
    passed.append("restart after crash safely recovers its stale endpoint")
    (EVIDENCE / "smoke.json").write_text(json.dumps({"passed": passed, "version": h.version}, indent=2) + "\n")
    print(json.dumps({"passed": passed}, indent=2))
except Exception:
    (EVIDENCE / "smoke-failure.txt").write_text("\n\n".join(item["output"].decode(errors="replace") for item in h.slaves))
    raise
finally:
    h.cleanup()
