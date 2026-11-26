#!/usr/bin/env bash
# KaraokeDock setup for Linux. Usage: scripts/setup.sh [cpu|cuda] [--with-duet-lyrics]
set -euo pipefail
profile="${1:-cpu}"
root="$(cd "$(dirname "$0")/.." && pwd)"
metadata="$root/data/metadata"

for tool in go uv ffmpeg deno yt-dlp pnpm; do
  command -v "$tool" >/dev/null || { echo "Missing $tool. Install it, then run this again."; exit 1; }
done

echo "Installing AI libraries..."
cd "$root/sidecar"
UV_PYTHON_INSTALL_DIR="$PWD/.python" uv sync --extra "$profile" --python 3.12 --managed-python
echo "Downloading AI models..."
uv run --no-sync python -m karaokedock_ml.fetch_models "$metadata/models"

if [[ " $* " == *" --with-duet-lyrics "* && ! -d "$metadata/amll" ]]; then
  echo "Downloading duet lyrics..."
  git clone --depth 1 https://github.com/amll-dev/amll-ttml-db "$metadata/amll"
fi

echo "Building the app..."
cd "$root/web"
pnpm install --frozen-lockfile
pnpm build
cd "$root/server"
mkdir -p "$root/bin"
go build -trimpath -ldflags '-s -w' -o "$root/bin/karaokedock" ./cmd/karaokedock
echo "Done. Start with: scripts/start.sh"
