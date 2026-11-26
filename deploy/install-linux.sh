#!/usr/bin/env bash
# One-step installer & updater for Debian/Ubuntu Proxmox LXC and Raspberry Pi (64-bit).
# Usage:
#   curl -fsSL https://github.com/geejayjay/karaoke-dock/releases/latest/download/install-linux.sh | sudo bash
#   curl -fsSL .../install-linux.sh | sudo bash -s -- --with-duet-lyrics
set -euo pipefail

REPO="${REPO:-geejayjay/karaoke-dock}"
INSTALL_DIR="/opt/karaokedock"
DATA_DIR="/var/lib/karaokedock"
WITH_DUET=0

for arg in "$@"; do
  case "$arg" in
    --with-duet-lyrics) WITH_DUET=1 ;;
    --repo=*) REPO="${arg#*=}" ;;
  esac
done

if [[ "${EUID}" -ne 0 ]]; then
  echo "Please run as root (e.g. with sudo)." >&2
  exit 1
fi

uname_m="$(uname -m)"
case "${uname_m}" in
  x86_64|amd64)
    ARCH="amd64"
    YTDLP_BIN="yt-dlp_linux"
    DENO_ARCH="x86_64-unknown-linux-gnu"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    YTDLP_BIN="yt-dlp_linux_aarch64"
    DENO_ARCH="aarch64-unknown-linux-gnu"
    ;;
  *)
    echo "Unsupported architecture: ${uname_m} (need 64-bit x86_64 or arm64)." >&2
    exit 1
    ;;
esac

echo "==> Installing system packages (ffmpeg, curl, git, unzip, build-essential)..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends build-essential ca-certificates curl ffmpeg git libgomp1 libsndfile1 unzip

if ! command -v uv >/dev/null 2>&1; then
  echo "==> Installing uv..."
  UV_INSTALL_DIR=/usr/local/bin sh -c "$(curl -fsSL https://astral.sh/uv/install.sh)"
fi

echo "==> Installing yt-dlp (${ARCH})..."
curl -fsSL "https://github.com/yt-dlp/yt-dlp/releases/latest/download/${YTDLP_BIN}" -o /usr/local/bin/yt-dlp
chmod +x /usr/local/bin/yt-dlp

if ! command -v deno >/dev/null 2>&1; then
  echo "==> Installing Deno (${ARCH})..."
  tmp_deno="$(mktemp -d)"
  curl -fsSL "https://github.com/denoland/deno/releases/latest/download/deno-${DENO_ARCH}.zip" -o "${tmp_deno}/deno.zip"
  unzip -q "${tmp_deno}/deno.zip" -d /usr/local/bin
  chmod +x /usr/local/bin/deno
  rm -rf "${tmp_deno}"
fi

if ! id -u karaokedock >/dev/null 2>&1; then
  useradd --system --home-dir "${DATA_DIR}" --create-home --shell /usr/sbin/nologin karaokedock
fi

mkdir -p "${INSTALL_DIR}" "${DATA_DIR}/metadata/apks" "${DATA_DIR}/media" "${DATA_DIR}/tmp"

BASE_URL="https://github.com/${REPO}/releases/latest/download"
echo "==> Downloading KaraokeDock (${ARCH}) from ${BASE_URL}..."
tmp_tar="$(mktemp -d)"
curl -fsSL "${BASE_URL}/karaokedock-linux-${ARCH}.tar.gz" -o "${tmp_tar}/kd.tar.gz"
tar -xzf "${tmp_tar}/kd.tar.gz" -C "${INSTALL_DIR}"
rm -rf "${tmp_tar}"
chmod +x "${INSTALL_DIR}/bin/karaokedock"

# Optional: download Mobile & TV APKs so devices on Wi-Fi can sideload directly from the server.
for apk in karaokedock-mobile.apk karaokedock-tv.apk; do
  curl -fsSL "${BASE_URL}/${apk}" -o "${DATA_DIR}/metadata/apks/${apk}" 2>/dev/null || true
done

echo "==> Setting up Python AI sidecar & downloading models..."
cd "${INSTALL_DIR}/sidecar"
UV_PYTHON_INSTALL_DIR="${INSTALL_DIR}/sidecar/.python" uv sync --extra cpu --python 3.12 --managed-python --no-dev
uv run --no-sync python -m karaokedock_ml.fetch_models "${DATA_DIR}/metadata/models"

if [[ "${WITH_DUET}" -eq 1 && ! -d "${DATA_DIR}/metadata/amll" ]]; then
  echo "==> Downloading optional AMLL duet lyrics..."
  git clone --depth 1 https://github.com/amll-dev/amll-ttml-db "${DATA_DIR}/metadata/amll"
fi

chown -R karaokedock:karaokedock "${INSTALL_DIR}" "${DATA_DIR}"

cp "${INSTALL_DIR}/deploy/karaokedock.service" /etc/systemd/system/karaokedock.service
systemctl daemon-reload
systemctl enable --now karaokedock.service
systemctl restart karaokedock.service

LAN_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
LAN_IP="${LAN_IP:-localhost}"

echo ""
echo "=========================================================="
echo " KaraokeDock is installed and running!"
echo "   TV Screen:  http://${LAN_IP}:7464/tv"
echo "   Admin:      http://${LAN_IP}:7464/admin"
echo "   Mobile APK: http://${LAN_IP}:7464/downloads/karaokedock-mobile.apk"
echo "   TV APK:     http://${LAN_IP}:7464/downloads/karaokedock-tv.apk"
echo "=========================================================="
