"""Times known lyrics to the vocals with stable-ts."""

from __future__ import annotations

import json
import os
from typing import Any, Callable, Iterable, Sequence

from . import runtime


def load_whisper(name: str, model_dir: str, device: str) -> Any:
    import stable_whisper

    return runtime.get_model(
        f"whisper:{name}:{device}",
        lambda: stable_whisper.load_model(name, device=device, download_root=os.path.join(model_dir, "whisper")),
    )


def _letters(s: str) -> int:
    return sum(1 for c in s if not c.isspace())


def map_words(lines: list[str], words: Iterable[dict[str, Any]]) -> list[list[dict[str, Any]]]:
    """Hands timed words back to their lyric lines by counting characters."""
    pending = list(words)
    out: list[list[dict[str, Any]]] = []
    i = 0
    for line in lines:
        need = _letters(line)
        got = 0
        row: list[dict[str, Any]] = []
        while i < len(pending) and got < need:
            row.append(pending[i])
            got += _letters(pending[i]["text"])
            i += 1
        out.append(row)
    return out


def word_rows(result: Any) -> tuple[list[dict[str, Any]], float]:
    """Flattens a stable-ts result, and returns its mean word confidence."""
    rows: list[dict[str, Any]] = []
    probs: list[float] = []
    for seg in result.segments:
        for w in seg.words:
            text = w.word.strip()
            if not text:
                continue
            rows.append({"text": text, "startMs": int(round(w.start * 1000)), "endMs": int(round(w.end * 1000))})
            if w.probability is not None:
                probs.append(float(w.probability))
    return rows, (sum(probs) / len(probs) if probs else 0.5)


def loudest_start(energy: Sequence[float], width: int) -> int:
    """Index where the loudest run of `width` values begins."""
    if len(energy) <= width:
        return 0
    total = best = sum(energy[:width])
    start = 0
    for i in range(1, len(energy) - width + 1):
        total += energy[i + width - 1] - energy[i - 1]
        if total > best:
            best, start = total, i
    return start


def detect_language(model: Any, path: str) -> str:
    """Finds the sung language from the part of the vocals with the most singing."""
    import numpy as np
    import whisper

    rate = whisper.audio.SAMPLE_RATE
    audio = whisper.load_audio(path)
    energy = [float(np.mean(audio[i : i + rate] ** 2)) for i in range(0, len(audio), rate)]
    start = loudest_start(energy, 30) * rate
    clip = whisper.pad_or_trim(audio[start : start + 30 * rate])
    mel = whisper.log_mel_spectrogram(clip, model.dims.n_mels).to(model.device)
    _, probs = model.detect_language(mel)
    return max(probs, key=probs.get)


def run(req: dict[str, Any], progress: Callable[[float], None]) -> dict[str, Any]:
    with open(req["lyrics"], encoding="utf-8") as f:
        lines: list[str] = json.load(f)["lines"]
    device = runtime.resolve_torch_device(req.get("device") or "auto")
    runtime.apply_threads(int(req.get("threads") or 0))
    progress(2)
    model = load_whisper(req.get("model") or "base", req["modelDir"], device)
    progress(10)
    language = req.get("language") or detect_language(model, req["input"])
    result = model.align(req["input"], "\n".join(lines), language=language, original_split=True, verbose=None)
    rows, confidence = word_rows(result)
    mapped = map_words(lines, rows)
    with open(req["output"], "w", encoding="utf-8") as f:
        json.dump({"lines": [{"words": r} for r in mapped]}, f, ensure_ascii=False)
    progress(100)
    return {"files": {"timed": req["output"]}, "confidence": round(confidence, 3)}
