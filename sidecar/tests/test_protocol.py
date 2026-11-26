import io
import json

from karaokedock_ml import protocol


def run(lines, monkeypatch=None):
    out = io.StringIO()
    protocol.serve(io.StringIO("\n".join(json.dumps(x) if isinstance(x, dict) else x for x in lines) + "\n"), out)
    return [json.loads(x) for x in out.getvalue().splitlines()]


def test_ping_reports_protocol():
    ev = run([{"id": "a", "op": "ping"}])[0]
    assert ev["id"] == "a" and ev["event"] == "done" and ev["protocol"] == protocol.PROTOCOL_VERSION
    assert "cpu" in ev["devices"]


def test_unknown_op_is_an_error():
    ev = run([{"id": "a", "op": "nope"}])[0]
    assert ev["event"] == "error" and ev["code"] == "failed"


def test_bad_line_is_skipped_and_loop_continues():
    evs = run(["not json", {"id": "b", "op": "ping"}])
    assert len(evs) == 1 and evs[0]["id"] == "b"


def test_shutdown_stops_the_loop():
    evs = run([{"id": "x", "op": "shutdown"}, {"id": "y", "op": "ping"}])
    assert [e["id"] for e in evs] == ["x"]


def test_gpu_oom_detection():
    assert protocol.is_gpu_oom(RuntimeError("CUDA out of memory. Tried to allocate 2 GiB"))
    assert not protocol.is_gpu_oom(RuntimeError("file not found"))


def test_progress_and_oom_events(monkeypatch):
    def fake(req, progress):
        progress(50)
        raise RuntimeError("CUDA out of memory")

    monkeypatch.setattr(protocol, "dispatch", fake)
    evs = run([{"id": "a", "op": "separate"}])
    assert evs[0] == {"id": "a", "event": "progress", "percent": 50}
    assert evs[1]["event"] == "error" and evs[1]["code"] == "gpu_oom"
