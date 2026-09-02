#!/usr/bin/env python3
"""Privacy-minimal GetLastInputInfo reset probe for Windows validation.

The JSONL output intentionally contains only wall-clock time, the prior and
new last-input ticks, and elapsed wall time since the previous observed reset.
It does not install input hooks or record input content, type, or position.
"""

from __future__ import annotations

import argparse
import ctypes
from ctypes import wintypes
from datetime import datetime, timezone
import json
import time


class LASTINPUTINFO(ctypes.Structure):
    _fields_ = [("cbSize", wintypes.UINT), ("dwTime", wintypes.DWORD)]


def last_input_tick() -> int:
    info = LASTINPUTINFO(ctypes.sizeof(LASTINPUTINFO), 0)
    if not ctypes.windll.user32.GetLastInputInfo(ctypes.byref(info)):
        raise ctypes.WinError(ctypes.get_last_error())
    return int(info.dwTime)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--duration-seconds", type=float, default=60)
    parser.add_argument("--poll-seconds", type=float, default=0.05)
    args = parser.parse_args()
    if args.duration_seconds < 60:
        parser.error("duration must be at least 60 seconds")
    if not 0.01 <= args.poll_seconds <= 2:
        parser.error("poll interval must be between 0.01 and 2 seconds")

    previous_tick = last_input_tick()
    previous_reset_at = time.monotonic()
    deadline = time.monotonic() + args.duration_seconds
    while time.monotonic() < deadline:
        tick = last_input_tick()
        if tick != previous_tick:
            observed = time.monotonic()
            print(json.dumps({
                "wall_clock_timestamp": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
                "previous_last_input_tick": previous_tick,
                "new_last_input_tick": tick,
                "seconds_since_previous_reset": round(observed - previous_reset_at, 6),
            }, separators=(",", ":")), flush=True)
            previous_tick = tick
            previous_reset_at = observed
        time.sleep(args.poll_seconds)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
