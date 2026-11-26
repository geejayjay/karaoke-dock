"""JSON Lines loop between the Go server and the AI code.

Only file paths cross this boundary. Libraries print to stdout freely, so at
startup we keep a private handle to the real stdout for the protocol, and send
sys.stdout to stderr.
"""

from __future__ import annotations

import json
import os
import sys
import traceback
from typing import Any, Callable, TextIO

PROTOCOL_VERSION = 1

Emit = Callable[[dict[str, Any]], None]


def protect_stdout() -> TextIO:
    """Returns a private protocol stream and points fd 1 at stderr."""
    private = os.fdopen(os.dup(1), "w", buffering=1, encoding="utf-8", newline="\n")
    os.dup2(2, 1)
    sys.stdout = sys.stderr
    return private


def available_devices() -> list[str]:
    devices = ["cpu"]
    try:
        import torch

        if torch.cuda.is_available():
            devices.append("cuda")
    except ImportError:
        pass
    try:
        import onnxruntime as ort

        if "OpenVINOExecutionProvider" in ort.get_available_providers():
            devices.append("openvino")
    except ImportError:
        pass
    return devices


def is_gpu_oom(exc: BaseException) -> bool:
    text = f"{type(exc).__name__}: {exc}".lower()
    return "out of memory" in text and ("cuda" in text or "cublas" in text or "onnxruntime" in text)


def dispatch(req: dict[str, Any], progress: Callable[[float], None]) -> dict[str, Any]:
    """Runs one command and returns the fields of its done event."""
    op = req.get("op")
    # Imports stay inside so `ping` and `shutdown` are instant.
    if op == "ping":
        return {"protocol": PROTOCOL_VERSION, "devices": available_devices()}
    if op == "separate":
        from . import separate

        return separate.run(req, progress)
    if op == "vocal_level":
        from . import separate

        return separate.vocal_level(req, progress)
    if op == "align":
        from . import align

        return align.run(req, progress)
    if op == "transcribe":
        from . import transcribe

        return transcribe.run(req, progress)
    if op == "ocr":
        from . import ocr

        return ocr.run(req, progress)
    raise ValueError(f"unknown op {op!r}")


def serve(stdin: TextIO, out: TextIO) -> None:
    def emit(data: dict[str, Any]) -> None:
        out.write(json.dumps(data, ensure_ascii=False) + "\n")
        out.flush()

    for line in stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError:
            print(f"bad protocol line: {line!r}", file=sys.stderr)
            continue
        req_id = req.get("id", "")
        if req.get("op") == "shutdown":
            emit({"id": req_id, "event": "done"})
            return

        def progress(percent: float) -> None:
            emit({"id": req_id, "event": "progress", "percent": round(percent, 1)})

        try:
            result = dispatch(req, progress)
            emit({"id": req_id, "event": "done", **result})
        except BaseException as exc:  # noqa: BLE001 - every failure must reach Go
            if isinstance(exc, (KeyboardInterrupt, SystemExit)):
                raise
            traceback.print_exc(file=sys.stderr)
            code = "gpu_oom" if is_gpu_oom(exc) else "failed"
            emit({"id": req_id, "event": "error", "code": code, "message": str(exc)[:500]})


def main() -> None:
    out = protect_stdout()
    serve(sys.stdin, out)
