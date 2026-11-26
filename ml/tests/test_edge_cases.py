"""Failure-mode and boundary tests: bad sources, reconnects, thresholds, storms, privacy, bus outages."""

from __future__ import annotations

import json
import time
from pathlib import Path
from typing import Any

import cv2
import fakeredis
import numpy as np
import pytest
import redis

from tests.conftest import InlineExecutor, stream_events
from visionguard_ml.bus import EventBus
from visionguard_ml.config import Settings
from visionguard_ml.describe import FakeDescriber, ResilientDescriber
from visionguard_ml.detectors import ModelCatalog
from visionguard_ml.geometry import point_in_polygon
from visionguard_ml.manager import PipelineManager
from visionguard_ml.pipeline import CameraPipeline, PipelineDeps
from visionguard_ml.rules.engine import RuleEngine
from visionguard_ml.samples import WAREHOUSE, SceneRenderer
from visionguard_ml.sources import FileSource, SourceError, StreamSource, _redact, open_source
from visionguard_ml.tracking.bytetrack import ByteTracker
from visionguard_ml.types import CameraConfig, Detection, Rule, TrackState, Zone

W = H = 100


def _t(tid: int, x: float, y: float, cls: str = "person") -> TrackState:
    return TrackState(tid, cls, 0.9, x - 2, y - 10, x + 2, y)


def _engine(*rules: Rule, zones: tuple[Zone, ...] = ()) -> RuleEngine:
    return RuleEngine(CameraConfig("c", "Cam", "file", "x", zones=zones, rules=rules))


def _deps(bus: EventBus, settings: Settings) -> PipelineDeps:
    return PipelineDeps(
        bus=bus,
        detector_factory=ModelCatalog(settings.models_dir).create_or_fallback,
        describer=ResilientDescriber(FakeDescriber()),
        executor=InlineExecutor(),
        snapshots_dir=settings.snapshots_dir,
        clips_dir=settings.clips_dir,
        video_dir=settings.video_dir,
        clip_pre_seconds=0.5,
        clip_post_seconds=0.2,
    )


# --------------------------------------------------------------------------- sources


def test_unreachable_rtsp_fails_fast() -> None:
    cam = CameraConfig("c", "c", "rtsp", "rtsp://127.0.0.1:9/nothing-listens-here")
    start = time.monotonic()
    with pytest.raises(SourceError):
        open_source(cam, Path("."))
    assert time.monotonic() - start < 10


@pytest.mark.parametrize("uri", ["not-a-url", "ftp://host/x", "file:///etc/passwd", ""])
def test_invalid_rtsp_urls_rejected(uri: str) -> None:
    with pytest.raises(SourceError):
        open_source(CameraConfig("c", "c", "rtsp", uri), Path("."))


def test_rtsp_credentials_are_redacted() -> None:
    assert _redact("rtsp://admin:hunter2@10.0.0.5:554/s") == "rtsp://***@10.0.0.5:554/s"
    assert _redact(0) == "0"


def test_zero_length_corrupt_and_unsupported_files(tmp_path: Path) -> None:
    empty = tmp_path / "empty.mp4"
    empty.write_bytes(b"")
    corrupt = tmp_path / "corrupt.mp4"
    corrupt.write_bytes(b"\x00\x00\x00\x18ftypmp42" + b"\xde\xad\xbe\xef" * 500)
    text = tmp_path / "notes.avi"
    text.write_text("this is not a video")
    for path in (empty, corrupt, text):
        with pytest.raises(SourceError):
            FileSource(path, 10)


class _FakeCapture:
    """Scripted cv2.VideoCapture stand-in: yields `frames` reads then fails (disconnect)."""

    def __init__(self, frames: int, opened: bool = True) -> None:
        self.frames, self.opened, self.reads = frames, opened, 0

    def isOpened(self) -> bool:  # noqa: N802 - mirrors cv2 API
        return self.opened

    def set(self, *_: Any) -> bool:
        return True

    def read(self) -> tuple[bool, np.ndarray | None]:
        self.reads += 1
        if self.reads > self.frames:
            time.sleep(0.01)
            return False, None
        time.sleep(0.005)
        return True, np.full((48, 64, 3), self.reads % 255, np.uint8)

    def release(self) -> None:
        self.opened = False


def test_stream_disconnect_and_reconnect() -> None:
    attempts: list[int] = []

    def factory(_uri: str | int) -> _FakeCapture:
        attempts.append(1)
        if len(attempts) == 2:
            return _FakeCapture(0, opened=False)  # first reconnect attempt: camera still down
        return _FakeCapture(frames=5)

    src = StreamSource("rtsp://cam/1", 10, reconnect_delay=0.01, read_timeout=1.0, capture_factory=factory)
    try:
        got = [src.read() for _ in range(3)]
        assert all(f is not None for f in got)
        deadline = time.monotonic() + 5
        while src.reconnects < 1 and time.monotonic() < deadline:
            time.sleep(0.01)
        assert src.reconnects >= 1 and len(attempts) >= 3
        assert src.read() is not None  # frames flow again after reconnect
    finally:
        src.close()


def test_stream_read_times_out_when_camera_silent() -> None:
    src = StreamSource(
        "rtsp://cam/2",
        10,
        reconnect_delay=5,
        read_timeout=0.2,
        capture_factory=lambda _u: _FakeCapture(frames=0),
    )
    try:
        assert src.read() is None
    finally:
        src.close()


def test_pipeline_recovers_after_source_appears(bus: EventBus, settings: Settings) -> None:
    cam = CameraConfig("late", "Late", "file", "samples/late.mp4", target_fps=10)
    pipe = CameraPipeline(cam, "color-synthetic", _deps(bus, settings))
    pipe.start()
    try:
        deadline = time.time() + 20
        while pipe.state != "error" and time.time() < deadline:
            time.sleep(0.02)
        assert pipe.state == "error"
        from visionguard_ml.samples import STREET, write_scene

        write_scene(STREET, settings.video_dir / "samples" / "late.mp4")
        deadline = time.time() + 20
        while pipe.frame_idx == 0 and time.time() < deadline:
            time.sleep(0.05)
        assert pipe.state == "online" and pipe.frame_idx > 0
    finally:
        pipe.stop()


def test_very_high_resolution_frames_are_downscaled(
    bus: EventBus, settings: Settings, redis_client: fakeredis.FakeRedis
) -> None:
    pipe = CameraPipeline(CameraConfig("hr", "8K", "file", "x"), "color-synthetic", _deps(bus, settings))
    base = SceneRenderer(WAREHOUSE).render(12.0)
    huge = cv2.resize(base, (7680, 4320), interpolation=cv2.INTER_NEAREST)
    tracks, _ = pipe.process_frame(huge, 1.0)
    frame_msg = json.loads(redis_client.xrange("vg:frames")[-1][1][b"data"])
    assert (frame_msg["width"], frame_msg["height"]) == (1920, 1080)
    assert len([t for t in tracks if t.cls == "person"]) == SceneRenderer(WAREHOUSE).visible(12.0, "person")


# --------------------------------------------------------------------------- zones & thresholds


def test_degenerate_and_off_frame_polygons_never_match() -> None:
    line_like = ((0.1, 0.1), (0.5, 0.5), (0.9, 0.9))
    two_points = ((0.1, 0.1), (0.9, 0.9))
    off_frame = ((1.2, 1.2), (1.5, 1.2), (1.5, 1.5))
    for poly in (line_like, two_points, off_frame):
        assert not point_in_polygon((0.5, 0.5), poly)
    eng = _engine(
        Rule("r", "R", "zone_intrusion", "z", cooldown_seconds=0),
        zones=(Zone("z", "Flat", "polygon", line_like),),
    )
    assert eng.process([_t(1, 50, 50)], W, H, 0)[0] == []


def test_self_intersecting_polygon_uses_even_odd_rule() -> None:
    bowtie = ((0.0, 0.0), (1.0, 1.0), (1.0, 0.0), (0.0, 1.0))
    assert point_in_polygon((0.9, 0.5), bowtie)
    assert not point_in_polygon((0.5, 0.1), bowtie)


def test_crowd_exactly_at_threshold_does_not_fire() -> None:
    eng = _engine(Rule("r", "Crowd", "crowd_count", None, max_count=3, cooldown_seconds=0))
    assert eng.process([_t(i, 10 + i * 10, 50) for i in range(3)], W, H, 0)[0] == []
    assert len(eng.process([_t(i, 10 + i * 10, 50) for i in range(4)], W, H, 1)[0]) == 1


def test_dwell_fires_exactly_at_threshold() -> None:
    eng = _engine(Rule("r", "Dwell", "dwell_time", None, seconds=120, classes=("car",)))
    fired_at = None
    for sec in range(0, 125):
        alerts, _ = eng.process([_t(1, 50, 50, "car")], W, H, float(sec))
        if alerts and fired_at is None:
            fired_at = sec
    assert fired_at == 120


def test_alert_storm_is_suppressed_by_cooldown() -> None:
    eng = _engine(Rule("r", "Intrusion", "zone_intrusion", None, cooldown_seconds=30))
    total = 0
    for frame in range(100):  # 100 different people pouring in over 10 s
        alerts, _ = eng.process([_t(frame, 50, 50)], W, H, frame * 0.1)
        total += len(alerts)
    assert total == 1


def test_tracker_id_churn_does_not_cause_duplicate_alerts() -> None:
    eng = _engine(Rule("r", "Intrusion", "zone_intrusion", None, cooldown_seconds=20))
    alerts = []
    for i in range(50):  # same person, new track id every 5 frames (detector flicker)
        alerts += eng.process([_t(i // 5, 50, 50)], W, H, i * 0.1)[0]
    assert len(alerts) == 1


def test_bytetrack_ids_stable_under_jitter_and_dropouts() -> None:
    rng = np.random.default_rng(3)
    tracker = ByteTracker()
    ids: set[int] = set()
    for i in range(60):
        if i % 7 == 3:  # periodic missed detection
            tracker.update([])
            continue
        x = 100 + i * 3 + rng.normal(0, 1.5)
        conf = 0.9 if i % 5 else 0.35  # periodic low-confidence frame
        tracks = tracker.update([Detection(x, 50, x + 20, 90, conf, "person")])
        ids.update(t.track_id for t in tracks)
    assert len(ids) == 1


# --------------------------------------------------------------------------- privacy & backpressure


def test_stored_snapshots_are_face_blurred(
    bus: EventBus, settings: Settings, redis_client: fakeredis.FakeRedis
) -> None:
    zone = Zone("z", "All", "polygon", ((0, 0), (1, 0), (1, 1), (0, 1)))
    rule = Rule("r", "Any person", "zone_intrusion", "z", cooldown_seconds=0)
    frame = SceneRenderer(WAREHOUSE).render(12.0)

    def head_sharpness(private: bool) -> float:
        cam = CameraConfig(
            f"p{private}", "P", "file", "x", privacy_mode=private, zones=(zone,), rules=(rule,)
        )
        pipe = CameraPipeline(cam, "color-synthetic", _deps(bus, settings))
        tracks, alerts = pipe.process_frame(frame, 1.0)
        assert alerts
        snap = cv2.imread(str(settings.snapshots_dir / f"{alerts[0].incident_id}.jpg"))
        person = next(t for t in tracks if t.cls == "person")
        y1 = int(person.y1) + 3  # skip the drawn box border
        head = snap[y1 : y1 + 8, int(person.x1) + 3 : int(person.x2) - 3]
        return float(cv2.Laplacian(cv2.cvtColor(head, cv2.COLOR_BGR2GRAY), cv2.CV_64F).var())

    sharp, blurred = head_sharpness(False), head_sharpness(True)
    assert blurred < sharp * 0.5, (sharp, blurred)
    alert = next(d for t, d in stream_events(redis_client) if t == "alert" and d["privacy_applied"])
    assert alert["privacy_applied"] is True


class _SlowDetector:
    model_id = "slow"
    classes = ("person",)

    def detect(self, frame: Any) -> list[Detection]:
        time.sleep(0.05)
        return []


def test_slow_inference_drops_frames_instead_of_queueing(bus: EventBus, settings: Settings) -> None:
    class _FastSource:
        frame_interval = 0.01  # source wants 100 fps; detector manages ~20

        def __init__(self) -> None:
            self.reads = 0

        def read(self) -> np.ndarray:
            self.reads += 1
            return np.zeros((36, 64, 3), np.uint8)

        def close(self) -> None: ...

    deps = _deps(bus, settings)
    deps.detector_factory = lambda _m: _SlowDetector()
    pipe = CameraPipeline(CameraConfig("s", "Slow", "file", "x"), "slow", deps)
    src = _FastSource()
    import threading

    t = threading.Thread(target=pipe._loop, args=(src,), daemon=True)
    t.start()
    time.sleep(1.0)
    pipe._stop.set()
    t.join(2)
    # Processed at detector speed (~20 fps), far below the 100 fps source, and every frame that
    # was read was processed immediately: nothing queued behind the slow detector.
    assert 1 <= pipe.frame_idx <= 25
    assert src.reads - pipe.frame_idx <= 1
    assert pipe.latency_ms >= 45


def test_redis_outage_does_not_stop_pipeline(settings: Settings) -> None:
    dead = EventBus(redis.Redis(host="127.0.0.1", port=1, socket_connect_timeout=0.1, socket_timeout=0.1))
    assert dead.ping() is False
    pipe = CameraPipeline(CameraConfig("d", "D", "file", "x"), "color-synthetic", _deps(dead, settings))
    for i in range(5):
        pipe.process_frame(SceneRenderer(WAREHOUSE).render(i), float(i))
    assert pipe.frame_idx == 5 and dead.read_config() is None


def test_bus_restart_keeps_pipelines_and_resumes(
    settings: Settings, redis_client: fakeredis.FakeRedis
) -> None:
    from visionguard_ml.samples import write_scene

    write_scene(WAREHOUSE, settings.video_dir / "samples" / "warehouse.mp4")
    bus = EventBus(redis_client)
    mgr = PipelineManager(
        settings, bus, ModelCatalog(settings.models_dir), ResilientDescriber(FakeDescriber())
    )
    doc = {
        "version": 1,
        "active_model": "color-synthetic",
        "cameras": [
            {"id": "cam-1", "name": "W", "source_type": "file", "source_uri": "samples/warehouse.mp4"}
        ],
    }
    redis_client.set("vg:config", json.dumps(doc))
    try:
        assert mgr.poll_once()
        pipe = mgr.get("cam-1")
        redis_client.flushall()  # Redis restarted without persistence
        assert mgr.poll_once() is False
        assert mgr.get("cam-1") is pipe and pipe is not None and pipe.running
        redis_client.set("vg:config", json.dumps(doc))  # API re-publishes on its resync tick
        mgr.poll_once()
        assert mgr.get("cam-1") is pipe  # same version -> no restart
        # Poll on the observable outcome (not wall-clock guesses) so a loaded CI box can't flake it.
        deadline = time.time() + 20
        while redis_client.xlen("vg:frames") == 0 and time.time() < deadline:
            time.sleep(0.05)
        assert redis_client.xlen("vg:frames") > 0  # publishing resumed on the fresh Redis
    finally:
        mgr.stop()
