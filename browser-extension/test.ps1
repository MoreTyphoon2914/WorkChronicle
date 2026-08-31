$ErrorActionPreference = "Stop"
$extensionRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$testOutput = & cscript.exe //nologo (Join-Path $extensionRoot "tests\core.test.js") 2>&1
$exitCode = $LASTEXITCODE
$testOutput | ForEach-Object { Write-Host $_ }
if ($exitCode -ne 0 -or $testOutput -notcontains "ALL TESTS PASSED") {
    throw "Browser extension unit tests failed with exit code $LASTEXITCODE"
}

$chromiumManifest = Get-Content (Join-Path $extensionRoot "manifests\manifest.chromium.json") -Raw | ConvertFrom-Json
$firefoxManifest = Get-Content (Join-Path $extensionRoot "manifests\manifest.firefox.json") -Raw | ConvertFrom-Json
foreach ($manifest in @($chromiumManifest, $firefoxManifest)) {
    if ($manifest.name -ne "WorkChronicle Browser Context") {
        throw "Extension display name is not branded as WorkChronicle"
    }
    if ($manifest.description -notmatch "WorkChronicle") {
        throw "Extension description is not branded as WorkChronicle"
    }
}
$loopbackPermission = "http://127.0.0.1/*"
if ($chromiumManifest.host_permissions.Count -ne 1 -or $chromiumManifest.host_permissions[0] -ne $loopbackPermission) {
    throw "Chromium manifest must grant only the valid loopback HTTP host pattern"
}
if ($firefoxManifest.permissions -notcontains $loopbackPermission) {
    throw "Firefox manifest is missing the valid loopback HTTP host pattern"
}
$background = Get-Content (Join-Path $extensionRoot "src\background.js") -Raw
if ($background -notmatch 'var endpoint = "http://127\.0\.0\.1:5601/api/v1/browser/observations";') {
    throw "Background sender is not pinned to the WorkChronicle loopback endpoint"
}
Write-Host "PASS manifest loopback permissions and fixed ingestion endpoint"
