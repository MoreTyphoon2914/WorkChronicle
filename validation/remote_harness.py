#!/usr/bin/env python3
"""Live REMOTE validation recorder.

This tool reads WorkTracker/ActivityWatch state and can control VLC through
its configured loopback HTTP API. It never inserts ActivityWatch events,
changes thresholds, or synthesizes user input.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import socket
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Callable


def now_utc() -> datetime:
    return datetime.now(timezone.utc)


def iso(value: datetime | None = None) -> str:
    return (value or now_utc()).isoformat().replace("+00:00", "Z")


def request_json(url: str, timeout: float = 10) -> Any:
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return json.load(response)


class Recorder:
    def __init__(self, repo: Path, scenario: str) -> None:
        self.repo = repo.resolve()
        self.config_path = self.repo / "config.json"
        self.worktracker = self.repo / "bin" / "worktracker.exe"
        self.config = json.loads(self.config_path.read_text(encoding="utf-8"))
        self.server = self.config.get("server", "http://localhost:5600").rstrip("/")
        self.scenario = scenario
        self.result_dir = self.repo / "validation" / "results"
        self.result_dir.mkdir(parents=True, exist_ok=True)
        self.evidence_path = self.result_dir / f"{scenario}.jsonl"
        self.result_path = self.result_dir / f"{scenario}.result.json"
        self.buckets = self._resolve_buckets()

    def _resolve_buckets(self) -> dict[str, str]:
        buckets = request_json(self.server + "/api/0/buckets/")
        hostname = socket.gethostname()

        def discover(kind: str, explicit: str = "") -> str:
            if explicit:
                if explicit not in buckets:
                    raise RuntimeError(f"configured {kind} bucket is missing")
                return explicit
            matches = []
            for bucket_id, bucket in buckets.items():
                combined = (bucket_id + " " + str(bucket.get("type", ""))).lower()
                host_matches = str(bucket.get("hostname", "")).lower() == hostname.lower() or hostname.lower() in bucket_id.lower()
                if host_matches and kind in combined:
                    matches.append(bucket_id)
            if len(matches) != 1:
                raise RuntimeError(f"expected one {kind} bucket, found {len(matches)}")
            return matches[0]

        output = {
            "window": discover("window", self.config.get("window_bucket", "")),
            "afk": discover("afk", self.config.get("afk_bucket", "")),
        }
        for key, prefix in (
            ("context", "aw-watcher-work-context_"),
            ("browser", "aw-watcher-browser-context_"),
        ):
            wanted = prefix + hostname
            match = next((bucket_id for bucket_id in buckets if bucket_id.lower() == wanted.lower()), "")
            if match:
                output[key] = match
        return output

    def _events(self, kind: str, limit: int = 6) -> list[dict[str, Any]]:
        bucket = self.buckets.get(kind)
        if not bucket:
            return []
        path = urllib.parse.quote(bucket, safe="")
        query = urllib.parse.urlencode({"limit": limit})
        return request_json(f"{self.server}/api/0/buckets/{path}/events?{query}")

    def _command_json(self, command: str) -> Any:
        flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
        completed = subprocess.run(
            [str(self.worktracker), command, "--config", str(self.config_path), "--json"],
            cwd=self.repo,
            capture_output=True,
            text=True,
            timeout=45,
            creationflags=flags,
        )
        if completed.returncode != 0:
            raise RuntimeError(f"worktracker {command} failed: {completed.stderr.strip()}")
        return json.loads(completed.stdout)

    @staticmethod
    def _current_data(events: list[dict[str, Any]]) -> dict[str, Any]:
        if not events:
            return {}

        def timestamp(event: dict[str, Any]) -> datetime:
            value = str(event.get("timestamp", "")).replace("Z", "+00:00")
            try:
                return datetime.fromisoformat(value)
            except ValueError:
                return datetime.min.replace(tzinfo=timezone.utc)

        # ActivityWatch ordering can vary when the AFK watcher leaves repeated
        # heartbeat rows with the same start. Select newest evidence explicitly.
        return max(events, key=timestamp).get("data", {})

    def capture(self, phase: str) -> dict[str, Any]:
        captured = now_utc()
        raw = {kind: self._events(kind) for kind in ("window", "afk", "context", "browser")}
        record = {
            "captured_at": iso(captured),
            "scenario": self.scenario,
            "phase": phase,
            "status": self._command_json("status"),
            "thresholds": {
                "afk_grace_seconds": float(self.config.get("afk_grace_minutes", 0)) * 60,
            },
            "current_raw": {kind: self._current_data(events) for kind, events in raw.items()},
            "events": raw,
        }
        with self.evidence_path.open("a", encoding="utf-8") as output:
            output.write(json.dumps(record, separators=(",", ":")) + "\n")
        return record

    def control_vlc(self, state: str) -> dict[str, Any]:
        vlc = self.config.get("vlc", {})
        url = str(vlc.get("url", ""))
        if not url.startswith(("http://127.0.0.1:", "http://localhost:")):
            raise RuntimeError("refusing to control a non-loopback VLC endpoint")
        command = {"playing": "pl_play", "paused": "pl_pause", "stopped": "pl_stop"}[state]
        parsed = urllib.parse.urlsplit(url)
        query = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
        query.append(("command", command))
        command_url = urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))
        token = base64.b64encode((":" + str(vlc.get("password", ""))).encode()).decode()
        request = urllib.request.Request(command_url, headers={"Authorization": "Basic " + token})
        with urllib.request.urlopen(request, timeout=5) as response:
            payload = json.load(response)
        observed = str(payload.get("state", "unknown")).lower()
        result = {"requested": state, "observed": observed, "at": iso()}
        with self.evidence_path.open("a", encoding="utf-8") as output:
            output.write(json.dumps({"scenario": self.scenario, "phase": "vlc-control", "vlc_control": result}, separators=(",", ":")) + "\n")
        return result

    def result(self, expected: str, passed: bool, reason: str, start: str, end: str, extra: dict[str, Any] | None = None) -> None:
        result = {
            "scenario": self.scenario,
            "start": start,
            "end": end,
            "expected": expected,
            "result": "PASS" if passed else "FAIL",
            "reason": reason,
            "evidence_file": str(self.evidence_path),
        }
        if extra:
            result.update(extra)
        self.result_path.write_text(json.dumps(result, indent=2), encoding="utf-8")


def afk_status(record: dict[str, Any]) -> str:
    return str(record.get("current_raw", {}).get("afk", {}).get("status", "")).lower()


def afk_age_seconds(record: dict[str, Any]) -> float:
    events = record.get("events", {}).get("afk", [])
    if not events:
        return -1
    latest = max(events, key=lambda event: str(event.get("timestamp", "")))
    try:
        started = datetime.fromisoformat(str(latest["timestamp"]).replace("Z", "+00:00"))
        captured = datetime.fromisoformat(str(record["captured_at"]).replace("Z", "+00:00"))
    except (KeyError, ValueError):
        return -1
    return (captured - started).total_seconds()


def beyond_afk_grace(record: dict[str, Any]) -> bool:
    grace = float(record.get("thresholds", {}).get("afk_grace_seconds", 0))
    return afk_age_seconds(record) >= grace


def locked(record: dict[str, Any]) -> bool:
    window = record.get("current_raw", {}).get("window", {})
    app = str(window.get("app", "")).lower()
    title = str(window.get("title", "")).lower()
    return app == "lockapp.exe" or "windows default lock screen" in title


def browser_playing(record: dict[str, Any]) -> bool:
    evidence = record.get("status", {}).get("passive_detector_evidence", {})
    return any(key.startswith("browser:") and value.get("available") and value.get("passive_work") for key, value in evidence.items())


def baseline(recorder: Recorder) -> int:
    start = iso()
    record = recorder.capture("baseline")
    status = record["status"]
    evidence = status.get("passive_detector_evidence", {})
    vlc = evidence.get("vlc", {})
    checks = {
        "remote_confirmed": status.get("location") == "REMOTE" and status.get("location_evidence") == "confirmed",
        "active_afk": afk_status(record) in ("not-afk", "active"),
        "working": status.get("work_state") == "WORKING",
        "vlc_not_playing": not vlc.get("passive_work", False) and vlc.get("state") in ("paused", "stopped"),
        "browser_not_playing": not browser_playing(record),
        "unlocked": not locked(record),
    }
    passed = all(checks.values())
    recorder.result(
        "REMOTE confirmed + active input + no passive playback => WORKING",
        passed,
        "all baseline predicates satisfied" if passed else "one or more baseline predicates failed",
        start,
        record["captured_at"],
        {"checks": checks},
    )
    print(json.dumps({"record": record, "checks": checks, "pass": passed}, indent=2))
    return 0 if passed else 1


CONDITIONS: dict[str, Callable[[dict[str, Any]], bool]] = {
    "break_afk": lambda record: afk_status(record) == "afk" and record["status"].get("work_state") == "BREAK",
    "working_afk_vlc": lambda record: afk_status(record) == "afk" and beyond_afk_grace(record) and record["status"].get("work_state") == "WORKING" and bool(record["status"].get("passive_detector_evidence", {}).get("vlc", {}).get("passive_work")),
    "working_afk_browser": lambda record: afk_status(record) == "afk" and beyond_afk_grace(record) and record["status"].get("work_state") == "WORKING" and browser_playing(record),
    "locked_break": lambda record: locked(record) and record["status"].get("work_state") == "BREAK",
    "unlocked_working": lambda record: not locked(record) and record["status"].get("work_state") == "WORKING",
}


def monitor(recorder: Recorder, condition: str, timeout: float, interval: float) -> int:
    predicate = CONDITIONS[condition]
    started = now_utc()
    last: dict[str, Any] | None = None
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        last = recorder.capture("monitor")
        if predicate(last):
            elapsed = (now_utc() - started).total_seconds()
            print(json.dumps({"condition": condition, "met": True, "elapsed_seconds": elapsed, "record": last}, indent=2))
            return 0
        time.sleep(interval)
    print(json.dumps({"condition": condition, "met": False, "last": last}, indent=2))
    return 1


def snapshot(recorder: Recorder, phase: str) -> int:
    print(json.dumps(recorder.capture(phase), indent=2))
    return 0


def control_vlc(recorder: Recorder, state: str) -> int:
    result = recorder.control_vlc(state)
    print(json.dumps(result, indent=2))
    return 0 if result["observed"] == state else 1


def audit_history(recorder: Recorder) -> int:
    counts = {"legacy_v0": 0, "v1": 0, "unsupported": 0}
    for event in recorder._events("context", limit=-1):
        data = event.get("data", {})
        if "schema_version" not in data:
            counts["legacy_v0"] += 1
        elif data.get("schema_version") == 1:
            counts["v1"] += 1
        else:
            counts["unsupported"] += 1
    result = {
        "context_bucket": recorder.buckets.get("context", ""),
        "counts": counts,
        "mixed_legacy_and_v1": counts["legacy_v0"] > 0 and counts["v1"] > 0,
    }
    print(json.dumps(result, indent=2))
    return 0 if result["mixed_legacy_and_v1"] else 1


def summarize_results(recorder: Recorder) -> int:
    summaries = []
    for path in sorted(recorder.result_dir.glob("*.jsonl")):
        captures = []
        for line in path.read_text(encoding="utf-8").splitlines():
            record = json.loads(line)
            if record.get("captured_at") and record.get("status"):
                captures.append(record)
        if not captures:
            continue
        first, last = captures[0], captures[-1]
        summaries.append({
            "scenario": path.stem,
            "start": first["captured_at"],
            "end": last["captured_at"],
            "first_state": first["status"].get("work_state"),
            "last_state": last["status"].get("work_state"),
            "last_location": last["status"].get("location"),
            "last_location_evidence": last["status"].get("location_evidence"),
            "last_afk": afk_status(last),
            "last_foreground": last["status"].get("foreground", {}),
            "last_passive": last["status"].get("passive_detector_evidence", {}),
        })
    print(json.dumps(summaries, indent=2))
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--scenario", required=True)
    subparsers = parser.add_subparsers(dest="command", required=True)
    baseline_parser = subparsers.add_parser("baseline")
    baseline_parser.set_defaults(handler=lambda args, recorder: baseline(recorder))
    snapshot_parser = subparsers.add_parser("snapshot")
    snapshot_parser.add_argument("--phase", required=True)
    snapshot_parser.set_defaults(handler=lambda args, recorder: snapshot(recorder, args.phase))
    monitor_parser = subparsers.add_parser("monitor")
    monitor_parser.add_argument("--condition", choices=sorted(CONDITIONS), required=True)
    monitor_parser.add_argument("--timeout", type=float, default=600)
    monitor_parser.add_argument("--interval", type=float, default=2)
    monitor_parser.set_defaults(handler=lambda args, recorder: monitor(recorder, args.condition, args.timeout, args.interval))
    vlc_parser = subparsers.add_parser("vlc")
    vlc_parser.add_argument("--state", choices=("playing", "paused", "stopped"), required=True)
    vlc_parser.set_defaults(handler=lambda args, recorder: control_vlc(recorder, args.state))
    audit_parser = subparsers.add_parser("audit-history")
    audit_parser.set_defaults(handler=lambda args, recorder: audit_history(recorder))
    summary_parser = subparsers.add_parser("summarize-results")
    summary_parser.set_defaults(handler=lambda args, recorder: summarize_results(recorder))
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    return args.handler(args, Recorder(args.repo, args.scenario))


if __name__ == "__main__":
    raise SystemExit(main())
