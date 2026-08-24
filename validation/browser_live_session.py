#!/usr/bin/env python3
"""Disposable Firefox media session for live REMOTE validation."""

from __future__ import annotations

import importlib.util
import json
import secrets
import sys
from datetime import datetime, timezone
from pathlib import Path


def load_integration(repo: Path):
    path = repo / "browser-extension" / "integration" / "run.py"
    spec = importlib.util.spec_from_file_location("worktracker_browser_integration", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("could not load browser integration helpers")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def emit(**values: object) -> None:
    print(json.dumps(values, separators=(",", ":")), flush=True)


def main() -> int:
    repo = Path(__file__).resolve().parents[1]
    integration = load_integration(repo)
    firefox = Path(r"C:\Program Files\Mozilla Firefox\firefox.exe")
    archive = repo / "browser-extension" / "dist" / "firefox.zip"
    run_id = datetime.now(timezone.utc).strftime("live-%Y%m%dT%H%M%S-") + secrets.token_hex(3)
    server = integration.LocalTestServer()
    controller = None
    tab = None
    other = None
    try:
        server.__enter__()
        controller = integration.FirefoxController(firefox, archive)
        tab = controller.create_tab(f"{server.base_url}/scenario.html?run={run_id}&browser=firefox")
        result = controller.evaluate(
            tab,
            "(async()=>{const s=await wtTest.addVideo(true);"
            "document.querySelector('#test-media').loop=true;return s})()",
        )
        emit(ready=True, run_id=run_id, browser="firefox", state="playing", media=result)
        for line in sys.stdin:
            command = line.strip().lower()
            if command == "play":
                result = controller.evaluate(tab, "(async()=>{document.querySelector('#test-media').loop=true;return await wtTest.play()})()")
                emit(command=command, state="playing", media=result)
            elif command == "pause":
                result = controller.evaluate(tab, "wtTest.pause()")
                emit(command=command, state="paused", media=result)
            elif command == "background":
                if other is None:
                    other = controller.create_tab(f"{server.base_url}/empty.html?run={run_id}&browser=firefox")
                controller.activate(other)
                emit(command=command, state="background")
            elif command == "foreground":
                controller.activate(tab)
                emit(command=command, state="foreground")
            elif command == "snapshot":
                emit(command=command, media=controller.evaluate(tab, "wtTest.snapshot()"))
            elif command in ("stop", "quit", "exit"):
                emit(command=command, stopped=True)
                return 0
            elif command:
                emit(command=command, error="unknown command")
    finally:
        if controller is not None:
            controller.close()
        server.__exit__(None, None, None)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
