from __future__ import annotations

import json
import urllib.request
from datetime import datetime, timedelta, time, timezone
from pathlib import Path
from zoneinfo import ZoneInfo


PROJECT_ROOT = Path(__file__).resolve().parent.parent
CONFIG_FILE = PROJECT_ROOT / "config.json"


def load_config():
    with CONFIG_FILE.open("r", encoding="utf-8") as f:
        return json.load(f)


CONFIG = load_config()

SERVER = CONFIG["server"].rstrip("/")
LOCAL_TZ = ZoneInfo(CONFIG["timezone"])

WINDOW_BUCKET = CONFIG["window_bucket"]
AFK_BUCKET = CONFIG["afk_bucket"]

STATUS_STALE_SECONDS = CONFIG.get("status_stale_seconds", 15)

TARGET_SECONDS = CONFIG["target_hours"] * 3600
AFK_GRACE_SECONDS = CONFIG["afk_grace_minutes"] * 60
AUTO_END_SECONDS = CONFIG["auto_end_after_minutes"] * 60

PASSIVE_APPS = {
    app.lower()
    for app in CONFIG["passive_work_apps"]
}

LOCK_APPS = {
    app.lower()
    for app in CONFIG["lock_apps"]
}

LOCK_TITLES = [
    text.lower()
    for text in CONFIG["lock_title_contains"]
]


def parse_iso(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def get_day_period():
    now = datetime.now(LOCAL_TZ)

    hour, minute = map(int, CONFIG["day_boundary"].split(":"))

    boundary = datetime.combine(
        now.date(),
        time(hour, minute),
        tzinfo=LOCAL_TZ,
    )

    if now < boundary:
        boundary -= timedelta(days=1)

    return boundary, now


def query_activitywatch(start: datetime, end: datetime):
    period = f"{start.isoformat()}/{end.isoformat()}"

    query = [
        f'windows = flood(query_bucket(find_bucket("{WINDOW_BUCKET}")));',
        f'afk = flood(query_bucket(find_bucket("{AFK_BUCKET}")));',
        'RETURN = {"windows": windows, "afk": afk};',
    ]

    body = json.dumps({
        "query": query,
        "timeperiods": [period],
    }).encode("utf-8")

    request = urllib.request.Request(
        f"{SERVER}/api/0/query/",
        data=body,
        headers={"Content-Type": "application/json"},
    )

    with urllib.request.urlopen(request, timeout=10) as response:
        result = json.load(response)

    return result[0]


def make_intervals(events):
    intervals = []

    for event in events:
        start = parse_iso(event["timestamp"])
        duration = float(event.get("duration", 0))

        intervals.append({
            "start": start,
            "end": start + timedelta(seconds=duration),
            "duration": duration,
            "data": event.get("data", {}),
        })

    intervals.sort(key=lambda event: event["start"])

    return intervals


def get_matching_event(events, moment):
    for event in events:
        if event["start"] <= moment < event["end"]:
            return event

    return None


def is_locked(window_event):
    if not window_event:
        return False

    app = window_event["data"].get("app", "").lower()
    title = window_event["data"].get("title", "").lower()

    if app in LOCK_APPS:
        return True

    return any(text in title for text in LOCK_TITLES)


def is_passive_work(window_event):
    if not window_event:
        return False

    app = window_event["data"].get("app", "").lower()

    return app in PASSIVE_APPS


def classify_timeline(windows, afk_events, start, end):
    boundaries = {start.astimezone(timezone.utc), end.astimezone(timezone.utc)}

    for event in windows:
        boundaries.add(event["start"])
        boundaries.add(event["end"])

    for event in afk_events:
        boundaries.add(event["start"])
        boundaries.add(event["end"])

        # Important:
        # split an AFK interval exactly where our grace period expires.
        grace_end = event["start"] + timedelta(seconds=AFK_GRACE_SECONDS)

        if event["start"] < grace_end < event["end"]:
            boundaries.add(grace_end)

    boundaries = sorted(
        point
        for point in boundaries
        if start.astimezone(timezone.utc)
        <= point
        <= end.astimezone(timezone.utc)
    )

    segments = []

    for left, right in zip(boundaries, boundaries[1:]):
        if right <= left:
            continue

        midpoint = left + (right - left) / 2

        window = get_matching_event(windows, midpoint)
        afk = get_matching_event(afk_events, midpoint)

        duration = (right - left).total_seconds()

        # Rule 1: Locked always wins.
        if window is None:
            state = "UNTRACKED"
        
        # Locked always means break.
        elif is_locked(window):
            state = "BREAK"

        else:
            afk_status = (
                afk["data"].get("status", "")
                if afk
                else ""
            ).lower()

            # Rule 2: Explicitly active.
            if afk_status != "afk":
                state = "WORKING"

            # Rule 3: Passive work such as VLC.
            elif is_passive_work(window):
                state = "WORKING"

            # Rule 4: AFK grace period.
            elif afk:
                afk_age = (
                    midpoint - afk["start"]
                ).total_seconds()

                if afk_age <= AFK_GRACE_SECONDS:
                    state = "WORKING"
                else:
                    state = "BREAK"

            else:
                state = "WORKING"

        segments.append({
            "start": left,
            "end": right,
            "duration": duration,
            "state": state,
            "window": window,
            "afk": afk,
        })

    return segments


def calculate(segments, now):
    work_segments = [
        segment
        for segment in segments
        if segment["state"] == "WORKING"
    ]

    if not work_segments:
        return None

    workday_start = work_segments[0]["start"]

    relevant = [
        segment
        for segment in segments
        if segment["end"] > workday_start
    ]

    # Determine whether we're sitting in a long trailing break.
    last_work_end = work_segments[-1]["end"]

    trailing_idle = (
        now.astimezone(timezone.utc) - last_work_end
    ).total_seconds()

    if trailing_idle >= AUTO_END_SECONDS:
        effective_end = last_work_end
    else:
        effective_end = now.astimezone(timezone.utc)
    untracked = 0.0
    working = 0.0
    breaking = 0.0

    for segment in relevant:
        segment_start = max(segment["start"], workday_start)
        segment_end = min(segment["end"], effective_end)

        if segment_end <= segment_start:
            continue

        duration = (segment_end - segment_start).total_seconds()

        if segment["state"] == "WORKING":
            working += duration
        elif segment["state"] == "BREAK":
            breaking += duration
        else:
            untracked += duration

    return {
    "start": workday_start,
    "end": effective_end,
    "working": working,
    "break": breaking,
    "untracked": untracked,
    }        


def format_duration(seconds):
    seconds = max(0, int(seconds))

    hours, remainder = divmod(seconds, 3600)
    minutes = remainder // 60

    return f"{hours}h {minutes:02d}m"

def get_current_state(segments, now):
    now_utc = now.astimezone(timezone.utc)

    for segment in reversed(segments):
        if segment["state"] == "UNTRACKED":
            continue

        age = (now_utc - segment["end"]).total_seconds()

        if age <= STATUS_STALE_SECONDS:
            return segment["state"]

        break

    return "UNTRACKED"

def main():
    start, now = get_day_period()

    try:
        raw = query_activitywatch(start, now)
    except Exception as exc:
        print("Could not query ActivityWatch.")
        print(exc)
        return

    windows = make_intervals(raw["windows"])
    afk_events = make_intervals(raw["afk"])

    segments = classify_timeline(
        windows,
        afk_events,
        start,
        now,
    )

    result = calculate(segments, now)

    if not result:
        print("No qualifying work activity found today.")
        return

    working = result["working"]
    breaking = result["break"]
    untracked = result["untracked"]

    remaining = max(0, TARGET_SECONDS - working)

    start_local = result["start"].astimezone(LOCAL_TZ)
    end_local = result["end"].astimezone(LOCAL_TZ)

    current_state = get_current_state(segments, now)

    print()
    print("WORK TIME")
    print("=" * 32)
    print(f"Start       {start_local:%H:%M}")
    print(f"Working     {format_duration(working)}")
    print(f"Break       {format_duration(breaking)}")

    if untracked >= 60:
        print(f"Untracked   {format_duration(untracked)}")
    print("-" * 32)
    print(f"Target      {format_duration(TARGET_SECONDS)}")
    print(f"Remaining   {format_duration(remaining)}")
    print("-" * 32)

    print(f"Status      {current_state}")

    if end_local < now - timedelta(seconds=1):
        print(f"Auto-ended  {end_local:%H:%M}")

    print()


if __name__ == "__main__":
    main()