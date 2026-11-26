"""Vocal removal with audio-separator."""

from __future__ import annotations

import logging
import os
from typing import Any, Callable

from . import runtime


def _apply_device(sep: Any, device: str) -> None:
    import torch

    if device == "cpu":
        sep.torch_device = torch.device("cpu")
        sep.onnx_execution_provider = ["CPUExecutionProvider"]
    elif device == "openvino":
        import onnxruntime as ort

        if "OpenVINOExecutionProvider" not in ort.get_available_providers():
            raise RuntimeError("OpenVINO isn't installed for this build")
        sep.onnx_execution_provider = ["OpenVINOExecutionProvider"]


def run(req: dict[str, Any], progress: Callable[[float], None]) -> dict[str, Any]:
    out_dir = req["outDir"]
    model = req["model"]
    device = req.get("device") or "auto"
    runtime.apply_threads(int(req.get("threads") or 0))
    progress(2)
    sep = runtime.get_model(f"sep:{model}:{device}", lambda: _load_with_device(model, req["modelDir"], out_dir, device))
    # The model stays warm between songs, so point it at this song's folder.
    sep.output_dir = out_dir
    sep.model_instance.output_dir = out_dir
    progress(10)
    sep.separate(req["input"], {"Vocals": "vocals", "Instrumental": "instrumental"})
    vocals = os.path.join(out_dir, "vocals.wav")
    instrumental = os.path.join(out_dir, "instrumental.wav")
    for path in (vocals, instrumental):
        if not os.path.exists(path):
            raise FileNotFoundError(f"separator did not write {path}")
    progress(100)
    return {"files": {"vocals": vocals, "instrumental": instrumental}}


def level_db(vocals: Any, instrumental: Any) -> float:
    """How loud the vocals are against the whole mix, in dB."""
    import numpy as np

    n = min(len(vocals), len(instrumental))
    v = np.asarray(vocals[:n], dtype=np.float64)
    mix = v + np.asarray(instrumental[:n], dtype=np.float64)
    power = float(np.mean(mix**2))
    if power <= 0:
        return -120.0
    return 10 * float(np.log10(max(float(np.mean(v**2)), 1e-12) / power))


def vocal_level(req: dict[str, Any], progress: Callable[[float], None]) -> dict[str, Any]:
    """Separates a short sample, to tell whether a song has a lead vocal at all."""
    import soundfile

    files = run(req, progress)["files"]
    vocals, _ = soundfile.read(files["vocals"])
    instrumental, _ = soundfile.read(files["instrumental"])
    return {"vocalDb": round(level_db(vocals, instrumental), 1)}


def _load_with_device(model: str, model_dir: str, out_dir: str, device: str) -> Any:
    from audio_separator.separator import Separator

    sep = Separator(log_level=logging.WARNING, model_file_dir=model_dir, output_dir=out_dir, output_format="WAV")
    _apply_device(sep, device)
    sep.load_model(model_filename=model)
    return sep
