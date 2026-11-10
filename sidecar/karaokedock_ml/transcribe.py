"""Writes lyrics from the vocals when no source has them."""

from __future__ import annotations

import json
from typing import Any, Callable

from . import align, runtime


def run(req: dict[str, Any], progress: Callable[[float], None]) -> dict[str, Any]:
    device = runtime.resolve_torch_device(req.get("device") or "auto")
    runtime.apply_threads(int(req.get("threads") or 0))
    progress(2)
    model = align.load_whisper(req.get("model") or "base", req["modelDir"], device)
    progress(10)
    result = model.transcribe(req["input"], language=req.get("language") or None, verbose=None)
    lines = []
    for seg in result.segments:
        words = [
            {"text": w.word.strip(), "startMs": int(round(w.start * 1000)), "endMs": int(round(w.end * 1000))}
            for w in seg.words
            if w.word.strip()
        ]
        if words:
            lines.append({"words": words})
    with open(req["output"], "w", encoding="utf-8") as f:
        json.dump({"lines": lines}, f, ensure_ascii=False)
    progress(100)
    return {"files": {"timed": req["output"]}}
