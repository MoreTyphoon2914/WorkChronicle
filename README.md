# WorkChronicle

Windows work-time tracker backed by ActivityWatch. The Go implementation keeps the Python proof of concept in `src/` unchanged and reads both its legacy context events and the versioned Go context schema.

## Configuration

Copy `config.example.json` to the ignored `config.json` and replace local-only values there. Never commit passwords or physical-network fingerprints. Explicit window/AFK bucket IDs are optional; WorkChronicle otherwise discovers a unique host-matching bucket.

The `work_targets` block configures fixed expectations: `daily_target_hours`, `daily_standard_minimum_hours`, `workdays_per_week`, and `overtime_rule`. Weekly values are derived by multiplying the daily values by the configured workdays. Older `target_hours` and `work_policy` keys remain read-compatible aliases. Work evaluations are calculated from classified `WORKING` totals at report time and are never stored in ActivityWatch.

JSON reports expose canonical `work_evaluation` and `week_to_date_evaluation` objects. The earlier `work_policy` and `week_to_date_policy` names are emitted as temporary compatibility aliases with identical values.

## Build and checks

```powershell
go test ./...
go vet ./...
go build ./...
go build -o .\bin\worktracker.exe .\cmd\worktracker
```

## Commands

```powershell
.\bin\worktracker.exe doctor --config .\config.json
.\bin\worktracker.exe status --config .\config.json
.\bin\worktracker.exe today --config .\config.json
.\bin\worktracker.exe week --config .\config.json --json
.\bin\worktracker.exe run --config .\config.json
```

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

Start `worktracker run` first. Do not run the Python context collector concurrently.

- Firefox: open `about:debugging#/runtime/this-firefox`, choose **Load Temporary Add-on**, and select `browser-extension\dist\firefox\manifest.json`. Temporary add-ons must be reloaded after Firefox restarts unless the package is signed and installed through normal Firefox policy.
- Chrome: open `chrome://extensions`, enable **Developer mode**, choose **Load unpacked**, and select `browser-extension\dist\chromium`.
- Edge: open `edge://extensions`, enable **Developer mode**, choose **Load unpacked**, and select `browser-extension\dist\chromium`.

The extension sends meaningful changes immediately and heartbeats active tabs and tabs containing media every five seconds. Background playing media stays represented even when its tab is inactive or its page is hidden. Navigation and tab closure emit terminal no-media observations when prior tab state is known.

### Automated browser integration tests

With exactly one Go collector running and no Python collector, build the packages and run the isolated-profile integration suite:

```powershell
.\browser-extension\build.ps1
.\browser-extension\integration\run.ps1
```

The suite uses loopback-only test pages and temporary headless browser profiles. It never installs the extension into a normal browser profile. Chrome and Edge load the unpacked Manifest V3 directory; Firefox installs the generated package temporarily through its loopback WebDriver BiDi test session. The suite queries the dedicated ActivityWatch browser bucket, validates current `status` evidence, and briefly restarts the existing console collector to verify endpoint failure/recovery. It leaves one Go collector running afterward.

`install` and `uninstall` manage a per-user Task Scheduler entry. Installation fails without bypassing Windows or company policy if task registration is blocked. Do not install the task until live console validation is complete.
