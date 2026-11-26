# Builds KaraokeDock for Windows (tray app + CLI) and packages KaraokeDock-Setup.exe with
# Inno Setup (see setup.iss), plus a portable zip.
param(
  [string]$Version = "",
  [string]$OutDir = "dist\windows"
)
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
if (-not $Version) {
  $Version = (git -C $root describe --tags --always 2>$null)
  if (-not $Version) { $Version = "dev" }
}
$stage = Join-Path $root "$OutDir\stage"
$zipOut = Join-Path $root "$OutDir\karaokedock-windows-amd64.zip"

# Shown in Windows' installed apps list, e.g. v0.5.0-beta -> 0.5.0-beta.
$appVer = $Version -replace '^v', ''

if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force -Path "$stage\bin", "$stage\sidecar" | Out-Null

# 1. Build Windows .ico from server/internal/api/icons/icon-192.png (PNG-in-ICO format)
$pngBytes = [System.IO.File]::ReadAllBytes((Join-Path $root 'server\internal\api\icons\icon-192.png'))
$icoPath = Join-Path $stage 'karaokedock.ico'
$ms = New-Object System.IO.MemoryStream
$bw = New-Object System.IO.BinaryWriter($ms)
$bw.Write([uint16]0); $bw.Write([uint16]1); $bw.Write([uint16]1)
$bw.Write([byte]192); $bw.Write([byte]192); $bw.Write([byte]0); $bw.Write([byte]0)
$bw.Write([uint16]1); $bw.Write([uint16]32)
$bw.Write([uint32]$pngBytes.Length); $bw.Write([uint32]22)
$bw.Write($pngBytes)
$bw.Flush()
[System.IO.File]::WriteAllBytes($icoPath, $ms.ToArray())
$bw.Close()

# 2. Build Tray GUI executable (default app, opens Admin) & CLI executable
Push-Location (Join-Path $root 'server')
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags "-s -w -H=windowsgui -X main.defaultTray=true -X main.version=$Version" -o (Join-Path $stage 'KaraokeDock.exe') ./cmd/karaokedock
if ($LASTEXITCODE) { throw 'Tray build failed' }
go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $stage 'bin\karaokedock.exe') ./cmd/karaokedock
if ($LASTEXITCODE) { throw 'CLI build failed' }
Pop-Location

# 3. Copy sidecar source files
Copy-Item (Join-Path $root 'sidecar\pyproject.toml'), (Join-Path $root 'sidecar\uv.lock') -Destination "$stage\sidecar\"
Copy-Item -Recurse (Join-Path $root 'sidecar\karaokedock_ml') -Destination "$stage\sidecar\karaokedock_ml"
Get-ChildItem -Path "$stage\sidecar" -Recurse -Directory -Filter '__pycache__' | Remove-Item -Recurse -Force

# 4. Create the portable zip
if (Test-Path $zipOut) { Remove-Item -Force $zipOut }
Compress-Archive -Path "$stage\*" -DestinationPath $zipOut

# 5. Build the installer with Inno Setup
$iscc = (Get-Command iscc -ErrorAction SilentlyContinue).Source
if (-not $iscc) {
    $iscc = @("${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe", "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe") |
        Where-Object { Test-Path $_ } | Select-Object -First 1
}
if (-not $iscc -or -not (Test-Path $iscc)) { throw 'Inno Setup 6 is needed: winget install JRSoftware.InnoSetup' }
& $iscc "/DAppVersion=$appVer" "/DStage=$stage" "/DOutDir=$(Join-Path $root $OutDir)" (Join-Path $PSScriptRoot 'setup.iss')
if ($LASTEXITCODE) { throw 'Installer build failed' }
Write-Host "Built $(Join-Path $root "$OutDir\KaraokeDock-Setup.exe") and $zipOut"
