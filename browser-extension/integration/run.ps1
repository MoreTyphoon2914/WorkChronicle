$ErrorActionPreference = "Stop"

$extensionRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$repoRoot = Split-Path -Parent $extensionRoot
$python = Join-Path $repoRoot ".venv\Scripts\python.exe"

if (-not (Test-Path -LiteralPath $python)) {
    throw "Repository Python environment not found at $python"
}

$agents = @(Get-CimInstance Win32_Process | Where-Object {
    $_.Name -eq "workchronicle-agent.exe" -and
    $_.ExecutablePath -eq (Join-Path $repoRoot "bin\workchronicle-agent.exe")
})
if ($agents.Count -ne 1) {
    throw "Expected exactly one repository WorkChronicle Host Agent; found $($agents.Count)"
}

& $python (Join-Path $extensionRoot "integration\run.py") `
    --repo $repoRoot `
    --agent-pid $agents[0].ProcessId `
    --agent (Join-Path $repoRoot "bin\workchronicle-agent.exe") `
    --token-file (Join-Path $repoRoot "secrets\agent-token.txt")
if ($LASTEXITCODE -ne 0) {
    throw "Browser integration tests failed with exit code $LASTEXITCODE"
}
