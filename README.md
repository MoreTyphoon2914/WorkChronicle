# WorkChronicle

Windows work-time tracker backed by ActivityWatch. The Go implementation keeps the Python proof of concept in `src/` unchanged and reads both its legacy context events and the versioned Go context schema.

## Configuration

Copy `config.example.json` to the ignored `config.json` and replace local-only values there. Never commit passwords or physical-network fingerprints. Explicit window/AFK bucket IDs are optional; WorkChronicle otherwise discovers a unique host-matching bucket.

The `work_targets` block configures fixed expectations: `daily_target_hours`, `daily_standard_minimum_hours`, `workdays_per_week`, and `overtime_rule`. Weekly values are derived by multiplying the daily values by the configured workdays. Older `target_hours` and `work_policy` keys remain read-compatible aliases. Work evaluations are calculated from classified `WORKING` totals at report time and are never stored in ActivityWatch.

JSON reports expose canonical `work_evaluation` and `week_to_date_evaluation` objects. The earlier `work_policy` and `week_to_date_policy` names are emitted as temporary compatibility aliases with identical values.

## Build and checks

```bash
go test ./...
go vet ./...
go build ./...
go build -o ./bin/worktracker.exe ./cmd/worktracker
```

## Commands

```bash
./bin/worktracker.exe doctor --config ./config.json
./bin/worktracker.exe status --config ./config.json
./bin/worktracker.exe today --config ./config.json
./bin/worktracker.exe week --config ./config.json --json
./bin/worktracker.exe week --config ./config.json --json-v2
./bin/worktracker.exe run --config ./config.json
```

`week --json` retains the original top-level day array for compatibility.
`week --json-v2` returns the versioned weekly summary object with period totals,
the average denominator, weekly evaluation, and the same detailed daily data.

## Docker Core demo

The authoritative WorkChronicle classification and reporting application can
run in Docker. A small Windows Host Agent remains native because interactive
foreground/input/session state, physical Wi-Fi, VLC, ActivityWatch compatibility, and the browser
extension's loopback receiver belong to the signed-in Windows session.

The Agent sends normalized facts to Core. It does not assign
`WORKING`/`BREAK`/`UNTRACKED` and does not evaluate work targets. Core persists
the facts under `/data`, derives evidence, classifies the timeline, evaluates
targets, and exposes reports.

### Host acquisition modes

`host_acquisition.mode` controls only where normalized foreground and AFK facts
come from. It defaults to `activitywatch`:

- `activitywatch`: existing production behavior; native watchers are disabled.
- `shadow`: ActivityWatch remains authoritative while native foreground, input,
  and session observations are persisted in separate diagnostic streams.
- `native`: native foreground/input/session facts become authoritative and the
  Agent does not require ActivityWatch connectivity. ActivityWatch support and
  its historical data remain available for later import and troubleshooting.

Shadow observations never enter the classifier's window or AFK inputs. The
dashboard's **Host acquisition** section shows component freshness and a
privacy-safe parity summary without window titles. Comparison uses normalized
executable, AFK, and lock state, accepting observations through
`parity_tolerance_seconds`; it intentionally does not demand identical sampling
boundaries. `native_afk_threshold_seconds` should initially match the installed
ActivityWatch AFK watcher's idle threshold. Native mode is not the default and
must not be promoted until live parity validation succeeds.

From Git Bash, create the local shared secret and build/start Core:

```bash
go run ./cmd/workchronicle-core generate-token ./secrets/agent-token.txt
docker compose config
docker compose build
docker compose up -d
docker compose ps
docker compose logs -f core
```

The `-d` flag means detached: containers run without occupying the terminal. It
does not control persistence or volumes.

Stop the existing `worktracker run` process and the Python context collector;
the Host Agent deliberately uses the same single-instance identity and the
same browser loopback port. Build and start the Agent in a normal Windows
terminal:

```powershell
go build -o .\bin\workchronicle-agent.exe .\cmd\workchronicle-agent
.\bin\workchronicle-agent.exe --config .\config.json `
  --core-url http://127.0.0.1:8080 `
  --token-file .\secrets\agent-token.txt
```

Open the dashboard at <http://127.0.0.1:8080/>. From Git Bash, inspect the
Docker-owned APIs with:

```bash
curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8080/api/v1/status
curl http://127.0.0.1:8080/api/v1/reports/today
curl http://127.0.0.1:8080/api/v1/reports/week
```

`/health` retains the aggregate browser observation count and also reports
Firefox, Chrome, and Edge integration status independently. “Connected” means a
heartbeat from that extension family was seen within
`WORKCHRONICLE_BROWSER_ACTIVE_FRESHNESS` (30 seconds by default); it does not
claim that every browser process on the machine is open.

The observation endpoint is `POST /api/v1/observations`, requires the shared
Bearer token, and accepts schema-V1 batches containing window, AFK, raw legacy
context, host network/app, and raw browser observations. Compose publishes Core
only on `127.0.0.1:8080`; the token is mounted as a secret file and is never
baked into the image.

The current networking path is:

```text
Browser extensions -> 127.0.0.1:5601 -> Windows Host Agent
ActivityWatch and/or native Windows foreground/input/session -> Windows Host Agent
Physical network / VLC -> Windows Host Agent
Windows Host Agent -> authenticated HTTP -> 127.0.0.1:8080
                   -> Docker published port -> Core container port 8080
Dashboard / curl -> 127.0.0.1:8080 -> Docker Core
```

Inside the container, `WORKCHRONICLE_LISTEN_ADDR=0.0.0.0:8080` allows Core to
receive traffic arriving through Docker's virtual network. Compose publishes
that container port only as `127.0.0.1:8080:8080`, so it is not exposed on the
Windows LAN interface.

`docker compose down` preserves the named `workchronicle-data` volume. After
`docker compose up -d`, reports are reconstructed from the persisted Core
state. `docker compose stop`, `start`, and `restart` manage the existing
container. `docker compose down` removes containers and the Compose network but
preserves the named volume. **Do not run `docker compose down -v` unless you
intend to delete the persistent WorkChronicle demo history.**

See [docs/BUILDING.md](docs/BUILDING.md) for the Dockerfile, Compose, clean-build,
and deployment walkthrough.

On Windows, add `--tray` to the existing collector command to enable the
optional system tray presentation in the same process:

```powershell
.\bin\worktracker.exe run --config .\config.json --tray
```

The normal command without `--tray` remains the console development mode. The
tray refreshes from the existing reporting service every five seconds; it does
not start another collector. Left-click the icon for current status. The
right-click menu opens the repository-local `reports` folder, the configured
log folder, or exits the collector cleanly.

The internal command and executable remain `worktracker` and `worktracker.exe`
for compatibility; the product displayed to users is WorkChronicle.

Stop `src/context_watcher.py` before starting `worktracker run`; the two collectors must not write to the same context bucket concurrently.

## Browser context ingestion

When enabled, `worktracker run` listens only on `127.0.0.1` at the configured `browser_ingest.port` (default `5601`). Browser observations are accepted at `POST /api/v1/browser/observations` and stored in the dedicated `aw-watcher-browser-context_<hostname>` ActivityWatch bucket. The endpoint requires `Content-Type: application/json` and schema version 1.

The schema records `browser`, `tab_id`, `active`, `visible`, `url`, `domain`, `title`, `observed_at`, and a `media` object containing `present`, `state`, `type`, and `audible`. The extension in `browser-extension/` emits that raw schema; the Go backend remains responsible for deriving generic passive-work evidence.

### Build and test the browser extension

The checked-in source is shared by Firefox, Chrome, and Edge. Run:

```powershell
.\browser-extension\test.ps1
.\browser-extension\build.ps1
```

This creates unpacked `browser-extension\dist\chromium` and `browser-extension\dist\firefox` directories plus ZIP archives. Generated packages are ignored by Git. Chrome and Edge use the same Manifest V3 package; Firefox uses a small Manifest V2 variant so it can load the same background scripts using Firefox's supported background model.

The extension permissions are limited to `tabs`, HTTP/HTTPS page content-script access, and `http://127.0.0.1/*`. WebExtension host match patterns cannot express a port, so the permission covers loopback HTTP ports; the extension code itself posts only to the fixed `127.0.0.1:5601` endpoint. Page access is necessary to observe standard HTML `<video>` and `<audio>` elements. The extension does not request or collect screenshots, camera/microphone input, page text, forms, cookies, passwords, downloads, browsing keystrokes, or LAN access.

### Load manually

Start `workchronicle-agent.exe` first for the Docker architecture. Do not run
the legacy native or Python collectors concurrently.

- Firefox: open `about:debugging#/runtime/this-firefox`, choose **Load Temporary Add-on**, and select `browser-extension\dist\firefox\manifest.json`. Temporary add-ons must be reloaded after Firefox restarts unless the package is signed and installed through normal Firefox policy.
- Chrome: open `chrome://extensions`, enable **Developer mode**, choose **Load unpacked**, and select `browser-extension\dist\chromium`.
- Edge: open `edge://extensions`, enable **Developer mode**, choose **Load unpacked**, and select `browser-extension\dist\chromium`.

Firefox temporary add-ons do not survive a Firefox restart. Chrome and Edge
normally retain unpacked development extensions, subject to enterprise browser
policy. Production distribution should use signed/published packages or managed
enterprise deployment rather than developer loading.

The extension sends meaningful changes immediately and heartbeats active tabs and tabs containing media every five seconds. Background playing media stays represented even when its tab is inactive or its page is hidden. Navigation and tab closure emit terminal no-media observations when prior tab state is known.

### Automated browser integration tests

With exactly one Host Agent running, Docker Core healthy, and no legacy/Python
collector, build the packages and run the isolated-profile integration suite:

```powershell
.\browser-extension\build.ps1
.\browser-extension\integration\run.ps1
```

The suite uses loopback-only test pages and temporary browser profiles. It never
installs the extension into a normal browser profile. Chrome and Edge load the
unpacked Manifest V3 directory; Firefox installs the generated package
temporarily through its loopback WebDriver BiDi test session. The suite queries
the dedicated ActivityWatch browser bucket, validates Docker Core `status`
evidence, and briefly restarts the Host Agent to verify endpoint
failure/recovery. It leaves one Host Agent running afterward.

`install` and `uninstall` manage a per-user Task Scheduler entry. Installation fails without bypassing Windows or company policy if task registration is blocked. Do not install the task until live console validation is complete.
