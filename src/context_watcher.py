from __future__ import annotations

import base64
import json
import subprocess
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

from aw_client import ActivityWatchClient
from aw_core.models import Event


PROJECT_ROOT = Path(__file__).resolve().parent.parent
CONFIG_FILE = PROJECT_ROOT / "config.json"

POLL_SECONDS = 2
NETWORK_REFRESH_SECONDS = 60


def load_config():
    with CONFIG_FILE.open("r", encoding="utf-8") as f:
        return json.load(f)


CONFIG = load_config()


def get_vlc_state():
    url = CONFIG["vlc"]["url"]
    password = CONFIG["vlc"]["password"]

    token = base64.b64encode(
        f":{password}".encode("utf-8")
    ).decode("ascii")

    request = urllib.request.Request(
        url,
        headers={
            "Authorization": f"Basic {token}"
        },
    )

    try:
        with urllib.request.urlopen(request, timeout=0.5) as response:
            data = json.load(response)

        return data.get("state", "unknown").lower()

    except Exception :
        return "unavailable"


def get_wifi_fingerprint():
    script = r"""
$cfg = Get-NetIPConfiguration -InterfaceAlias "Wi-Fi" -ErrorAction SilentlyContinue

if (-not $cfg) {
    return
}

$gw = $cfg.IPv4DefaultGateway.NextHop

if (-not $gw) {
    return
}

ping -n 1 $gw | Out-Null

$neighbor = Get-NetNeighbor `
    -InterfaceAlias "Wi-Fi" `
    -IPAddress $gw `
    -ErrorAction SilentlyContinue

[PSCustomObject]@{
    IP = $cfg.IPv4Address.IPAddress
    Gateway = $gw
    GatewayMAC = $neighbor.LinkLayerAddress
} | ConvertTo-Json -Compress
"""

    try:
        result = subprocess.run(
            [
                "powershell.exe",
                "-NoProfile",
                "-Command",
                script,
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )

        output = result.stdout.strip()

        if not output:
            return None

        return json.loads(output)

    except Exception:
        return None


def normalize_mac(value):
    if not value:
        return ""

    return value.strip().upper().replace(":", "-")


def detect_location(fingerprint):
    if not fingerprint:
        return "REMOTE"

    mac = normalize_mac(
        fingerprint.get("GatewayMAC", "")
    )

    office_macs = {
        normalize_mac(x)
        for x in CONFIG.get("office_gateway_macs", [])
    }

    home_macs = {
        normalize_mac(x)
        for x in CONFIG.get("home_gateway_macs", [])
    }

    if mac in office_macs:
        return "OFFICE"

    if mac in home_macs:
        return "REMOTE"

    # Unknown network gets the stricter rules.
    return "REMOTE"


def main():
    client = ActivityWatchClient(
        "aw-watcher-work-context"
    )

    bucket_id = (
        f"aw-watcher-work-context_"
        f"{client.client_hostname}"
    )

    client.create_bucket(
        bucket_id,
        event_type="workcontext",
    )

    fingerprint = None
    location = "REMOTE"
    last_network_check = 0.0

    print(f"Bucket: {bucket_id}")
    print("Context watcher running.")
    print("Press Ctrl+C to stop.")
    print()

    with client:
        while True:
            now_monotonic = time.monotonic()

            if (
                now_monotonic - last_network_check
                >= NETWORK_REFRESH_SECONDS
            ):
                fingerprint = get_wifi_fingerprint()
                location = detect_location(fingerprint)
                last_network_check = now_monotonic

            vlc_state = get_vlc_state()

            data = {
                "location": location,
                "vlc_state": vlc_state,
            }

            event = Event(
                timestamp=datetime.now(timezone.utc),
                data=data,
            )

            client.heartbeat(
                bucket_id,
                event,
                pulsetime=POLL_SECONDS + 2,
                queued=True,
                commit_interval=15,
            )

            ip = (
                fingerprint.get("IP", "?")
                if fingerprint
                else "?"
            )

            print(
            f"{datetime.now():%H:%M:%S} | "
            f"Location: {location:<7} | "
            f"VLC: {vlc_state:<11} | "
            f"Wi-Fi: {ip}"
            )

            time.sleep(POLL_SECONDS)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print("\nStopped.")