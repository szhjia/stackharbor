#!/usr/bin/env python3
"""Measure an existing release engine and optional idle native app, without installation."""
import argparse
import ctypes
import hashlib
import json
import os
import pathlib
import platform
import plistlib
import resource
import statistics
import subprocess
import sys
import time
from engine_fixture import Fixture, same_process, usage


WEBKIT_HELPERS = {"com.apple.WebKit.Networking", "com.apple.WebKit.GPU", "com.apple.WebKit.WebContent"}
WEBKIT_ROOTS = ("/System/Library/Frameworks/WebKit.framework/Versions/A/XPCServices",
                "/System/Volumes/Preboot/Cryptexes/OS/System/Library/Frameworks/WebKit.framework/Versions/A/XPCServices")


def native_windows(pid):
    """Read window metadata only; no accessibility, UI actions or screenshots."""
    graphics = ctypes.CDLL("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
    foundation = ctypes.CDLL("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
    graphics.CGWindowListCopyWindowInfo.argtypes = [ctypes.c_uint32, ctypes.c_uint32]
    graphics.CGWindowListCopyWindowInfo.restype = ctypes.c_void_p
    foundation.CFPropertyListCreateData.argtypes = [ctypes.c_void_p, ctypes.c_void_p, ctypes.c_long, ctypes.c_ulong, ctypes.c_void_p]
    foundation.CFPropertyListCreateData.restype = ctypes.c_void_p
    foundation.CFDataGetLength.argtypes = [ctypes.c_void_p]
    foundation.CFDataGetLength.restype = ctypes.c_long
    foundation.CFDataGetBytePtr.argtypes = [ctypes.c_void_p]
    foundation.CFDataGetBytePtr.restype = ctypes.POINTER(ctypes.c_ubyte)
    foundation.CFRelease.argtypes = [ctypes.c_void_p]
    array = graphics.CGWindowListCopyWindowInfo(0, 0)
    data = None
    try:
        if not array:
            raise RuntimeError("Native background guard cannot read window metadata")
        data = foundation.CFPropertyListCreateData(None, array, 100, 0, None)
        if not data:
            raise RuntimeError("Native background guard cannot decode window metadata")
        rows = plistlib.loads(ctypes.string_at(foundation.CFDataGetBytePtr(data), foundation.CFDataGetLength(data)))
        return [{"id": w["kCGWindowNumber"], "onscreen": bool(w.get("kCGWindowIsOnscreen"))}
                for w in rows if w.get("kCGWindowOwnerPID") == pid]
    finally:
        if data:
            foundation.CFRelease(data)
        if array:
            foundation.CFRelease(array)


class NativeBackgroundGuard:
    def __init__(self, pid):
        self.pid = pid
        self.baseline = None
        self.checks = 0
        self.last = []
        self.check()

    def check(self):
        self.last = native_windows(self.pid)
        self.checks += 1
        identities = {row["id"] for row in self.last}
        if any(row["onscreen"] for row in self.last):
            raise RuntimeError("Native background precondition changed: a window is onscreen; close it before repeating measurement")
        if self.baseline is not None and not identities.issubset(self.baseline):
            raise RuntimeError("Native background precondition changed: a new window exists, including hidden/minimized windows")
        if self.baseline is None:
            self.baseline = identities

    def report(self):
        return {"pid": self.pid, "checks": self.checks, "initial_hidden_window_ids": sorted(self.baseline or []),
                "last_windows": self.last,
                "scope": "Window metadata checked at warmup and every sample; a window entirely between samples is not ruled out"}


def observer_spec(value):
    try:
        pid, identity = map(int, value.split(":"))
        if pid <= 1 or identity <= 0:
            raise ValueError()
        return pid, identity
    except ValueError:
        raise argparse.ArgumentTypeError("Expected an independently attributed PID:KERNEL_BIRTH pair")


def process_metadata(pid):
    row = subprocess.check_output(["ps", "-ww", "-p", str(pid), "-o", "uid=,comm="], text=True, timeout=5).strip()
    owner, command = row.split(maxsplit=1)
    executable = pathlib.Path(command).resolve(strict=True)
    return {"pid": pid, "uid": int(owner), "executable": str(executable),
            "sha256": hashlib.sha256(executable.read_bytes()).hexdigest()}


def inspect_apple_webkit(pid):
    if sys.platform != "darwin":
        raise RuntimeError("Explicit Apple WebKit observer roots require macOS")
    metadata = process_metadata(pid)
    executable = pathlib.Path(metadata["executable"])
    name = executable.name
    allowed = name in WEBKIT_HELPERS and any(
        executable == pathlib.Path(root) / (name + ".xpc") / "Contents/MacOS" / name
        for root in WEBKIT_ROOTS)
    if metadata["uid"] != os.getuid() or not allowed or executable.stat().st_uid != 0:
        raise RuntimeError("Observer must be a current-user Apple WebKit helper at an allowlisted system path")
    flags = kernel_code_signing_flags(pid)
    # Apple XNU CS_VALID and CS_PLATFORM_BINARY, from osfmk/kern/cs_blobs.h.
    # Verify the running image. System Cryptex bundles can intentionally carry
    # legacy resource-envelope rules rejected by file-level codesign --strict.
    if flags & 0x04000001 != 0x04000001:
        raise RuntimeError("Observer lacks a valid Apple platform signature in kernel status")
    metadata.update(role="explicit_apple_webkit_helper", kernel_code_signing_flags=hex(flags),
                    kernel_apple_platform_verified=True,
                    attribution="independently supplied; relationship to native app is not inferred from its name")
    return metadata


def kernel_code_signing_flags(pid):
    # Read-only CS_OPS_STATUS (0); no mutation csops operations are exposed.
    system = ctypes.CDLL("/usr/lib/libSystem.B.dylib", use_errno=True)
    system.csops.argtypes = [ctypes.c_int, ctypes.c_uint, ctypes.c_void_p, ctypes.c_size_t]
    system.csops.restype = ctypes.c_int
    flags = ctypes.c_uint32()
    if system.csops(pid, 0, ctypes.byref(flags), ctypes.sizeof(flags)):
        raise OSError(ctypes.get_errno(), "Cannot read observer kernel code-signing status")
    return flags.value


def bind_observers(roots, specifications):
    metadata = []
    for pid, identity in specifications:
        if pid in roots:
            raise RuntimeError("Duplicate measured observer root")
        if not same_process(pid, identity):
            raise RuntimeError(f"Observer {pid} exited or changed identity before measurement")
        item = inspect_apple_webkit(pid)
        if not same_process(pid, identity):
            raise RuntimeError(f"Observer {pid} identity changed during verification")
        item["identity"] = identity
        roots[pid] = identity
        metadata.append(item)
    return metadata


def process_tree(roots):
    # This short-lived sampling helper is included in sampler_children_cpu_seconds.
    rows = subprocess.check_output(["ps", "-axo", "pid=,ppid="], text=True, timeout=5)
    parents = {int(row.split()[0]): int(row.split()[1]) for row in rows.splitlines() if len(row.split()) == 2}
    included = set(roots)
    while True:
        expanded = included | {pid for pid, parent in parents.items() if parent in included}
        if included == expanded:
            return included
        included = expanded


def sample(roots, excluded):
    values = {}
    missed = []
    for pid in process_tree(roots):
        if pid in excluded:
            continue
        try:
            values[pid] = usage(pid)
        except (OSError, ValueError):
            missed.append(pid)
    for pid, identity in roots.items():
        if pid not in values or values[pid]["identity"] != identity:
            raise RuntimeError(f"Measured root {pid} exited or changed identity")
    return {"at": time.monotonic(), "processes": values,
            "cpu": sum(v["cpu"] + v["children_cpu"] for v in values.values()),
            "rss": sum(v["rss"] for v in values.values()), "transient_read_misses": missed}


def cpu_time(kind):
    value = resource.getrusage(kind)
    return value.ru_utime + value.ru_stime


def percentile(values, proportion):
    return sorted(values)[max(0, min(len(values) - 1, int((len(values) - 1) * proportion)))]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, help="Existing release Go binary; never builds or installs")
    parser.add_argument("--duration", type=float, default=600, help="Measured seconds, after warmup (default: 600)")
    parser.add_argument("--workspaces", type=int, default=0, help="Number of isolated workspace fixtures")
    parser.add_argument("--services", type=int, default=0, help="Sleeping services started per fixture workspace")
    parser.add_argument("--interval", type=float, default=2, help="Sampling interval in seconds (default: 2)")
    parser.add_argument("--warmup", type=float, default=10, help="Warmup seconds excluded from CPU interval")
    parser.add_argument("--native-pid", type=int, help="Observe an existing idle release app and helpers; never signal it")
    parser.add_argument("--require-native-background", action="store_true",
                        help="macOS only: invalidate and clean the private fixture if a native window appears during sampling")
    parser.add_argument("--observer", type=observer_spec, action="append", default=[], metavar="PID:BIRTH",
                        help="Explicit attributed Apple WebKit helper outside the parent tree; requires native PID; exit invalidates measurement; never signalled")
    parser.add_argument("--output", type=pathlib.Path, help="Write JSON evidence in addition to stdout")
    args = parser.parse_args()
    if args.duration <= 0 or args.interval < .1 or args.warmup < 0 or args.workspaces < 0 or args.services < 0:
        parser.error("duration must be positive, interval >=0.1, and fixture counts/warmup nonnegative")
    if args.services and not args.workspaces:
        parser.error("--services requires at least one --workspaces fixture")
    if args.observer and not args.native_pid:
        parser.error("--observer requires --native-pid and independent helper attribution")
    if args.require_native_background and (not args.native_pid or sys.platform != "darwin"):
        parser.error("--require-native-background requires macOS and --native-pid")
    fixture = Fixture(args.binary)
    report = {"schema_version": 1, "binary": str(fixture.binary), "binary_sha256": hashlib.sha256(fixture.binary.read_bytes()).hexdigest(),
              "platform": platform.platform(), "duration_requested_seconds": args.duration, "warmup_seconds": args.warmup,
              "interval_seconds": args.interval, "workspaces": args.workspaces, "running_services_per_workspace": args.services,
              "scope": "temporary direct engine serve; existing native app only if --native-pid is supplied",
              "limitations": ["CPU sums cumulative own and waited-child kernel counters over the sampled live tree; transient unreaped/vanishing processes can create accounting gaps.",
                              "RSS is sampled resident memory, may double-count shared pages and does not establish a peak between samples.",
                              "No native installation, login/reboot, window-closure or signing/notarization gate is exercised.",
                              "Only the parent tree and explicitly supplied verified Apple WebKit helper roots are sampled; other unattributed/reparented helpers remain unmeasured.",
                              "The fixture overrides engine idle timeout to 24h; production demand launch and 60s idle exit are not measured."]}
    background_guard = None
    try:
        report["version"] = fixture.command("--version").stdout.strip()
        fixture.start_engine()
        roots = {fixture.engine.pid: fixture.engine_identity}
        if args.native_pid:
            if args.native_pid <= 1 or args.native_pid == os.getpid():
                raise RuntimeError("--native-pid must identify the intended existing idle release app")
            native = process_metadata(args.native_pid)
            command = native["executable"]
            if native["uid"] != os.getuid():
                raise RuntimeError("--native-pid must belong to the current user")
            if pathlib.Path(command).name != "stackharbor-desktop":
                raise RuntimeError(f"--native-pid is not stackharbor-desktop: {command}")
            roots[args.native_pid] = usage(args.native_pid)["identity"]
            report["native_executable"] = command
            report["native_sha256"] = native["sha256"]
            report["limitations"].append("Native release identity and idle state are operator-provided; sampler verifies its executable name/PID identity, not signing or background-window state.")
            if args.require_native_background:
                background_guard = NativeBackgroundGuard(args.native_pid)
        report["explicit_observer_roots"] = bind_observers(roots, args.observer)
        if args.observer:
            report["limitations"].append("Explicit helpers are same-user, allowlisted Apple-signed strict roots; any exit or identity change invalidates this measurement. Their relationship to the native app requires independent evidence.")
        report["root_processes"] = [{"pid": pid, "identity": identity} for pid, identity in roots.items()]
        for number in range(args.workspaces):
            root = fixture.workspace(f"workspace{number}", max(1, args.services))
            fixture.request("POST", "/api/v1/workspaces/open", {"root": str(root)})
            if args.services:
                fixture.start_services(root)
        excluded = {item["pid"] for item in fixture.services}
        report["excluded_fixture_processes"] = fixture.services.copy()
        time.sleep(args.warmup)
        sampler_self_start = cpu_time(resource.RUSAGE_SELF)
        sampler_children_start = cpu_time(resource.RUSAGE_CHILDREN)
        first = sample(roots, excluded)
        if background_guard:
            background_guard.check()
        samples = [first]
        end = first["at"] + args.duration
        while samples[-1]["at"] < end:
            time.sleep(min(args.interval, max(0, end - time.monotonic())))
            if not fixture.alive():
                raise RuntimeError("Fixture service identity changed during measurement")
            samples.append(sample(roots, excluded))
            if background_guard:
                background_guard.check()
        elapsed = samples[-1]["at"] - first["at"]
        sampler_self = cpu_time(resource.RUSAGE_SELF) - sampler_self_start
        sampler_children = cpu_time(resource.RUSAGE_CHILDREN) - sampler_children_start
        cpu = samples[-1]["cpu"] - first["cpu"]
        # A negative aggregate delta indicates unaccounted/reparented exits rather
        # than negative CPU usage; refuse a passing measurement.
        aggregate_valid = cpu >= 0 and all(b["cpu"] >= a["cpu"] for a, b in zip(samples, samples[1:]))
        own = {}
        for item in samples:
            for pid, value in item["processes"].items():
                key = (pid, value["identity"])
                entry = own.setdefault(key, {"pid": pid, "identity": value["identity"], "own_cpu_first_seconds": value["cpu"],
                                             "own_cpu_last_seconds": value["cpu"], "waited_children_cpu_first_seconds": value["children_cpu"],
                                             "waited_children_cpu_last_seconds": value["children_cpu"], "rss_max_bytes": 0, "samples_seen": 0})
                entry.update(own_cpu_last_seconds=value["cpu"], waited_children_cpu_last_seconds=value["children_cpu"],
                             rss_max_bytes=max(entry["rss_max_bytes"], value["rss"]), samples_seen=entry["samples_seen"] + 1)
        rss = [item["rss"] for item in samples]
        conservative_cpu = 100 * (cpu + sampler_self + sampler_children) / elapsed
        measured_cpu = 100 * cpu / elapsed
        misses = sum(len(item["transient_read_misses"]) for item in samples)
        acceptance_scope = bool(args.native_pid) and args.duration >= 600 and args.services == 0 and aggregate_valid and misses == 0
        report.update(result="measured", duration_seconds=elapsed, sample_count=len(samples),
                      engine_native_and_observer_cpu_seconds=cpu, engine_native_and_observer_cpu_percent_one_core=measured_cpu,
                      sampler_self_cpu_seconds=sampler_self, sampler_children_cpu_seconds=sampler_children,
                      cpu_percent_one_core_including_sampler=conservative_cpu,
                      rss_bytes={"mean": statistics.fmean(rss), "p95": percentile(rss, .95), "max_sampled": max(rss)},
                      per_pid=list(own.values()), transient_read_misses=misses, aggregate_cpu_accounting_monotonic=aggregate_valid,
                      targets={"cpu_percent_one_core_max": 1, "no_service_engine_native_rss_bytes_max": 150 * 1024 * 1024,
                               "required_duration_seconds": 600, "candidate_acceptance_scope_present": acceptance_scope, "full_acceptance_scope_present": False,
                               "cpu_with_sampler_within_target": conservative_cpu <= 1 if acceptance_scope else None,
                               "sampled_rss_within_target": max(rss) <= 150 * 1024 * 1024 if acceptance_scope else None,
                               "release_acceptance": "unverified; native release identity/background state and transient helper accounting require independent confirmation"})
        if not args.native_pid:
            report["limitations"].append("Engine-only measurement: combined native+engine 150 MiB/1% acceptance is unverified.")
        if args.duration < 600:
            report["limitations"].append("Duration is shorter than the required 10 minute acceptance window.")
    except Exception as error:
        report.update(result="failed", error=str(error))
    finally:
        if background_guard:
            report["native_background_guard"] = background_guard.report()
        report["cleanup_issues"] = fixture.cleanup()
    encoded = json.dumps(report, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded)
    print(encoded, end="")
    return 0 if report["result"] == "measured" and not report["cleanup_issues"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
