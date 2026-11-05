#!/usr/bin/env python3
"""Check startup flags and optional Linux native-entry lifecycle.

Every server uses a fresh Chinese temporary data directory. xdg-open is replaced
in PATH by a URL logger, so this script never opens the user's browser. The
normal run covers headless/browser-only flags and saved desktop_default=0.
--native additionally requires Linux /proc, DISPLAY, Python3/PyGObject/GTK3,
xwininfo and xprop. It observes real GTK windows and child-process ownership.

The helper-exit case terminates only a verified helper belonging to this test;
it simulates an unexpected entry exit, not a normal close-button interaction.
No synthetic mouse events or helper-private IPC are used. This is not Windows
interaction acceptance, screen-edge/chevron hit testing, or LAN verification.
"""
import argparse
import glob
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def wait_for(check, description, seconds=15):
    deadline = time.monotonic() + seconds
    last = None
    while time.monotonic() < deadline:
        try:
            last = check()
            if last:
                return last
        except (OSError, ValueError, KeyError, urllib.error.URLError) as exc:
            last = str(exc)
        time.sleep(.05)
    raise AssertionError(f"Timed out: {description}; last={last!r}")


def direct_children(pid):
    children = set()
    for filename in glob.glob(f"/proc/{pid}/task/*/children"):
        try:
            children.update(int(value) for value in Path(filename).read_text().split())
        except OSError:
            continue
    return children


def descendants(pid):
    found, pending = set(), [pid]
    while pending:
        for child in direct_children(pending.pop()):
            if child not in found:
                found.add(child)
                pending.append(child)
    return found


def proc_arguments(pid):
    try:
        return Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")
    except OSError:
        return []


def proc_identity(pid):
    """PID/starttime avoids treating a reused unrelated PID as our helper."""
    try:
        raw = Path(f"/proc/{pid}/stat").read_text()
        fields = raw[raw.rfind(")") + 2:].split()
        if fields[0] == "Z":
            return None
        return pid, fields[19]
    except (OSError, ValueError, IndexError):
        return None


def is_native_helper(pid):
    args = proc_arguments(pid)
    return bool(args and "python" in Path(os.fsdecode(args[0])).name.lower()
                and any(b"READY GTK3-X11" in part and b"import gi" in part for part in args))


def helpers(pid):
    return sorted(child for child in descendants(pid) if is_native_helper(child)
                  and proc_identity(child) is not None)


def windows_for(pid):
    tree = subprocess.run(["xwininfo", "-root", "-tree"], capture_output=True,
                          text=True, timeout=5)
    assert tree.returncode == 0, (tree.stdout, tree.stderr)
    windows = []
    for line in tree.stdout.splitlines():
        if "ProjNote" not in line and "projnote" not in line:
            continue
        match = re.search(r"\b(0x[0-9a-fA-F]+)\s+", line)
        if not match:
            continue
        wid = match[1]
        prop = subprocess.run(["xprop", "-id", wid, "_NET_WM_PID", "WM_CLASS"],
                              capture_output=True, text=True, timeout=5)
        owner = re.search(r"_NET_WM_PID[^=]*=\s*(\d+)", prop.stdout)
        if not owner or int(owner[1]) != pid:
            continue
        role = ("entry" if re.search(r'"(?:projnote-compact|ProjNoteCompact)"', prop.stdout)
                else "panel" if re.search(r'"(?:projnote-native-note|ProjNoteNativeNote)"', prop.stdout)
                else "other")
        info = subprocess.run(["xwininfo", "-id", wid], capture_output=True,
                              text=True, timeout=5)
        if info.returncode:
            continue  # Window may be unmapped/destroyed between the two queries.
        windows.append({"id": wid, "role": role, "description": line.strip(),
                        "viewable": "Map State: IsViewable" in info.stdout})
    return windows


def api(base, method, path, data=None):
    payload = json.dumps(data, ensure_ascii=False).encode() if data is not None else None
    request = urllib.request.Request(
        base + path, payload, method=method,
        headers={"Content-Type": "application/json", "X-ProjNote-Request": "1",
                 "Origin": base},
    )
    with OPENER.open(request, timeout=10) as response:
        assert response.status in (200, 201), (method, path, response.status)
        return json.load(response)["data"]


class StartupRun:
    def __init__(self, harness, name, flags=(), data=None):
        self.harness, self.flags = harness, tuple(flags)
        self.root = data or harness.root / name
        self.root.mkdir(exist_ok=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            self.port = sock.getsockname()[1]
        assert 1024 <= self.port <= 65436, self.port
        self.log_path = harness.root / f"服务-{len(harness.runs)}.log"
        self.log = self.log_path.open("w+", encoding="utf-8")
        self.args = [harness.binary, "--data-dir", str(self.root), "--port", str(self.port), "--foreground", *flags]
        self.process = subprocess.Popen(self.args, stdout=self.log, stderr=subprocess.STDOUT,
                                        env=harness.env)
        self.runtime = None
        harness.runs.append(self)
        wait_for(self.ready, "service ready: " + name, seconds=25)
        assert self.health["version"].startswith(harness.expected_version), self.health
        self.api("PUT", "/api/settings", {"backup_auto": "0"})

    @property
    def url(self):
        return self.runtime["url"]

    def ready(self):
        if self.process.poll() is not None:
            self.log.flush()
            raise AssertionError(("service exited", self.process.returncode,
                                  self.log_path.read_text()))
        runtime_path = self.root / "runtime.json"
        if not runtime_path.exists():
            return None
        runtime = json.loads(runtime_path.read_text())
        health = api(runtime["url"], "GET", "/api/health")
        if runtime.get("instance") != health.get("instance"):
            return None
        assert runtime["pid"] == self.process.pid, runtime
        self.runtime, self.health = runtime, health
        return runtime

    def api(self, method, path, data=None):
        return api(self.url, method, path, data)

    def repeat(self, *flags):
        before = self.runtime.copy()
        result = subprocess.run(
            [self.harness.binary, "--data-dir", str(self.root), "--port", str(self.port), "--foreground", *flags],
            capture_output=True, text=True, env=self.harness.env, timeout=20,
        )
        assert result.returncode == 0, (result.returncode, result.stdout, result.stderr)
        assert "已在运行" in result.stdout, result.stdout
        assert json.loads((self.root / "runtime.json").read_text()) == before
        assert self.process.poll() is None
        return result

    def stop(self):
        self.harness.remember_helpers(self)
        if self.process.poll() is None:
            stopped = subprocess.run(
                [self.harness.binary, "--data-dir", str(self.root), "--stop"],
                capture_output=True, text=True, env=self.harness.env, timeout=25,
            )
            assert stopped.returncode == 0, (stopped.stdout, stopped.stderr)
            self.process.wait(timeout=25)
            assert self.process.returncode == 0, self.log_path.read_text()
        self.log.close()


class Harness:
    def __init__(self, binary, root, native, expected_version):
        self.binary, self.root, self.native = binary, root, native
        self.expected_version = expected_version
        self.runs, self.helper_identities = [], set()
        self.browser_log = root / "浏览器URL.log"
        stubdir = root / "浏览器 替身"
        stubdir.mkdir()
        stub = stubdir / "xdg-open"
        stub.write_text('#!/bin/sh\nprintf "%s\\n" "$1" >> "$PROJNOTE_QA_BROWSER_LOG"\nexit 0\n')
        stub.chmod(0o700)
        self.env = os.environ.copy()
        self.env["PATH"] = str(stubdir) + os.pathsep + self.env.get("PATH", "")
        self.env["PROJNOTE_QA_BROWSER_LOG"] = str(self.browser_log)
        self.env.pop("PROJNOTE_TEST_COMPACT_NATIVE", None)
        self.report = {"binary": binary, "native": native, "scenarios": [], "limitations": [
            "Linux startup/process/API checks; not Windows interaction acceptance",
            "Native helper termination simulates unexpected entry exit, not normal close-button use",
            "No synthetic pointer/chevron/drag hit testing or network-sharing coverage",
        ]}

    def urls(self):
        return self.browser_log.read_text().splitlines() if self.browser_log.exists() else []

    def remember_helpers(self, run):
        for pid in helpers(run.process.pid):
            identity = proc_identity(pid)
            if identity:
                self.helper_identities.add(identity)

    def assert_quiet(self, run, before, seconds=.4):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            actual = helpers(run.process.pid)
            self.remember_helpers(run)
            assert actual == [], ("unexpected native helper", actual, run.args)
            assert self.urls() == before, ("unexpected browser URL", self.urls(), before)
            assert run.process.poll() is None
            time.sleep(.04)

    def pass_case(self, name, **evidence):
        self.report["scenarios"].append({"scenario": name, **evidence})
        print("PASS " + name, flush=True)

    def cleanup(self):
        errors = []
        for run in reversed(self.runs):
            try:
                run.stop()
            except Exception as exc:
                errors.append(str(exc))
                if run.process.poll() is None:
                    run.process.terminate()
                    try:
                        run.process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        run.process.kill()
                        run.process.wait(timeout=5)
                run.log.close()
        # Only helpers with a recorded PID/starttime and embedded GTK marker are
        # eligible for fallback cleanup; never terminate an unrelated process.
        for identity in self.helper_identities:
            pid, _ = identity
            if proc_identity(pid) == identity and is_native_helper(pid):
                os.kill(pid, signal.SIGTERM)
        wait_for(lambda: all(proc_identity(pid) != identity for identity in self.helper_identities
                             for pid in [identity[0]]), "all test native helpers exited", seconds=8)
        assert all(run.process.poll() is not None for run in self.runs)
        self.report["cleanup"] = {"service_pids": [run.process.pid for run in self.runs],
                                  "helper_pids": sorted(pid for pid, _ in self.helper_identities),
                                  "all_exited": True}
        assert not errors, errors


def flag_cases(h):
    for flags in [("--compact", "--browser"), ("--compact", "--no-desktop"),
                  ("--browser", "--no-browser")]:
        data = h.root / ("冲突参数 " + "_".join(flags))
        result = subprocess.run([h.binary, "--data-dir", str(data), *flags],
                                capture_output=True, text=True, env=h.env, timeout=10)
        assert result.returncode != 0, (flags, result.stdout, result.stderr)
        assert not (data / "runtime.json").exists()
        assert h.urls() == []
    h.pass_case("conflicting startup flags fail before service/browser launch")

    for flag in ["--no-browser", "--no-desktop"]:
        before = h.urls()
        run = StartupRun(h, "单次参数 中文 " + flag, [flag])
        h.assert_quiet(run, before)
        assert run.api("GET", "/api/settings")["desktop_default"] == "1"
        run.repeat(flag)
        h.assert_quiet(run, before)
        h.pass_case(flag + " is service-only, repeat remains quiet, preference unchanged",
                    service_pid=run.process.pid)
        run.stop()

    before = h.urls()
    browser = StartupRun(h, "显式浏览器 中文", ["--browser", "--no-desktop"])
    wait_for(lambda: h.urls() == before + [browser.url], "exactly one explicit browser URL")
    h.assert_quiet(browser, before + [browser.url])
    h.pass_case("--browser --no-desktop opens exactly one stub URL and no native helper",
                browser_url=browser.url)
    browser.stop()

    seed = StartupRun(h, "关闭默认入口 中文", ["--no-browser"])
    seed.api("PUT", "/api/settings", {"desktop_default": "0"})
    saved_root = seed.root
    seed.stop()
    before = h.urls()
    disabled = StartupRun(h, "已保存0后普通启动", data=saved_root)
    assert disabled.api("GET", "/api/settings")["desktop_default"] == "0"
    h.assert_quiet(disabled, before)
    disabled.repeat()
    h.assert_quiet(disabled, before)
    disabled.repeat("--browser")
    wait_for(lambda: h.urls() == before + [disabled.url], "explicit browser overrides saved desktop=0")
    h.assert_quiet(disabled, before + [disabled.url])
    h.pass_case("desktop_default=0 survives restart/repeat; explicit --browser still opens once",
                service_pid=disabled.process.pid, browser_url=disabled.url)
    disabled.stop()


def native_cases(h):
    before = h.urls()
    run = StartupRun(h, "默认便签入口 中文")
    found = wait_for(lambda: helpers(run.process.pid), "default native helper")
    assert len(found) == 1, found
    helper = found[0]
    h.remember_helpers(run)

    def observed(panel_visible):
        assert helpers(run.process.pid) == [helper], helpers(run.process.pid)
        current = windows_for(helper)
        entry = any(w["role"] == "entry" and w["viewable"] for w in current)
        panel = any(w["role"] == "panel" and w["viewable"] for w in current)
        return current if entry == (not panel_visible) and panel == panel_visible else None

    windows = wait_for(lambda: observed(False), "default entry visible / panel hidden")
    assert h.urls() == before
    run.repeat()
    windows = wait_for(lambda: observed(False), "ordinary second launch leaves panel hidden")
    assert h.urls() == before
    h.pass_case("default startup and ordinary repeat reuse one service/helper, entry only, no browser",
                service_pid=run.process.pid, helper_pid=helper, windows=windows)

    # External service draft changes while the editor remains initialized. An
    # entry-only request must not load/overwrite this draft or flush stale text.
    project = run.api("POST", "/api/projects", {"name": "入口复用验收项目"})
    existing = run.api("GET", "/api/desktop/draft")
    stored = run.api("PUT", "/api/desktop/draft", {
        "version": existing["version"], "content": "二启与entry请求不得覆盖的服务端草稿",
        "project_id": project["id"], "mode": "task",
    })
    run.api("POST", "/api/desktop/entry", {})
    wait_for(lambda: observed(False), "entry-only hook leaves hidden panel hidden")
    time.sleep(1.2)
    assert run.api("GET", "/api/desktop/draft") == stored
    run.api("POST", "/api/desktop/compact", {})
    wait_for(lambda: observed(True), "explicit compact hook expands panel")
    assert h.urls() == before
    run.api("POST", "/api/desktop/entry", {})
    wait_for(lambda: observed(True), "entry-only hook preserves an already open panel")
    time.sleep(1.2)
    assert run.api("GET", "/api/desktop/draft") == stored
    assert helpers(run.process.pid) == [helper]
    h.pass_case("entry-only/compact hooks preserve helper and server draft; only compact reveals panel",
                draft_version=stored["version"], helper_pid=helper)

    identity = proc_identity(helper)
    assert identity in h.helper_identities and helper in descendants(run.process.pid)
    assert is_native_helper(helper), "refuse to terminate an unverified child"
    os.kill(helper, signal.SIGTERM)
    wait_for(lambda: proc_identity(helper) != identity, "test helper terminated")
    run.repeat()
    recreated = wait_for(lambda: helpers(run.process.pid), "ordinary launch recreates exited helper")
    assert len(recreated) == 1 and recreated[0] != helper, recreated
    helper = recreated[0]
    h.remember_helpers(run)
    wait_for(lambda: observed(False), "recreated entry visible / panel hidden")
    assert h.urls() == before
    assert run.api("GET", "/api/desktop/draft") == stored
    h.pass_case("ordinary second launch recreates an unexpectedly exited test helper without browser/panel",
                previous_helper_pid=identity[0], recreated_helper_pid=helper,
                exit_simulation="SIGTERM to verified test child; not normal close-button use")
    run.repeat("--compact")
    wait_for(lambda: observed(True), "explicit --compact on existing instance reveals native panel")
    assert h.urls() == before
    h.pass_case("explicit --compact reuses the existing helper and reveals native panel")
    run.stop()

    explicit = StartupRun(h, "显式原生展开 中文", ["--compact"])
    active = wait_for(lambda: helpers(explicit.process.pid), "explicit compact helper")
    assert len(active) == 1, active
    h.remember_helpers(explicit)
    wait_for(lambda: any(w["role"] == "panel" and w["viewable"] for w in windows_for(active[0])),
             "fresh --compact native panel visible")
    assert h.urls() == before
    h.pass_case("fresh --compact expands native panel without browser", helper_pid=active[0])
    explicit.stop()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", nargs="?", help="path to newly compiled ProjNote")
    parser.add_argument("--binary", dest="binary_option", help="alternative explicit binary path")
    parser.add_argument("--expected-version", default="0.4.0", help="expected binary version prefix")
    parser.add_argument("--native", action="store_true", help="observe actual Linux GTK helper/windows")
    args = parser.parse_args()
    if args.binary and args.binary_option and Path(args.binary).resolve() != Path(args.binary_option).resolve():
        parser.error("positional binary and --binary disagree")
    binary = args.binary_option or args.binary
    if not binary:
        parser.error("provide binary or --binary")
    if sys.platform != "linux" or not Path("/proc/self/stat").exists():
        parser.error("this startup/process script is Linux-only; it does not verify Windows UI")
    binary = str(Path(binary).resolve(strict=True))
    if args.native:
        if not os.environ.get("DISPLAY"):
            parser.error("--native requires an accessible DISPLAY (X11/XWayland)")
        for tool in ["python3", "xwininfo", "xprop"]:
            if not shutil.which(tool):
                parser.error("--native requires " + tool)
        check = subprocess.run(["python3", "-c", "import gi; gi.require_version('Gtk','3.0'); from gi.repository import Gtk; assert Gtk.init_check()[0]"],
                               capture_output=True, text=True, timeout=10,
                               env={**os.environ, "GDK_BACKEND": "x11"})
        if check.returncode:
            parser.error("--native cannot initialize GTK3: " + check.stderr.strip())
    with tempfile.TemporaryDirectory(prefix="projnote-v034-") as temp:
        harness = Harness(binary, Path(temp), args.native, args.expected_version)
        try:
            flag_cases(harness)
            if args.native:
                native_cases(harness)
            else:
                harness.report["skipped"] = ["default/native window lifecycle; rerun with --native"]
        finally:
            harness.cleanup()
        harness.report["status"] = "passed"
        harness.report["browser_stub_urls"] = harness.urls()
        print(json.dumps(harness.report, ensure_ascii=False, indent=2))
        print("PASS cleanup: all temporary services/helpers exited; data removed on script exit", flush=True)


if __name__ == "__main__":
    main()
