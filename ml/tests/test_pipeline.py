from __future__ import annotations

import time
from pathlib import Path
from typing import Any

import fakeredis
import numpy as np
import pytest
from fastapi.testclient import TestClient

from tests.conftest import InlineExecutor, stream_events
from visionguard_ml.app import create_app
from visionguard_ml.bus import EventBus, parse_config
from visionguard_ml.config import Settings
from visionguard_ml.describe import FakeDescriber, ResilientDescriber
from visionguard_ml.detectors import ModelCatalog
from visionguard_ml.manager import PipelineManager
from visionguard_ml.media import BufferedFrame, FrameRingBuffer, write_clip
from visionguard_ml.pipeline import CameraPipeline, PipelineDeps
from visionguard_ml.privacy import FaceBlurrer
from visionguard_ml.samples import FPS, WAREHOUSE, SceneRenderer, write_scene
from visionguard_ml.sources import FileSource, SourceError, open_source, resolve_file
from visionguard_ml.types import CameraConfig, TrackState


def _deps(bus: EventBus, settings: Settings) -> PipelineDeps:
    catalog = ModelCatalog(settings.models_dir)
    return PipelineDeps(
        bus=bus,
        detector_factory=catalog.create_or_fallback,
        describer=ResilientDescriber(FakeDescriber()),
        executor=InlineExecutor(),
        snapshots_dir=settings.snapshots_dir,
        clips_dir=settings.clips_dir,
        video_dir=settings.video_dir,
        clip_pre_seconds=1.0,
        clip_post_seconds=1.0,
    )


def test_pipeline_process_frames_end_to_end(
    warehouse_camera: CameraConfig, bus: EventBus, redis_client: fakeredis.FakeRedis, settings: Settings
) -> None:
    pipe = CameraPipeline(warehouse_camera, "color-synthetic", _deps(bus, settings))
    renderer = SceneRenderer(WAREHOUSE)
    t0 = 1_700_000_000.0
    for i in range(0, 120):  # first 12 s: intrusion fires at ~8 s
        pipe.process_frame(renderer.render(i / FPS), t0 + i / FPS)
    deadline = time.time() + 10
    while time.time() < deadline and not list(settings.clips_dir.glob("*.mp4")):
        time.sleep(0.05)

    events = stream_events(redis_client)
    types = {t for t, _ in events}
    assert {"stats", "track_points", "alert", "incident_update", "camera_status", "line_cross"} <= types
    alert = next(d for t, d in events if t == "alert")
    assert alert["rule_type"] == "zone_intrusion" and alert["severity"] == "high"
    assert (settings.data_dir / "media" / alert["snapshot_path"]).exists()
    updates = [d for t, d in events if t == "incident_update" and d["id"] == alert["id"]]
    assert any("description" in u for u in updates)
    assert list(settings.clips_dir.glob("*.mp4"))

    frames = stream_events(redis_client, "vg:frames")
    assert len(frames) == 120
    last = frames[-1][1]
    assert last["camera_id"] == "cam-1" and last["width"] == 640 and last["tracks"]
    assert all(0 <= v <= 1 for v in last["tracks"][0]["bbox"])
    assert pipe.status()["frames"] == 120
    assert pipe.fps == pytest.approx(10, rel=0.05)
    assert pipe.latest_jpeg is not None and pipe.latest_jpeg[:2] == b"\xff\xd8"


def test_pipeline_hot_reconfigure_and_model_switch(
    warehouse_camera: CameraConfig, bus: EventBus, settings: Settings
) -> None:
    pipe = CameraPipeline(warehouse_camera, "color-synthetic", _deps(bus, settings))
    frame = SceneRenderer(WAREHOUSE).render(5)
    pipe.process_frame(frame, 1.0)
    pipe.update_camera(CameraConfig("cam-1", "Renamed", "file", "samples/warehouse.mp4"))
    pipe.set_model("motion-mog2")
    pipe.process_frame(frame, 2.0)
    assert pipe.camera.name == "Renamed" and pipe.engine.camera.rules == ()
    assert pipe.model_id == "motion-mog2"


def test_pipeline_thread_plays_file_and_stops(
    warehouse_camera: CameraConfig, bus: EventBus, settings: Settings
) -> None:
    write_scene(WAREHOUSE, settings.video_dir / "samples" / "warehouse.mp4")
    pipe = CameraPipeline(warehouse_camera, "color-synthetic", _deps(bus, settings))
    pipe.start()
    deadline = time.time() + 5
    while time.time() < deadline and pipe.frame_idx < 5:
        time.sleep(0.05)
    assert pipe.running and pipe.state == "online"
    pipe.stop()
    assert not pipe.running and pipe.state == "offline"


def test_pipeline_reports_source_errors(bus: EventBus, settings: Settings) -> None:
    cam = CameraConfig("bad", "Bad", "file", "samples/missing.mp4")
    pipe = CameraPipeline(cam, "color-synthetic", _deps(bus, settings))
    pipe.start()
    deadline = time.time() + 3
    while time.time() < deadline and pipe.state != "error":
        time.sleep(0.02)
    assert pipe.state == "error" and "not found" in (pipe.message or "")
    pipe.stop()


def _config_doc(version: int, enabled: bool = True, **cam: Any) -> dict[str, Any]:
    camera = {
        "id": "cam-1",
        "name": "Warehouse",
        "source_type": "file",
        "source_uri": "samples/warehouse.mp4",
        "enabled": enabled,
        "privacy_mode": True,
        "target_fps": 10,
        "zones": [
            {"id": "z1", "name": "R", "kind": "polygon", "points": [[0.7, 0.1], [0.95, 0.1], [0.95, 0.45]]}
        ],
        "rules": [
            {
                "id": "r1",
                "name": "Intrusion",
                "type": "zone_intrusion",
                "zone_id": "z1",
                "params": {"cooldown_seconds": 5},
                "severity": "high",
                "enabled": True,
            }
        ],
    }
    camera.update(cam)
    return {"version": version, "active_model": "color-synthetic", "cameras": [camera, {"id": "broken"}]}


def test_parse_config_skips_malformed() -> None:
    model, cams = parse_config(_config_doc(1))
    assert model == "color-synthetic"
    assert [c.id for c in cams] == ["cam-1"]
    assert cams[0].rules[0].cooldown_seconds == 5
    assert cams[0].zones[0].points[0] == (0.7, 0.1)


def test_manager_reconcile_lifecycle(
    bus: EventBus, redis_client: fakeredis.FakeRedis, settings: Settings
) -> None:
    import json

    write_scene(WAREHOUSE, settings.video_dir / "samples" / "warehouse.mp4")
    mgr = PipelineManager(
        settings, bus, ModelCatalog(settings.models_dir), ResilientDescriber(FakeDescriber())
    )
    assert mgr.poll_once() is False  # no config yet
    redis_client.set("vg:config", json.dumps(_config_doc(1)))
    assert mgr.poll_once() is True
    assert mgr.poll_once() is False  # same version
    first = mgr.get("cam-1")
    assert first is not None and first.running

    # Rule-only change -> same pipeline object, hot update.
    redis_client.set("vg:config", json.dumps(_config_doc(2, name="Renamed")))
    mgr.poll_once()
    assert mgr.get("cam-1") is first

    # Source change -> restart.
    redis_client.set("vg:config", json.dumps(_config_doc(3, target_fps=5)))
    mgr.poll_once()
    assert mgr.get("cam-1") is not first

    # Model switch propagates.
    doc = _config_doc(4, target_fps=5)
    doc["active_model"] = "motion-mog2"
    redis_client.set("vg:config", json.dumps(doc))
    mgr.poll_once()
    assert mgr.active_model == "motion-mog2"

    models = mgr.publish_catalog()
    assert next(m for m in models if m["id"] == "motion-mog2")["active"] is True
    assert json.loads(redis_client.get("vg:models") or "[]")

    # Disable -> stopped and status says disabled.
    redis_client.set("vg:config", json.dumps(_config_doc(5, enabled=False)))
    mgr.poll_once()
    assert mgr.get("cam-1") is None
    assert json.loads(redis_client.get("vg:status:cam-1") or "{}")["state"] == "disabled"
    mgr.stop()


def test_app_endpoints(bus: EventBus, redis_client: fakeredis.FakeRedis, settings: Settings) -> None:
    mgr = PipelineManager(
        settings, bus, ModelCatalog(settings.models_dir), ResilientDescriber(FakeDescriber())
    )
    cam = CameraConfig("cam-x", "X", "file", "samples/none.mp4")
    pipe = CameraPipeline(cam, "color-synthetic", mgr.deps)
    mgr.pipelines["cam-x"] = pipe
    with TestClient(create_app(settings, manager=mgr)) as client:
        health = client.get("/health").json()
        assert health["status"] == "ok" and health["pipelines"] == 1
        assert client.get("/snapshot/cam-x").status_code == 404
        assert client.get("/stream/unknown").status_code == 404
        pipe.process_frame(SceneRenderer(WAREHOUSE).render(3), 1.0)
        snap = client.get("/snapshot/cam-x")
        assert snap.status_code == 200 and snap.headers["content-type"] == "image/jpeg"
        assert client.get("/pipelines").json()[0]["camera_id"] == "cam-x"
        assert any(m["id"] == "color-synthetic" for m in client.get("/models").json())
        assert "vg_frames_processed_total" in client.get("/metrics").text


def test_sources(settings: Settings, tmp_path: Path) -> None:
    video = write_scene(WAREHOUSE, settings.video_dir / "samples" / "warehouse.mp4")
    with pytest.raises(SourceError):
        resolve_file("../../etc/passwd", settings.video_dir)
    with pytest.raises(SourceError):
        open_source(CameraConfig("c", "c", "rtsp", "ftp://nope"), settings.video_dir)
    src = FileSource(video, target_fps=5)
    assert src.step == 2 and src.frame_interval == pytest.approx(0.2)
    for _ in range(320):  # beyond 600 source frames -> loops
        assert src.read() is not None
    src.close()


def test_privacy_blurs_head_region() -> None:
    frame = np.zeros((100, 100, 3), np.uint8)
    frame[10:20, 10:30] = 255
    frame[12:14, 10:30] = 0  # high-frequency detail in the head region
    person = TrackState(1, "person", 0.9, 10, 10, 30, 60)
    out = FaceBlurrer().apply(frame, [person])
    assert not np.array_equal(out[10:20, 10:30], frame[10:20, 10:30])
    assert np.array_equal(out[40:60, 10:30], frame[40:60, 10:30])  # body untouched
    assert np.array_equal(FaceBlurrer().apply(frame, []), frame)


def test_ring_buffer_and_clip_writer(tmp_path: Path) -> None:
    ring = FrameRingBuffer(seconds=1, fps=5)
    for i in range(10):
        ring.push(BufferedFrame(float(i), np.full((48, 64, 3), i * 20, np.uint8), []))
    frames = ring.snapshot()
    assert len(frames) == 5 and frames[0].ts == 5.0
    path = write_clip([f.frame for f in frames], tmp_path / "c.mp4", 5)
    assert path.exists() and path.stat().st_size > 0
