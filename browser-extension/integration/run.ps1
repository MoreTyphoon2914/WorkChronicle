$ErrorActionPreference = "Stop"

$extensionRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$repoRoot = Split-Path -Parent $extensionRoot
$python = Join-Path $repoRoot ".venv\Scripts\python.exe"

if (-not (Test-Path -LiteralPath $python)) {
    throw "Repository Python environment not found at $python"
}

$collectors = @(Get-CimInstance Win32_Process | Where-Object {
    $_.Name -eq "worktracker.exe" -and
    $_.CommandLine -match "\srun(?:\s|$)" -and
    $_.ExecutablePath -eq (Join-Path $repoRoot "bin\worktracker.exe")
})
if ($collectors.Count -ne 1) {
    throw "Expected exactly one repository WorkChronicle collector; found $($collectors.Count)"
}

& $python (Join-Path $extensionRoot "integration\run.py") --repo $repoRoot --collector-pid $collectors[0].ProcessId
if ($LASTEXITCODE -ne 0) {
    throw "Browser integration tests failed with exit code $LASTEXITCODE"
}
