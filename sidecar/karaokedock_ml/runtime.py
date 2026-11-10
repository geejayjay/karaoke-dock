"""Shared model and thread handling."""

from __future__ import annotations

import gc
from typing import Any, Callable

_active: tuple[str, Any] | None = None


def apply_threads(requested: int) -> None:
    """Uses physical cores minus 2 when `requested` is 0."""
    import psutil
    import torch

    n = requested
    if n <= 0:
        n = max((psutil.cpu_count(logical=False) or 2) - 2, 1)
    torch.set_num_threads(n)


def get_model(key: str, load: Callable[[], Any]) -> Any:
    """Keeps one model warm, and unloads only when switching to another."""
    global _active
    if _active is not None and _active[0] == key:
        return _active[1]
    release()
    model = load()
    _active = (key, model)
    return model


def release() -> None:
    global _active
    _active = None
    gc.collect()
    try:
        import torch

        if torch.cuda.is_available():
            torch.cuda.empty_cache()
    except ImportError:
        pass


def resolve_torch_device(device: str) -> str:
    import torch

    if device == "cuda" or (device in ("auto", "", None) and torch.cuda.is_available()):
        return "cuda"
    return "cpu"
