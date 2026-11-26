#!/usr/bin/env bash
set -euo pipefail

METADATA_DIR="${KARAOKEDOCK_METADATA:-/data/metadata}"
SIDECAR_DIR="${KARAOKEDOCK_SIDECAR:-/opt/karaokedock/sidecar}"
MODELS_DIR="${METADATA_DIR}/models"

mkdir -p "${METADATA_DIR}" "${MODELS_DIR}"

BUILT_IN_MODELS="/opt/karaokedock/models"

# Put the AI models in the data volume on first start: copied from the image,
# or downloaded if the image doesn't have them.
if [[ ! -f "${MODELS_DIR}/Kim_Vocal_2.onnx" || ! -d "${MODELS_DIR}/whisper" ]]; then
  if [[ -f "${BUILT_IN_MODELS}/Kim_Vocal_2.onnx" ]]; then
    echo "First start: copying the AI models into ${MODELS_DIR}..."
    cp -a "${BUILT_IN_MODELS}/." "${MODELS_DIR}/"
  else
    echo "First start: downloading the AI models..."
    (cd "${SIDECAR_DIR}" && uv run --no-sync python -m karaokedock_ml.fetch_models "${MODELS_DIR}")
  fi
fi

exec /usr/local/bin/karaokedock "$@"
