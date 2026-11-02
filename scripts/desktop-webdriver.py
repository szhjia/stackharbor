"""Actual Linux WebKitGTK WebView acceptance through tauri-driver's W3C API."""
import base64
import json
import os
import signal
import subprocess
import time
import urllib.request
import urllib.error
from desktop_test_support import Harness, EVIDENCE, until

h = Harness()
driver = None
session = None
base = "http://127.0.0.1:4444"

def call(method, path, value=None):
    request = urllib.request.Request(base + path, data=None if value is None else json.dumps(value).encode(), headers={"Content-Type": "application/json"}, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            result = json.load(response)["value"]
    except urllib.error.HTTPError as error:
        raise RuntimeError(json.load(error)) from error
    if isinstance(result, dict) and "error" in result:
        raise RuntimeError(result)
    return result

def js(script):
    return call("POST", f"/session/{session}/execute/sync", {"script": script, "args": []})

def click(text):
    return click_element("xpath", "//*[self::button or @role='menuitem'][normalize-space(.)='" + text + "']")

def click_element(using, value):
    def interact():
        element = call("POST", f"/session/{session}/element", {"using": using, "value": value})
        identifier = element["element-6066-11e4-a52e-4f735466cecf"]
        # Retained observations render before the fresh session snapshot arrives.
        # Honor the existing disabled-write boundary and re-find after rerenders.
        if not call("GET", f"/session/{session}/element/{identifier}/enabled"):
            return False
        call("POST", f"/session/{session}/element/{identifier}/click", {})
        return True
    return until(interact)

try:
    slave = h.slave("webview")
    driver = subprocess.Popen(["tauri-driver"], env=h.env, stdout=h.log, stderr=h.log)
    until(lambda: call("GET", "/status"))
    result = call("POST", "/session", {"capabilities": {"alwaysMatch": {"tauri:options": {"application": str(h.app)}}}})
    session = result["sessionId"]
    until(lambda: js("return document.body.innerText.includes('Workspaces')"))
    inventory = until(lambda: h.inventory() if h.inventory()["sessions"] else None)
    sid = inventory["sessions"][0]["identity"]["session_id"]
    url = h.gateway()["url"]
    call("POST", f"/session/{session}/url", {"url": url + "workspaces/" + sid})
    until(lambda: js("return document.body.innerText.includes('Desktop API')"))
    def action(name):
        # Accepted operations navigate to Operations. Re-enter the selected
        # workspace explicitly before issuing each independent next action.
        call("POST", f"/session/{session}/url", {"url": url + "workspaces/" + sid})
        until(lambda: js("return document.body.innerText.includes('Desktop API')"))
        click_element("css selector", 'button[aria-label="Actions for desktop/api"]')
        click(name)
        until(lambda: js("return !!document.querySelector('[role=dialog]')"))
        click_element("xpath", "//*[@role='dialog']//button[starts-with(normalize-space(.),'Confirm ')]")
        until(lambda: js("return !document.querySelector('[role=dialog]')"))
    action("Start")
    until(lambda: h.assert_service(slave))
    previous = h.gateway()
    os.kill(previous["pid"], signal.SIGTERM)
    def reopened():
        h.request("", background=False)
        current = h.gateway()
        return current if current["url"] != previous["url"] else None
    replacement = until(reopened)
    url = replacement["url"]
    until(lambda: js("return location.href.startsWith(" + json.dumps(url) + ") && document.body.innerText.includes('Workspaces')"))
    until(lambda: h.assert_service(slave))
    action("Restart")
    until(lambda: h.assert_service(slave))
    action("Stop")
    until(lambda: not json.loads(h.command("status", "--root", str(slave["root"]), "--json").stdout)["processes"])
    call("POST", f"/session/{session}/url", {"url": url + "workspaces/" + sid})
    until(lambda: js("return document.body.innerText.includes('Desktop API')"))
    click("Close session")
    click_element("xpath", "//*[@role='dialog']//button[normalize-space(.)='Confirm close']")
    assert slave["process"].wait(timeout=12) == 0
    call("POST", f"/session/{session}/url", {"url": url + "workspaces/" + sid})
    until(lambda: js("return document.body.innerText.includes('ended or disappeared')"))
    screenshot = call("GET", f"/session/{session}/screenshot")
    (EVIDENCE / "webview.png").write_bytes(base64.b64decode(screenshot))
    (EVIDENCE / "webview.json").write_text(json.dumps({"passed": ["real WebKitGTK render and cookie/CSRF bootstrap", "owned gateway replacement recovers same WebView with fresh authentication", "service start/restart/stop through existing plan UI", "close-session restores terminal", "no native invocation bridge required"]}, indent=2) + "\n")
    print("Real WebView service controls and session close passed")
except Exception as error:
    if session:
        try:
            (EVIDENCE / "webview-failure.png").write_bytes(base64.b64decode(call("GET", f"/session/{session}/screenshot")))
            (EVIDENCE / "webview-failure.txt").write_text(str(error) + "\n" + str(js("return document.body.innerText")))
        except Exception:
            pass
    raise
finally:
    if session:
        try:
            call("DELETE", f"/session/{session}")
        except OSError:
            pass
    h.cleanup()
    if driver:
        driver.terminate()
        driver.wait(timeout=10)
