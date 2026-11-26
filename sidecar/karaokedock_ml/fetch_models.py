"""Downloads every model during setup, so the server never downloads at runtime.

Usage: python -m karaokedock_ml.fetch_models <model-dir> [separation-model] [whisper-model]
"""

from __future__ import annotations

import logging
import os
import sys
import tempfile


def main() -> None:
    model_dir = os.path.abspath(sys.argv[1])
    separation = sys.argv[2] if len(sys.argv) > 2 else "Kim_Vocal_2.onnx"
    whisper_name = sys.argv[3] if len(sys.argv) > 3 else "base"
    os.makedirs(model_dir, exist_ok=True)

    from audio_separator.separator import Separator

    print(f"Fetching vocal removal model {separation}")
    with tempfile.TemporaryDirectory() as tmp:
        sep = Separator(log_level=logging.WARNING, model_file_dir=model_dir, output_dir=tmp, output_format="WAV")
        sep.load_model(model_filename=separation)

    import stable_whisper

    print(f"Fetching Whisper model {whisper_name}")
    stable_whisper.load_model(whisper_name, device="cpu", download_root=os.path.join(model_dir, "whisper"))
    print("Models ready.")


if __name__ == "__main__":
    main()
