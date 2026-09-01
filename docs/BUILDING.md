# Building and running the Docker Core

WorkChronicle uses a native Windows Host Agent for interactive-session facts
and a Linux Docker Core for all authoritative classification and reporting.
Run the examples below from Git Bash unless a section explicitly says
PowerShell.

## Data and network path

```text
Firefox / Chrome / Edge extensions
    -> http://127.0.0.1:5601
    -> Windows Host Agent

ActivityWatch / VLC / foreground / AFK / lock / physical network
    -> Windows Host Agent

Windows Host Agent
    -> Bearer-authenticated schema-V1 observation batches
    -> http://127.0.0.1:8080
    -> Docker published port
    -> Core port 8080

Dashboard and curl
    -> http://127.0.0.1:8080
    -> Docker Core
```

Core listens on `0.0.0.0:8080` *inside its container*. This is necessary for
Docker port forwarding. Compose binds the corresponding Windows host port to
`127.0.0.1:8080`, not `0.0.0.0`, so the API is reachable only through host
loopback.

## Dockerfile walkthrough

| Instruction | Purpose |
| --- | --- |
| `FROM golang:1.26@sha256:… AS build` | Uses a digest-pinned Go compiler stage. |
| `WORKDIR /src` | Establishes the isolated source directory. |
| `COPY go.mod ./` | Copies module metadata before source for cache reuse. |
| `COPY cmd ./cmd` | Copies application commands, including the real Core. |
| `COPY internal ./internal` | Copies the shared domain and business packages. |
| `RUN … go build … ./cmd/workchronicle-core` | Cross-builds a static Linux Core with reproducible paths and stripped symbols. |
| `FROM …distroless…@sha256:…` | Starts a digest-pinned minimal runtime without a shell or toolchain. |
| `COPY … /workchronicle-core` | Copies only the authoritative Core executable. |
| `COPY … /data` | Creates a directory owned by the runtime user. |
| `USER nonroot:nonroot` | Prevents root execution. |
| `VOLUME ["/data"]` | Documents the persistent data mount. |
| `EXPOSE 8080` | Documents the container API port; it does not publish it. |
| `HEALTHCHECK …` | Runs Core's built-in loopback health probe. |
| `ENTRYPOINT …` | Starts the real Core application. |

## Compose walkthrough

| Compose entry | Purpose |
| --- | --- |
| `name: workchronicle` | Gives resources a predictable project prefix. |
| `services.core.build` | Builds the repository Dockerfile. |
| `image: workchronicle-core:local` | Gives the demo image a stable local name. |
| `restart: unless-stopped` | Restarts Core unless an operator stopped it. |
| `127.0.0.1:8080:8080` | Publishes Core only on Windows loopback. |
| `environment` | Supplies non-secret runtime configuration and overridable defaults. |
| `WORKCHRONICLE_LISTEN_ADDR=0.0.0.0:8080` | Listens on the container network, not the host LAN. |
| `WORKCHRONICLE_BROWSER_ACTIVE_FRESHNESS` | Defines recent browser-integration heartbeat freshness. |
| `volumes: workchronicle-data:/data` | Persists observation history across recreation. |
| `secrets: agent_token` | Mounts the Agent credential as a file. |
| top-level `volumes` | Declares the persistent named volume. |
| top-level `secrets` | Reads the ignored local token file without adding it to the image. |

Other timing and work-target environment entries map directly to the existing
classification and WorkEvaluation configuration. Values can be overridden in
the shell or an ignored local `.env` file.

## Build and test

```bash
go test ./...
go vet ./...
go build ./...

docker compose config
docker compose build --no-cache
docker image ls workchronicle-core
```

Generate the local token once. The command deliberately refuses to overwrite
an existing token file:

```bash
go run ./cmd/workchronicle-core generate-token ./secrets/agent-token.txt
```

Start and inspect Core:

```bash
docker compose up -d
docker compose ps
docker compose logs -f core

curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8080/
curl http://127.0.0.1:8080/api/v1/status
curl http://127.0.0.1:8080/api/v1/reports/today
curl http://127.0.0.1:8080/api/v1/reports/week
```

`-d` means detached mode and is unrelated to persistence or volumes.

## Run the Windows Host Agent

Stop `worktracker run` and the Python context collector first. In PowerShell:

```powershell
go build -o .\bin\workchronicle-agent.exe .\cmd\workchronicle-agent
.\bin\workchronicle-agent.exe `
  --config .\config.json `
  --core-url http://127.0.0.1:8080 `
  --token-file .\secrets\agent-token.txt
```

The Agent deliberately shares the legacy collector's single-instance identity
and browser receiver port. It collects and normalizes facts but never assigns a
work state or evaluates targets.

Foreground/AFK acquisition defaults to ActivityWatch. To perform parity
validation, add this non-secret block to the ignored local `config.json`:

```json
"host_acquisition": {
  "mode": "shadow",
  "native_poll_seconds": 2,
  "native_afk_threshold_seconds": 180,
  "parity_tolerance_seconds": 5
}
```

In `shadow`, ActivityWatch remains the only classification authority. Native
Windows observations are persisted separately and appear only in `/health` and
the dashboard's Host acquisition diagnostics. `native` is an explicit cutover
mode that can run foreground/AFK/session acquisition without ActivityWatch; it
is deliberately not the default.

### Native watcher live-validation checklist

Record `/health` and `/api/v1/status` timestamps before and after each action.
Never run the legacy collector beside the Host Agent.

1. Set `mode` to `shadow`, start Core and one Agent, and confirm all four Host
   acquisition rows are connected. Confirm comparison summaries contain no
   titles or URLs.
2. At home/REMOTE, record normal input, stop input past the configured native
   AFK threshold, resume input, lock, and unlock. Then repeat AFK with browser
   video playing. ActivityWatch must remain authoritative throughout.
3. At OFFICE, record normal use, ordinary AFK while unlocked, lock, and unlock.
   Existing Core rules must continue to produce OFFICE work while unlocked and
   immediate BREAK while locked.
4. During both locations, switch rapidly among applications. Compare normalized
   executable/AFK/lock outcomes within `parity_tolerance_seconds`; exact event
   start/end equality is not required.
5. Stop/start the Agent, restart Core, and sleep/resume Windows. Confirm gaps are
   not bridged as observed time, persisted shadow history survives Core restart,
   and component timestamps recover.
6. Stop ActivityWatch temporarily while still in `shadow`. Confirm native rows
   continue updating independently while authoritative status becomes stale or
   UNTRACKED rather than silently using shadow facts.
7. Only after recording acceptable parity, explicitly test `native` mode with
   ActivityWatch stopped. Repeat REMOTE and OFFICE input/AFK/lock cases and
   confirm browser/VLC evidence remains independent.

For each case record the ActivityWatch and native last-observation timestamps,
comparison flags, status state/location/foreground executable, and the measured
transition-time difference. Risks to resolve before default cutover include
machine-specific secure-desktop behavior, idle-threshold mismatch, sleep/resume
gaps, and foreground process access denied by protected applications.

## Container lifecycle and persistence

```bash
docker compose stop
docker compose start
docker compose restart
docker compose down
docker compose up -d
```

`stop` keeps the container. `down` removes the service container and Compose
network but retains named volumes. The next `up -d` recreates Core and loads its
history from `/data/state.json`.

Warning: this command also deletes the named data volume and its persisted
history:

```bash
docker compose down -v
```

Use it only when intentionally resetting demo data.

## Browser extension packages

From PowerShell, because the checked-in packaging scripts are PowerShell:

```powershell
.\browser-extension\test.ps1
.\browser-extension\build.ps1
```

The build creates one Firefox package and one Chromium package shared by Chrome
and Edge under ignored `browser-extension/dist/`. Firefox developer loading is
temporary and must be repeated after a Firefox restart. Chrome and Edge unpacked
installs normally remain configured, subject to enterprise policy. Signed,
published, or managed distribution is required for production deployment.
