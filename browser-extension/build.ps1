$ErrorActionPreference = "Stop"

$extensionRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$distRoot = Join-Path $extensionRoot "dist"

function Build-Package {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Manifest
    )

    $packageRoot = Join-Path $distRoot $Name
    $sourceRoot = Join-Path $packageRoot "src"
    New-Item -ItemType Directory -Force -Path $sourceRoot | Out-Null
    Copy-Item -Force -Path (Join-Path $extensionRoot "src\*.js") -Destination $sourceRoot
    Copy-Item -Force -Path (Join-Path $extensionRoot "manifests\$Manifest") -Destination (Join-Path $packageRoot "manifest.json")

    $archive = Join-Path $distRoot "$Name.zip"
    if (Test-Path -LiteralPath $archive) {
        Remove-Item -LiteralPath $archive
    }
    Compress-Archive -Path (Join-Path $packageRoot "*") -DestinationPath $archive
}

New-Item -ItemType Directory -Force -Path $distRoot | Out-Null
Build-Package -Name "chromium" -Manifest "manifest.chromium.json"
Build-Package -Name "firefox" -Manifest "manifest.firefox.json"
Write-Host "Created unpacked packages and ZIP archives under $distRoot"
