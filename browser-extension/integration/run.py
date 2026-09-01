#!/usr/bin/env python3
"""Phase 3C browser integration tests using only the Python standard library.

The harness launches browsers with disposable profiles, serves loopback-only
test pages, drives tabs through CDP/WebDriver BiDi, and verifies persisted
ActivityWatch events plus WorkChronicle status output.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import http.server
import json
import os
import re
import secrets
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import wave
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Callable


INGEST_HEALTH = "http://127.0.0.1:5601/healthz"
OBSERVATION_PATH = "/api/v1/browser/observations"
HEARTBEAT_SECONDS = 5


def utc_now() -> datetime:
    return datetime.now(timezone.utc)


def iso(value: datetime) -> str:
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def json_request(url: str, method: str = "GET", body: Any = None, timeout: float = 5) -> Any:
    data = None if body is None else json.dumps(body).encode("utf-8")
    headers = {"Content-Type": "application/json"} if data is not None else {}
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(request, timeout=timeout) as response:
        payload = response.read()
    if not payload:
        return None
    try:
        return json.loads(payload)
    except json.JSONDecodeError:
        return payload.decode("utf-8", errors="replace")


def wait_until(action: Callable[[], Any], timeout: float, description: str, interval: float = 0.25) -> Any:
    deadline = time.monotonic() + timeout
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            result = action()
            if result:
                return result
        except Exception as error:  # transient startup/query failures are expected
            last_error = error
        time.sleep(interval)
    suffix = f"; last error: {last_error}" if last_error else ""
    raise AssertionError(f"timed out waiting for {description}{suffix}")


def make_wave(duration_seconds: int = 20) -> bytes:
    import io
    import math

    output = io.BytesIO()
    sample_rate = 8000
    with wave.open(output, "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(1)
        wav.setframerate(sample_rate)
        frames = bytearray()
        for index in range(sample_rate * duration_seconds):
            sample = 128 + int(24 * math.sin(2 * math.pi * 440 * index / sample_rate))
            frames.append(sample)
        wav.writeframes(bytes(frames))
    return output.getvalue()


PAGE = """<!doctype html>
<html><head><meta charset="utf-8"><title>{title}</title></head>
<body><h1>WorkChronicle Phase 3C test</h1>
<script>
(() => {{
  let media = null;
  function snapshot() {{
    return media ? {{tag: media.tagName.toLowerCase(), paused: media.paused,
      ended: media.ended, currentTime: media.currentTime, duration: media.duration}} : null;
  }}
  async function add(kind, playNow) {{
    if (media) media.remove();
    media = document.createElement(kind);
    media.id = 'test-media';
    media.preload = 'auto';
    media.muted = kind === 'video';
    media.src = '/tone.wav?run={run}';
    document.body.appendChild(media);
    await new Promise((resolve, reject) => {{
      if (media.readyState >= 1) return resolve();
      media.addEventListener('loadedmetadata', resolve, {{once: true}});
      media.addEventListener('error', () => reject(new Error('media load failed')), {{once: true}});
    }});
    if (playNow) await media.play();
    return snapshot();
  }}
  window.wtTest = {{
    addVideo: (playNow) => add('video', playNow),
    addAudio: (playNow) => add('audio', playNow),
    play: async () => {{ await media.play(); return snapshot(); }},
    pause: () => {{ media.pause(); return snapshot(); }},
    end: async () => {{
      media.currentTime = Math.max(0, media.duration - 0.2);
      await media.play();
      await new Promise(resolve => media.addEventListener('ended', resolve, {{once: true}}));
      return snapshot();
    }},
    remove: () => {{ if (media) media.remove(); media = null; return null; }},
    snapshot
  }};
}})();
</script></body></html>"""


class TestPageHandler(http.server.BaseHTTPRequestHandler):
    wave_data = make_wave()

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        if parsed.path == "/tone.wav":
            self.send_response(200)
            self.send_header("Content-Type", "audio/wav")
            self.send_header("Content-Length", str(len(self.wave_data)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(self.wave_data)
            return
        if parsed.path in ("/scenario.html", "/empty.html"):
            run = query.get("run", ["missing"])[0]
            browser = query.get("browser", ["unknown"])[0]
            suffix = "empty" if parsed.path == "/empty.html" else "scenario"
            title = f"WT3C-{run}-{browser}-{suffix}"
            page = PAGE.format(title=title, run=run) if suffix == "scenario" else f"<!doctype html><title>{title}</title><p>no media</p>"
            encoded = page.encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(encoded)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(encoded)
            return
        self.send_error(404)

    def log_message(self, _format: str, *_args: Any) -> None:
        return


class LocalTestServer:
    def __init__(self) -> None:
        class QuietServer(http.server.ThreadingHTTPServer):
            def handle_error(self, _request: Any, _client_address: Any) -> None:
                return
        self.server = QuietServer(("127.0.0.1", 0), TestPageHandler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    @property
    def base_url(self) -> str:
        host, port = self.server.server_address
        return f"http://{host}:{port}"

    def __enter__(self) -> "LocalTestServer":
        self.thread.start()
        return self

    def __exit__(self, *_args: Any) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)


class WebSocket:
    def __init__(self, url: str, origin: str = "http://127.0.0.1") -> None:
        parsed = urllib.parse.urlparse(url)
        self.socket = socket.create_connection((parsed.hostname or "127.0.0.1", parsed.port or 80), timeout=10)
        self.socket.settimeout(15)
        key = base64.b64encode(os.urandom(16)).decode("ascii")
        path = parsed.path or "/"
        if parsed.query:
            path += "?" + parsed.query
        request = (
            f"GET {path} HTTP/1.1\r\nHost: {parsed.hostname}:{parsed.port}\r\n"
            f"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\n"
            f"Sec-WebSocket-Version: 13\r\nOrigin: {origin}\r\n\r\n"
        )
        self.socket.sendall(request.encode("ascii"))
        response = self._read_headers()
        if b" 101 " not in response.split(b"\r\n", 1)[0]:
            raise RuntimeError(f"WebSocket handshake failed: {response[:300]!r}")
        expected = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode("ascii")).digest())
        if expected.lower() not in response.lower():
            raise RuntimeError("WebSocket handshake returned an invalid accept key")
        self.next_id = 1
        self.events: list[dict[str, Any]] = []

    def _read_headers(self) -> bytes:
        data = bytearray()
        while b"\r\n\r\n" not in data:
            chunk = self.socket.recv(4096)
            if not chunk:
                raise RuntimeError("WebSocket closed during handshake")
            data.extend(chunk)
        return bytes(data)

    def _read_exact(self, size: int) -> bytes:
        data = bytearray()
        while len(data) < size:
            chunk = self.socket.recv(size - len(data))
            if not chunk:
                raise RuntimeError("WebSocket closed")
            data.extend(chunk)
        return bytes(data)

    def send_json(self, payload: dict[str, Any]) -> None:
        data = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        mask = os.urandom(4)
        length = len(data)
        header = bytearray([0x81])
        if length < 126:
            header.append(0x80 | length)
        elif length < 65536:
            header.append(0x80 | 126)
            header.extend(struct.pack("!H", length))
        else:
            header.append(0x80 | 127)
            header.extend(struct.pack("!Q", length))
        header.extend(mask)
        header.extend(bytes(value ^ mask[index % 4] for index, value in enumerate(data)))
        self.socket.sendall(header)

    def receive_json(self) -> dict[str, Any]:
        while True:
            first, second = self._read_exact(2)
            opcode = first & 0x0F
            length = second & 0x7F
            if length == 126:
                length = struct.unpack("!H", self._read_exact(2))[0]
            elif length == 127:
                length = struct.unpack("!Q", self._read_exact(8))[0]
            masked = bool(second & 0x80)
            mask = self._read_exact(4) if masked else b""
            data = self._read_exact(length)
            if masked:
                data = bytes(value ^ mask[index % 4] for index, value in enumerate(data))
            if opcode == 0x8:
                raise RuntimeError("WebSocket peer closed")
            if opcode == 0x9:
                self._send_control(0xA, data)
                continue
            if opcode == 0x1:
                return json.loads(data.decode("utf-8"))

    def _send_control(self, opcode: int, data: bytes) -> None:
        mask = os.urandom(4)
        frame = bytearray([0x80 | opcode, 0x80 | len(data)])
        frame.extend(mask)
        frame.extend(bytes(value ^ mask[index % 4] for index, value in enumerate(data)))
        self.socket.sendall(frame)

    def command(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        command_id = self.next_id
        self.next_id += 1
        self.send_json({"id": command_id, "method": method, "params": params or {}})
        while True:
            response = self.receive_json()
            if response.get("id") != command_id:
                self.events.append(response)
                continue
            if "error" in response:
                raise RuntimeError(f"{method} failed: {response['error']}")
            return response.get("result", {})

    def close(self) -> None:
        try:
            self._send_control(0x8, b"")
        except OSError:
            pass
        self.socket.close()


class BrowserController:
    identity: str

    def create_tab(self, url: str) -> str:
        raise NotImplementedError

    def evaluate(self, tab: str, expression: str) -> Any:
        raise NotImplementedError

    def activate(self, tab: str) -> None:
        raise NotImplementedError

    def navigate(self, tab: str, url: str) -> None:
        raise NotImplementedError

    def close_tab(self, tab: str) -> None:
        raise NotImplementedError

    def close(self) -> None:
        raise NotImplementedError


class UnsupportedBrowser(RuntimeError):
    pass


class ChromiumController(BrowserController):
    def __init__(self, identity: str, executable: Path, extension: Path) -> None:
        self.identity = identity
        self.port = free_port()
        self.profile = tempfile.mkdtemp(prefix=f"worktracker-{identity}-")
        self.connections: dict[str, WebSocket] = {}
        self.process: subprocess.Popen[Any] | None = None
        self.log_path = Path(self.profile) / "browser.log"
        args = [
            str(executable), f"--user-data-dir={self.profile}",
            f"--load-extension={extension}", f"--disable-extensions-except={extension}",
            f"--remote-debugging-port={self.port}", "--remote-allow-origins=*",
            "--autoplay-policy=no-user-gesture-required", "--no-first-run",
            "--no-default-browser-check", "--disable-background-networking",
            "--disable-component-update", "--disable-sync", "--window-position=-32000,-32000",
            "--window-size=800,600", "about:blank",
        ]
        flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
        try:
            with self.log_path.open("wb") as log:
                self.process = subprocess.Popen(args, stdout=log, stderr=log, creationflags=flags)
            wait_until(lambda: json_request(f"http://127.0.0.1:{self.port}/json/version"), 15, f"{identity} DevTools")
            try:
                wait_until(self._extension_target, 5, f"{identity} unpacked extension", interval=0.5)
            except AssertionError as error:
                raise UnsupportedBrowser(f"{identity} did not activate the command-line unpacked extension") from error
        except UnsupportedBrowser:
            self.close()
            raise
        except Exception as error:
            diagnostic = self._diagnostic()
            self.close()
            raise RuntimeError(f"{error}; browser log: {diagnostic}") from error

    def _diagnostic(self) -> str:
        try:
            return self.log_path.read_text(encoding="utf-8", errors="replace")[-2000:].replace("\n", " ")
        except OSError:
            return "unavailable"

    def _extension_target(self) -> bool:
        targets = json_request(f"http://127.0.0.1:{self.port}/json/list")
        return any(str(target.get("url", "")).startswith("chrome-extension://") and str(target.get("url", "")).endswith("/src/background.js") for target in targets)

    def create_tab(self, url: str) -> str:
        target = json_request(f"http://127.0.0.1:{self.port}/json/new?{urllib.parse.quote(url, safe='')}", method="PUT")
        tab = str(target["id"])
        self.connections[tab] = WebSocket(target["webSocketDebuggerUrl"])
        self.connections[tab].command("Runtime.enable")
        wait_until(lambda: self.evaluate(tab, "document.readyState") in ("interactive", "complete"), 15, "page load")
        return tab

    def evaluate(self, tab: str, expression: str) -> Any:
        result = self.connections[tab].command("Runtime.evaluate", {"expression": expression, "awaitPromise": True, "returnByValue": True})
        if result.get("exceptionDetails"):
            raise RuntimeError(f"browser JavaScript failed: {result['exceptionDetails']}")
        return result.get("result", {}).get("value")

    def activate(self, tab: str) -> None:
        json_request(f"http://127.0.0.1:{self.port}/json/activate/{tab}")

    def navigate(self, tab: str, url: str) -> None:
        self.connections[tab].command("Page.navigate", {"url": url})
        wait_until(lambda: self.evaluate(tab, "document.readyState") == "complete", 15, "navigation")

    def close_tab(self, tab: str) -> None:
        json_request(f"http://127.0.0.1:{self.port}/json/close/{tab}")
        connection = self.connections.pop(tab, None)
        if connection:
            connection.close()

    def close(self) -> None:
        for connection in list(self.connections.values()):
            connection.close()
        self.connections.clear()
        if self.process is not None and self.process.poll() is None:
            subprocess.run(["taskkill", "/PID", str(self.process.pid), "/T", "/F"], capture_output=True)
            try:
                self.process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.process.kill()
        for _ in range(5):
            shutil.rmtree(self.profile, ignore_errors=True)
            if not Path(self.profile).exists():
                break
            time.sleep(0.25)


class FirefoxController(BrowserController):
    identity = "firefox"

    def __init__(self, executable: Path, extension_archive: Path) -> None:
        self.port = free_port()
        self.profile = tempfile.mkdtemp(prefix="worktracker-firefox-")
        self.xpi = Path(self.profile) / "worktracker-browser-context.xpi"
        shutil.copy2(extension_archive, self.xpi)
        self.process: subprocess.Popen[Any] | None = None
        self.socket: WebSocket | None = None
        self.log_path = Path(self.profile) / "browser.log"
        origin = f"http://127.0.0.1:{self.port}"
        args = [
            str(executable), "-headless", "-no-remote", "--profile", self.profile,
            "--remote-debugging-port", str(self.port), "--remote-allow-hosts", "localhost,127.0.0.1",
            "--remote-allow-origins", origin, "about:blank",
        ]
        flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
        try:
            with self.log_path.open("wb") as log:
                self.process = subprocess.Popen(args, stdout=log, stderr=log, creationflags=flags)
            wait_until(lambda: self._connect(origin), 15, "Firefox WebDriver BiDi")
            assert self.socket is not None
            self.socket.command("session.new", {"capabilities": {"alwaysMatch": {}}})
            self.socket.command("session.subscribe", {"events": ["log.entryAdded"]})
            self.install_result = self.socket.command("webExtension.install", {"extensionData": {"type": "path", "path": str(self.xpi)}})
        except Exception as error:
            diagnostic = self._diagnostic()
            self.close()
            raise RuntimeError(f"{error}; browser log: {diagnostic}") from error

    def _diagnostic(self) -> str:
        try:
            return self.log_path.read_text(encoding="utf-8", errors="replace")[-2000:].replace("\n", " ")
        except OSError:
            return "unavailable"

    def extension_diagnostic(self) -> dict[str, Any]:
        realms: dict[str, Any] = {}
        debugging_text = ""
        background_probe: Any = None
        try:
            assert self.socket is not None
            realms = self.socket.command("script.getRealms")
        except Exception as error:
            realms = {"error": str(error)}
        try:
            context = self.socket.command("browsingContext.create", {"type": "tab"})["context"]
            self.socket.command("browsingContext.navigate", {"context": context, "url": "about:debugging#/runtime/this-firefox", "wait": "complete"})
            time.sleep(2)
            result = self.socket.command("script.evaluate", {
                "expression": "document.body.innerText", "target": {"context": context},
                "awaitPromise": False, "resultOwnership": "none",
            })
            debugging_text = str(result.get("result", {}).get("value", ""))[-4000:]
            self.socket.command("browsingContext.close", {"context": context})
        except Exception as error:
            debugging_text = "unavailable: " + str(error)
        uuid_match = re.search(r"Internal UUID\s+([0-9a-f-]{36})", debugging_text)
        if uuid_match:
            try:
                context = self.socket.command("browsingContext.create", {"type": "tab"})["context"]
                background_url = f"moz-extension://{uuid_match.group(1)}/_generated_background_page.html"
                self.socket.command("browsingContext.navigate", {"context": context, "url": background_url, "wait": "complete"})
                result = self.socket.command("script.evaluate", {
                    "expression": "(async () => { try { const r = await fetch('http://127.0.0.1:5601/healthz'); return {core: typeof globalThis.WorkTrackerBrowserCore, compat: typeof globalThis.WorkTrackerBrowserCompat, status: r.status, body: await r.text()}; } catch (e) { return {core: typeof globalThis.WorkTrackerBrowserCore, compat: typeof globalThis.WorkTrackerBrowserCompat, error: String(e)}; } })()",
                    "target": {"context": context}, "awaitPromise": True, "resultOwnership": "none",
                })
                background_probe = result
                self.socket.command("browsingContext.close", {"context": context})
            except Exception as error:
                background_probe = {"error": str(error)}
        return {
            "install": getattr(self, "install_result", None),
            "realms": realms,
            "log_events": [] if self.socket is None else self.socket.events[-20:],
            "about_debugging": debugging_text,
            "background_probe": background_probe,
            "browser_log": self._diagnostic(),
        }

    def _connect(self, origin: str) -> bool:
        try:
            self.socket = WebSocket(f"ws://127.0.0.1:{self.port}/session", origin=origin)
            return True
        except (OSError, RuntimeError):
            if self.socket is not None:
                try:
                    self.socket.close()
                except Exception:
                    pass
                self.socket = None
            return False

    def create_tab(self, url: str) -> str:
        result = self.socket.command("browsingContext.create", {"type": "tab"})
        tab = str(result["context"])
        self.navigate(tab, url)
        return tab

    def evaluate(self, tab: str, expression: str) -> Any:
        result = self.socket.command("script.evaluate", {
            "expression": expression, "target": {"context": tab},
            "awaitPromise": True, "resultOwnership": "none",
        })
        if result.get("type") != "success":
            raise RuntimeError(f"Firefox JavaScript failed: {result}")
        remote = result.get("result", {})
        return remote.get("value")

    def activate(self, tab: str) -> None:
        self.socket.command("browsingContext.activate", {"context": tab})

    def navigate(self, tab: str, url: str) -> None:
        self.socket.command("browsingContext.navigate", {"context": tab, "url": url, "wait": "complete"})

    def close_tab(self, tab: str) -> None:
        self.socket.command("browsingContext.close", {"context": tab})

    def close(self) -> None:
        try:
            if self.socket is not None:
                self.socket.command("session.end")
        except Exception:
            pass
        try:
            if self.socket is not None:
                self.socket.close()
        except Exception:
            pass
        if self.process is not None and self.process.poll() is None:
            subprocess.run(["taskkill", "/PID", str(self.process.pid), "/T", "/F"], capture_output=True)
            try:
                self.process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.process.kill()
        for _ in range(5):
            shutil.rmtree(self.profile, ignore_errors=True)
            if not Path(self.profile).exists():
                break
            time.sleep(0.25)


@dataclass
class ActivityWatchProbe:
    base_url: str
    bucket: str
    started: datetime

    def events(self) -> list[dict[str, Any]]:
        query = urllib.parse.urlencode({"start": iso(self.started - timedelta(seconds=2)), "end": iso(utc_now() + timedelta(seconds=2)), "limit": -1})
        path = urllib.parse.quote(self.bucket, safe="")
        return json_request(f"{self.base_url.rstrip('/')}/api/0/buckets/{path}/events?{query}")

    def matching(self, run_id: str, browser: str, tab_id: str | None = None) -> list[dict[str, Any]]:
        output = []
        token = f"WT3C-{run_id}-{browser}"
        for event in self.events():
            data = event.get("data", {})
            if data.get("browser") != browser:
                continue
            if tab_id is not None and data.get("tab_id") != str(tab_id):
                continue
            if token not in str(data.get("title", "")) and run_id not in str(data.get("url", "")):
                continue
            output.append(event)
        output.sort(key=lambda event: event.get("timestamp", ""))
        return output

    def wait(self, run_id: str, browser: str, predicate: Callable[[dict[str, Any]], bool], description: str, tab_id: str | None = None, timeout: float = 12, since: datetime | None = None) -> dict[str, Any]:
        def find() -> dict[str, Any] | None:
            for event in reversed(self.matching(run_id, browser, tab_id)):
                if since is not None and datetime.fromisoformat(event["timestamp"].replace("Z", "+00:00")) < since:
                    continue
                if predicate(event.get("data", {})):
                    return event
            return None
        return wait_until(find, timeout, f"{browser} {description}")


@dataclass
class TestResults:
    passed: list[str] = field(default_factory=list)
    failed: list[str] = field(default_factory=list)
    skipped: list[str] = field(default_factory=list)

    def check(self, name: str, action: Callable[[], Any]) -> Any:
        try:
            value = action()
            self.passed.append(name)
            print(f"PASS {name}", flush=True)
            return value
        except Exception as error:
            self.failed.append(f"{name}: {error}")
            print(f"FAIL {name}: {error}", flush=True)
            raise


def media_is(state: str, present: bool = True, media_type: str | None = None, active: bool | None = None, visible: bool | None = None) -> Callable[[dict[str, Any]], bool]:
    def predicate(data: dict[str, Any]) -> bool:
        media = data.get("media", {})
        return (
            media.get("present") is present
            and media.get("state") == state
            and (media_type is None or media.get("type") == media_type)
            and (active is None or data.get("active") is active)
            and (visible is None or data.get("visible") is visible)
        )
    return predicate


def status_json(core_url: str) -> dict[str, Any]:
    return json_request(core_url.rstrip("/") + "/api/v1/status", timeout=30)


def assert_status(core_url: str, source_id: str, state: str, passive: bool, require_vlc: bool = False) -> None:
    status = status_json(core_url)
    evidence = status.get("passive_detector_evidence", {})
    browser = evidence.get(source_id)
    if not browser:
        raise AssertionError(f"status omitted {source_id}; sources were {sorted(evidence)}")
    if browser.get("state") != state or browser.get("passive_work") is not passive:
        raise AssertionError(f"unexpected status evidence for {source_id}: {browser}")
    if require_vlc and "vlc" not in evidence:
        raise AssertionError("status did not retain independent VLC evidence")


def run_browser(controller: BrowserController, server: LocalTestServer, aw: ActivityWatchProbe, run_id: str, core_url: str, results: TestResults, verify_status: bool) -> None:
    browser = controller.identity
    scenario = f"{server.base_url}/scenario.html?run={run_id}&browser={browser}"
    empty = f"{server.base_url}/empty.html?run={run_id}&browser={browser}"
    tab = results.check(f"{browser}: launch extension and test tab", lambda: controller.create_tab(scenario))

    no_media = lambda data: data.get("media", {}).get("present") is False and data.get("active") is True
    first = results.check(f"{browser}: normal active tab without media", lambda: aw.wait(run_id, browser, no_media, "no-media observation"))
    tab_id = str(first["data"]["tab_id"])
    source_id = f"browser:{browser}:{tab_id}"

    stage = utc_now()
    results.check(f"{browser}: dynamic video insertion and playback", lambda: controller.evaluate(tab, "wtTest.addVideo(true)"))
    playing_event = results.check(f"{browser}: video playing event", lambda: aw.wait(run_id, browser, media_is("playing", media_type="video"), "video playing", tab_id, since=stage))
    if verify_status:
        results.check(f"{browser}: Core status playing passive_work=true with VLC coexistence", lambda: assert_status(core_url, source_id, "playing", True, True))

    def heartbeat_check() -> None:
        initial = datetime.fromisoformat(playing_event["timestamp"].replace("Z", "+00:00"))
        def later_heartbeat() -> dict[str, Any] | None:
            for event in aw.matching(run_id, browser, tab_id):
                event_time = datetime.fromisoformat(event["timestamp"].replace("Z", "+00:00"))
                if event_time >= initial + timedelta(seconds=HEARTBEAT_SECONDS - 1) and event.get("data", {}).get("media", {}).get("state") == "playing":
                    return event
            return None
        wait_until(later_heartbeat, HEARTBEAT_SECONDS + 6, f"{browser} second playing heartbeat")
    results.check(f"{browser}: five-second heartbeat", heartbeat_check)

    stage = utc_now()
    results.check(f"{browser}: pause media", lambda: controller.evaluate(tab, "wtTest.pause()"))
    results.check(f"{browser}: video paused event", lambda: aw.wait(run_id, browser, media_is("paused", media_type="video"), "video paused", tab_id, since=stage))
    if verify_status:
        results.check(f"{browser}: Core status paused passive_work=false", lambda: assert_status(core_url, source_id, "paused", False, True))

    stage = utc_now()
    results.check(f"{browser}: resume and naturally end media", lambda: controller.evaluate(tab, "wtTest.end()"))
    results.check(f"{browser}: video ended/stopped event", lambda: aw.wait(run_id, browser, media_is("stopped", media_type="video"), "video stopped", tab_id, since=stage))
    if verify_status:
        results.check(f"{browser}: Core status stopped passive_work=false", lambda: assert_status(core_url, source_id, "stopped", False, True))

    stage = utc_now()
    results.check(f"{browser}: remove dynamic media", lambda: controller.evaluate(tab, "wtTest.remove()"))
    results.check(f"{browser}: media removal clearing event", lambda: aw.wait(run_id, browser, media_is("none", present=False), "media removed", tab_id, since=stage))

    stage = utc_now()
    results.check(f"{browser}: dynamic audio insertion", lambda: controller.evaluate(tab, "wtTest.addAudio(true)"))
    results.check(f"{browser}: audio playing event", lambda: aw.wait(run_id, browser, media_is("playing", media_type="audio"), "audio playing", tab_id, since=stage))
    stage = utc_now()
    controller.evaluate(tab, "wtTest.remove(); wtTest.addVideo(true)")
    aw.wait(run_id, browser, media_is("playing", media_type="video"), "background test setup", tab_id, since=stage)

    other = results.check(f"{browser}: open second tab", lambda: controller.create_tab(empty))
    stage = utc_now()
    results.check(f"{browser}: activate second tab", lambda: controller.activate(other))
    results.check(f"{browser}: background playback preserved", lambda: aw.wait(run_id, browser, media_is("playing", media_type="video", active=False, visible=False), "background hidden playback", tab_id, since=stage))
    results.check(f"{browser}: page visibility change", lambda: aw.wait(run_id, browser, lambda data: data.get("visible") is False and data.get("tab_id") == tab_id, "hidden visibility", tab_id, since=stage))
    stage = utc_now()
    controller.activate(tab)
    results.check(f"{browser}: foreground visibility restored", lambda: aw.wait(run_id, browser, lambda data: data.get("visible") is True and data.get("active") is True, "visible active tab", tab_id, since=stage))
    controller.close_tab(other)

    stage = utc_now()
    controller.navigate(tab, empty)
    results.check(f"{browser}: navigation clears playing state", lambda: aw.wait(run_id, browser, media_is("none", present=False), "navigation clearing", tab_id, since=stage))

    closing_tab = controller.create_tab(scenario + "&closure=1")
    stage = utc_now()
    controller.evaluate(closing_tab, "wtTest.addVideo(true)")
    closure_playing = aw.wait(run_id, browser, lambda data: media_is("playing", media_type="video")(data) and "closure=1" in data.get("url", ""), "closure setup", since=stage)
    closure_id = str(closure_playing["data"]["tab_id"])
    stage = utc_now()
    controller.close_tab(closing_tab)
    results.check(f"{browser}: tab closure clears playing state", lambda: aw.wait(run_id, browser, media_is("none", present=False), "tab closure clearing", closure_id, since=stage))


def ingest_healthy() -> bool:
    try:
        result = json_request(INGEST_HEALTH, timeout=1)
        return result.get("status") == "ok" and result.get("schema_version") == 1
    except Exception:
        return False


def edge_policy_smoke(executable: Path, extension: Path, server: LocalTestServer, aw: ActivityWatchProbe, run_id: str, results: TestResults) -> None:
    profile = tempfile.mkdtemp(prefix="worktracker-edge-")
    url = f"{server.base_url}/empty.html?run={run_id}&browser=edge"
    log_path = Path(profile) / "browser.log"
    process: subprocess.Popen[Any] | None = None
    try:
        args = [
            str(executable), f"--user-data-dir={profile}", f"--load-extension={extension}",
            f"--disable-extensions-except={extension}", "--autoplay-policy=no-user-gesture-required",
            "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
            "--disable-component-update", "--disable-sync", "--window-position=-32000,-32000",
            "--window-size=800,600", url,
        ]
        with log_path.open("wb") as log:
            process = subprocess.Popen(args, stdout=log, stderr=log, creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
        first = results.check("edge: policy-compatible extension launch and identity", lambda: aw.wait(run_id, "edge", lambda data: data.get("media", {}).get("present") is False, "initial no-media observation", timeout=15))
        tab_id = str(first["data"]["tab_id"])
        first_time = datetime.fromisoformat(first["timestamp"].replace("Z", "+00:00"))
        def no_media_heartbeat() -> dict[str, Any] | None:
            for event in aw.matching(run_id, "edge", tab_id):
                event_time = datetime.fromisoformat(event["timestamp"].replace("Z", "+00:00"))
                if event_time >= first_time + timedelta(seconds=4) and event.get("data", {}).get("media", {}).get("present") is False:
                    return event
            return None
        wait_until(no_media_heartbeat, HEARTBEAT_SECONDS + 4, "Edge five-second heartbeat")
        results.passed.append("edge: five-second heartbeat")
        print("PASS edge: five-second heartbeat", flush=True)
        results.skipped.append("edge: media and tab lifecycle control unavailable under corporate DevTools policy")
        print("SKIP edge: media and tab lifecycle control unavailable under corporate DevTools policy", flush=True)
    finally:
        if process is not None and process.poll() is None:
            subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True)
        for _ in range(5):
            shutil.rmtree(profile, ignore_errors=True)
            if not Path(profile).exists():
                break
            time.sleep(0.25)


def restart_agent(agent: Path, config: Path, core_url: str, token_file: Path) -> subprocess.Popen[Any]:
    flags = getattr(subprocess, "DETACHED_PROCESS", 0) | getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0) | getattr(subprocess, "CREATE_NO_WINDOW", 0)
    return subprocess.Popen([str(agent), "--config", str(config), "--core-url", core_url, "--token-file", str(token_file)], cwd=str(agent.parent.parent), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, creationflags=flags, close_fds=True)


def outage_recovery(controller: BrowserController, server: LocalTestServer, aw: ActivityWatchProbe, run_id: str, agent_pid: int, agent: Path, config: Path, core_url: str, token_file: Path, results: TestResults) -> None:
    browser = controller.identity
    url = f"{server.base_url}/scenario.html?run={run_id}&browser={browser}&outage=1"
    tab = controller.create_tab(url)
    stage = utc_now()
    controller.evaluate(tab, "wtTest.addVideo(true)")
    event = aw.wait(run_id, browser, lambda data: media_is("playing", media_type="video")(data) and "outage=1" in data.get("url", ""), "outage setup", since=stage)
    tab_id = str(event["data"]["tab_id"])
    restarted: subprocess.Popen[Any] | None = None
    try:
        os.kill(agent_pid, 15)
        wait_until(lambda: not ingest_healthy(), 8, "ingest endpoint shutdown")
        results.passed.append("ingest: temporary endpoint failure observed")
        print("PASS ingest: temporary endpoint failure observed", flush=True)
        time.sleep(HEARTBEAT_SECONDS + 1)
    finally:
        restarted = restart_agent(agent, config, core_url, token_file)
        wait_until(ingest_healthy, 15, "Host Agent restart and ingest recovery")
    recovery_start = utc_now()
    recovered = aw.wait(
        run_id, browser,
        lambda data: data.get("tab_id") == tab_id and data.get("media", {}).get("state") == "playing"
        and datetime.fromisoformat(data["observed_at"].replace("Z", "+00:00")) >= recovery_start,
        "post-recovery heartbeat", tab_id, timeout=HEARTBEAT_SECONDS + 8, since=recovery_start,
    )
    if not recovered:
        raise AssertionError("playing heartbeat did not recover")
    results.passed.append("ingest: heartbeat recovered after collector restart")
    print("PASS ingest: heartbeat recovered after collector restart", flush=True)
    controller.close_tab(tab)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--agent-pid", required=True, type=int)
    parser.add_argument("--agent", required=True, type=Path)
    parser.add_argument("--token-file", required=True, type=Path)
    parser.add_argument("--core-url", default="http://127.0.0.1:8080")
    parser.add_argument("--browsers", default="chrome,edge,firefox", help="comma-separated browser identities")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    repo = args.repo.resolve()
    config_path = repo / "config.json"
    extension_root = repo / "browser-extension" / "dist"
    config = json.loads(config_path.read_text(encoding="utf-8"))
    hostname = socket.gethostname()
    aw = ActivityWatchProbe(config.get("server", "http://localhost:5600"), f"aw-watcher-browser-context_{hostname}", utc_now())
    run_id = utc_now().strftime("%Y%m%dT%H%M%S") + "-" + secrets.token_hex(3)
    results = TestResults()

    if not ingest_healthy():
        print("FAIL browser ingest was not healthy before testing", file=sys.stderr)
        return 1
    print("PASS browser ingest healthy on 127.0.0.1:5601", flush=True)

    browsers: list[tuple[str, Path]] = [
        ("chrome", Path(r"C:\Program Files\Google\Chrome\Application\chrome.exe")),
        ("edge", Path(r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe")),
        ("firefox", Path(r"C:\Program Files\Mozilla Firefox\firefox.exe")),
    ]
    selected = {value.strip().lower() for value in args.browsers.split(",") if value.strip()}
    browsers = [item for item in browsers if item[0] in selected]
    controllers: list[BrowserController] = []
    collector_restarted = False
    try:
        with LocalTestServer() as server:
            for identity, executable in browsers:
                if not executable.exists():
                    results.skipped.append(f"{identity}: executable not installed")
                    print(f"SKIP {identity}: executable not installed", flush=True)
                    continue
                controller: BrowserController | None = None
                try:
                    if identity == "firefox":
                        controller = FirefoxController(executable, extension_root / "firefox.zip")
                    else:
                        controller = ChromiumController(identity, executable, extension_root / "chromium")
                    controllers.append(controller)
                    run_browser(controller, server, aw, run_id, args.core_url, results, verify_status=True)
                    if not collector_restarted:
                        outage_recovery(controller, server, aw, run_id, args.agent_pid, args.agent, config_path, args.core_url, args.token_file, results)
                        collector_restarted = True
                except UnsupportedBrowser as error:
                    results.skipped.append(f"{identity}: {error}")
                    print(f"SKIP {identity}: {error}", flush=True)
                except Exception as error:
                    if identity == "edge" and "DevTools remote debugging is disallowed by the system admin" in str(error):
                        results.skipped.append("edge: full tab-control scenarios blocked by corporate DevTools policy")
                        print("SKIP edge: full tab-control scenarios blocked by corporate DevTools policy", flush=True)
                        try:
                            edge_policy_smoke(executable, extension_root / "chromium", server, aw, run_id, results)
                        except Exception as smoke_error:
                            results.failed.append(f"edge policy-compatible smoke: {smoke_error}")
                            print(f"FAIL edge policy-compatible smoke: {smoke_error}", flush=True)
                    else:
                        results.failed.append(f"{identity}: {error}")
                        print(f"FAIL {identity}: {error}", flush=True)
                    if controller is not None and hasattr(controller, "extension_diagnostic"):
                        print("DIAGNOSTIC " + json.dumps(controller.extension_diagnostic(), default=str), flush=True)
                finally:
                    if controller is not None:
                        controller.close()
                        if controller in controllers:
                            controllers.remove(controller)
    finally:
        for controller in controllers:
            controller.close()
        if not ingest_healthy():
            restart_agent(args.agent, config_path, args.core_url, args.token_file)
            try:
                wait_until(ingest_healthy, 15, "collector restoration")
            except Exception as error:
                results.failed.append(f"collector restoration: {error}")

    print("\nIntegration summary")
    print(f"  passed:  {len(results.passed)}")
    print(f"  failed:  {len(results.failed)}")
    print(f"  skipped: {len(results.skipped)}")
    for failure in results.failed:
        print(f"  FAILURE {failure}")
    for skipped in results.skipped:
        print(f"  SKIPPED {skipped}")
    return 1 if results.failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
