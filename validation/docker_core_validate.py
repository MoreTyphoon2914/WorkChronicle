"""Privacy-safe live validator for the Docker WorkChronicle Core APIs."""

from __future__ import annotations

import argparse
import json
import math
import urllib.request


def get_json(base: str, path: str) -> dict:
    with urllib.request.urlopen(base.rstrip("/") + path, timeout=15) as response:
        if response.status != 200:
            raise RuntimeError(f"GET {path} returned {response.status}")
        return json.load(response)


def totals(report: dict) -> tuple[float, float, float]:
    value = report["totals"]
    return (
        float(value["working_seconds"]),
        float(value["break_seconds"]),
        float(value["untracked_seconds"]),
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--expect-date-with-work")
    args = parser.parse_args()

    health = get_json(args.base_url, "/health")
    status = get_json(args.base_url, "/api/v1/status")
    today = get_json(args.base_url, "/api/v1/reports/today")
    week = get_json(args.base_url, "/api/v1/reports/week")

    if health.get("status") != "healthy" or not health.get("agent_connected"):
        raise RuntimeError(f"Core/Agent health is not healthy: {health}")
    counts = health.get("observation_counts") or {}
    for source in ("windows", "afk", "host_context"):
        if int(counts.get(source, 0)) <= 0:
            raise RuntimeError(f"Core has no real {source} observations: {counts}")
    days = week.get("days") or []
    summed = tuple(sum(totals(day)[index] for day in days) for index in range(3))
    weekly = totals(week)
    if any(not math.isclose(summed[index], weekly[index], abs_tol=1e-6) for index in range(3)):
        raise RuntimeError(f"weekly totals {weekly} do not equal daily sum {summed}")
    if args.expect_date_with_work:
        matching = [day for day in days if day.get("date") == args.expect_date_with_work]
        if not matching or totals(matching[0])[0] <= 0:
            raise RuntimeError(f"persisted work missing for {args.expect_date_with_work}")

    print(f"health={health['status']} agent_connected={health['agent_connected']} observations={counts}")
    print(f"status={status.get('work_state')} location={status.get('location')}")
    print(f"today={today.get('date')} totals_seconds={totals(today)}")
    print(
        f"week={week.get('period_start')}..{week.get('period_end')} "
        f"days={len(days)} totals_seconds={weekly} denominator={week.get('average_denominator')}"
    )
    print("PASS Docker Core owns classified status and reconciled reports")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
