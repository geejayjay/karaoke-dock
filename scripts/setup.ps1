# KaraokeDock setup for Windows. Run from the project folder:
#   powershell -ExecutionPolicy Bypass -File scripts\setup.ps1 [-Profile cpu|cuda] [-WithDuetLyrics]
param(
  [ValidateSet('cpu', 'cuda')][string]$Profile = 'cpu',
  [switch]$WithDuetLyrics
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$metadata = Join-Path $root 'data\metadata'

foreach ($tool in 'go', 'uv', 'ffmpeg', 'deno', 'yt-dlp', 'pnpm') {
  if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "Missing $tool. Install it, then run this again." }
}

Write-Host 'Installing AI libraries...'
Push-Location (Join-Path $root 'sidecar')
# A private Python keeps this project independent of the rest of the machine.
$env:UV_PYTHON_INSTALL_DIR = Join-Path (Get-Location) '.python'
uv sync --extra $Profile --python 3.12 --managed-python
if ($LASTEXITCODE) { throw 'uv sync failed' }
Write-Host 'Downloading AI models...'
uv run --no-sync python -m karaokedock_ml.fetch_models (Join-Path $metadata 'models')
if ($LASTEXITCODE) { throw 'model download failed' }
Pop-Location

if ($WithDuetLyrics) {
  $amll = Join-Path $metadata 'amll'
  if (-not (Test-Path $amll)) {
    Write-Host 'Downloading duet lyrics...'
    git clone --depth 1 https://github.com/amll-dev/amll-ttml-db $amll
    if ($LASTEXITCODE) { throw 'duet lyric download failed' }
  }
}

Write-Host 'Building the app...'
Push-Location (Join-Path $root 'web')
pnpm install --frozen-lockfile
if ($LASTEXITCODE) { throw 'pnpm install failed' }
pnpm build
if ($LASTEXITCODE) { throw 'frontend build failed' }
Pop-Location

Push-Location (Join-Path $root 'server')
New-Item -ItemType Directory -Force (Join-Path $root 'bin') | Out-Null
go build -trimpath -ldflags '-s -w' -o (Join-Path $root 'bin\karaokedock.exe') ./cmd/karaokedock
if ($LASTEXITCODE) { throw 'server build failed' }
Pop-Location

Write-Host 'Done. Start with: scripts\start.ps1'
