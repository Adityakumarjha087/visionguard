"""Per-camera processing pipeline: capture -> detect -> track -> rules -> publish."""

from __future__ import annotations

import logging
import threading
import time
from collections import defaultdict
from collections.abc import Callable
from concurrent.futures import Executor
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from visionguard_ml import metrics as m
from visionguard_ml.bus import EventBus
from visionguard_ml.describe import IncidentContext, ResilientDescriber
from visionguard_ml.detectors import Detector
from visionguard_ml.media import (
    BufferedFrame,
    ClipRecorder,
    FrameRingBuffer,
    PendingClip,
    annotate,
    encode_jpeg,
)
from visionguard_ml.privacy import FaceBlurrer
from visionguard_ml.rules.engine import RuleEngine
from visionguard_ml.sources import FrameSource, SourceError, open_source
from visionguard_ml.tracking.bytetrack import ByteTrackConfig, ByteTracker
from visionguard_ml.types import AlertEvent, CameraConfig, Frame, LineCrossing, TrackState

log = logging.getLogger(__name__)


def iso(ts: float) -> str:
    return datetime.fromtimestamp(ts, UTC).isoformat().replace("+00:00", "Z")


@dataclass(slots=True)
class PipelineDeps:
    bus: EventBus
    detector_factory: Callable[[str], Detector]
    describer: ResilientDescriber
    executor: Executor
    snapshots_dir: Path
    clips_dir: Path
    video_dir: Path
    jpeg_quality: int = 70
    clip_pre_seconds: float = 4.0
    clip_post_seconds: float = 4.0


class CameraPipeline:
    """Owns one camera. ``process_frame`` is pure-ish and unit-tested; ``run`` adds I/O."""

    STATS_INTERVAL = 1.0
    STATUS_INTERVAL = 3.0

    def __init__(self, camera: CameraConfig, model_id: str, deps: PipelineDeps) -> None:
        self.camera = camera
        self.deps = deps
        self.model_id = model_id
        self.detector = deps.detector_factory(model_id)
        self.tracker = ByteTracker(ByteTrackConfig(frame_rate=camera.target_fps))
        self.engine = RuleEngine(camera)
        self.blurrer = FaceBlurrer()
        self.ring = FrameRingBuffer(deps.clip_pre_seconds, camera.target_fps)
        self.clips = ClipRecorder()
        self.frame_idx = 0
        self.fps = 0.0
        self.latency_ms = 0.0
        self.state = "starting"
        self.message: str | None = None
        self.latest_jpeg: bytes | None = None
        self.latest_seq = 0
        self._pending_camera: CameraConfig | None = None
        self._pending_model: str | None = None
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        self._window_counts: dict[str, int] = defaultdict(int)
        self._window_points: dict[int, dict[str, Any]] = {}
        self._last_stats = 0.0
        self._last_status = 0.0
        self._fps_ema: float | None = None
        self._last_frame_time: float | None = None

    # ------------------------------------------------------------------ control
    def start(self) -> None:
        self._thread = threading.Thread(target=self.run, daemon=True, name=f"pipeline-{self.camera.id[:8]}")
        self._thread.start()

    def stop(self, timeout: float = 5.0) -> None:
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=timeout)
        self.clips.flush()

    @property
    def running(self) -> bool:
        return self._thread is not None and self._thread.is_alive()

    def update_camera(self, camera: CameraConfig) -> None:
        """Zones/rules/privacy changed; applied before the next frame (thread-safe)."""
        with self._lock:
            self._pending_camera = camera

    def set_model(self, model_id: str) -> None:
        with self._lock:
            self._pending_model = model_id

    def _apply_pending(self) -> None:
        with self._lock:
            cam, model = self._pending_camera, self._pending_model
            self._pending_camera = self._pending_model = None
        if cam is not None:
            self.camera = cam
            self.engine.reconfigure(cam)
        if model is not None and model != self.model_id:
            self.detector = self.deps.detector_factory(model)
            self.model_id = model
            self.tracker.reset()

    # ------------------------------------------------------------------ loop
    def run(self) -> None:
        backoff = 1.0
        while not self._stop.is_set():
            try:
                source = open_source(self.camera, self.deps.video_dir)
            except SourceError as exc:
                self._set_state("error", str(exc))
                m.SOURCE_ERRORS.labels(self.camera.id).inc()
                if self._stop.wait(backoff):
                    break
                backoff = min(backoff * 2, 30.0)
                continue
            backoff = 1.0
            self._set_state("online", None)
            try:
                self._loop(source)
            finally:
                source.close()
        self._set_state("offline", None)

    def _loop(self, source: FrameSource) -> None:
        next_tick = time.monotonic()
        failures = 0
        while not self._stop.is_set():
            frame = source.read()
            if frame is None:
                failures += 1
                m.SOURCE_ERRORS.labels(self.camera.id).inc()
                if failures > 5:
                    self._set_state("error", "source stopped producing frames")
                    return
                continue
            failures = 0
            self.process_frame(frame, time.time())
            next_tick += source.frame_interval
            delay = next_tick - time.monotonic()
            if delay > 0:
                self._stop.wait(delay)
            else:
                next_tick = time.monotonic()  # running behind: don't try to catch up

    # ------------------------------------------------------------------ per frame
    def process_frame(self, frame: Frame, ts: float) -> tuple[list[TrackState], list[AlertEvent]]:
        self._apply_pending()
        cam_label = self.camera.id
        h, w = frame.shape[:2]
        t0 = time.perf_counter()
        detections = self.detector.detect(frame)
        t1 = time.perf_counter()
        tracks = self.tracker.update(detections)
        t2 = time.perf_counter()
        alerts, crossings = self.engine.process(tracks, w, h, ts)
        t3 = time.perf_counter()

        m.STAGE_LATENCY.labels(cam_label, "detect").observe(t1 - t0)
        m.STAGE_LATENCY.labels(cam_label, "track").observe(t2 - t1)
        m.STAGE_LATENCY.labels(cam_label, "rules").observe(t3 - t2)
        m.STAGE_LATENCY.labels(cam_label, "total").observe(t3 - t0)
        m.FRAMES.labels(cam_label).inc()
        m.ACTIVE_TRACKS.labels(cam_label).set(len(tracks))
        for d in detections:
            m.DETECTIONS.labels(cam_label, d.cls).inc()

        self.frame_idx += 1
        self.latency_ms = (t3 - t0) * 1000
        self._update_fps(ts)
        self.latest_jpeg = encode_jpeg(frame, self.deps.jpeg_quality)
        self.latest_seq += 1

        self.deps.bus.publish_frame(
            {
                "camera_id": self.camera.id,
                "ts": iso(ts),
                "frame": self.frame_idx,
                "fps": round(self.fps, 2),
                "latency_ms": round(self.latency_ms, 2),
                "width": w,
                "height": h,
                "model": self.model_id,
                "tracks": [t.to_message(w, h) for t in tracks],
            }
        )

        item = BufferedFrame(ts, frame, tracks)
        self.ring.push(item)
        self.clips.feed(item)
        self._accumulate(tracks, w, h, ts)
        for c in crossings:
            self._publish_crossing(c)
        for alert in alerts:
            self._handle_alert(alert, frame, tracks)
        self._maybe_publish_status(ts)
        return tracks, alerts

    def _update_fps(self, ts: float) -> None:
        if self._last_frame_time is not None and ts > self._last_frame_time:
            inst = 1.0 / (ts - self._last_frame_time)
            self._fps_ema = inst if self._fps_ema is None else 0.9 * self._fps_ema + 0.1 * inst
            self.fps = self._fps_ema
            m.FPS.labels(self.camera.id).set(self.fps)
        self._last_frame_time = ts

    def _accumulate(self, tracks: list[TrackState], w: int, h: int, ts: float) -> None:
        counts: dict[str, int] = defaultdict(int)
        for t in tracks:
            counts[t.cls] += 1
            if t.track_id not in self._window_points and t.cls != "helmet":
                x, y = t.anchor(w, h)
                self._window_points[t.track_id] = {
                    "track_id": t.track_id,
                    "cls": t.cls,
                    "x": round(x, 4),
                    "y": round(y, 4),
                }
        for cls, n in counts.items():
            self._window_counts[cls] = max(self._window_counts[cls], n)
        if ts - self._last_stats < self.STATS_INTERVAL:
            return
        self._last_stats = ts
        self.deps.bus.publish_event(
            "stats", {"camera_id": self.camera.id, "ts": iso(ts), "counts": dict(self._window_counts)}
        )
        if self._window_points:
            self.deps.bus.publish_event(
                "track_points",
                {"camera_id": self.camera.id, "ts": iso(ts), "points": list(self._window_points.values())},
            )
        self._window_counts = defaultdict(int)
        self._window_points = {}

    def _publish_crossing(self, c: LineCrossing) -> None:
        m.CROSSINGS.labels(self.camera.id, c.direction).inc()
        self.deps.bus.publish_event(
            "line_cross",
            {
                "camera_id": c.camera_id,
                "zone_id": c.zone_id,
                "track_id": c.track_id,
                "cls": c.cls,
                "direction": c.direction,
                "ts": iso(c.ts),
            },
        )

    def _privacy(self, frame: Frame, tracks: list[TrackState]) -> Frame:
        return self.blurrer.apply(frame, tracks) if self.camera.privacy_mode else frame

    def _handle_alert(self, alert: AlertEvent, frame: Frame, tracks: list[TrackState]) -> None:
        m.ALERTS.labels(self.camera.id, alert.rule_type).inc()
        zones = self.camera.zones
        highlight = set(alert.track_ids)
        snapshot = annotate(self._privacy(frame, tracks), tracks, zones, highlight)
        jpeg = encode_jpeg(snapshot, 85)
        snap_rel = f"snapshots/{alert.incident_id}.jpg"
        path = self.deps.snapshots_dir / f"{alert.incident_id}.jpg"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(jpeg)
        alert.snapshot_path = snap_rel

        self.deps.bus.publish_event(
            "alert",
            {
                "id": alert.incident_id,
                "camera_id": alert.camera_id,
                "rule_id": alert.rule_id,
                "rule_name": alert.rule_name,
                "rule_type": alert.rule_type,
                "severity": alert.severity,
                "occurred_at": iso(alert.ts),
                "track_ids": alert.track_ids,
                "object_class": alert.object_class,
                "message": alert.message,
                "zone_id": alert.zone_id,
                "snapshot_path": snap_rel,
                "privacy_applied": self.camera.privacy_mode,
            },
        )
        log.info(
            "alert raised", extra={"camera": self.camera.id, "rule": alert.rule_name, "msg": alert.message}
        )

        privacy, cam_zones = self._privacy, zones

        def render(bf: BufferedFrame) -> Frame:
            return annotate(privacy(bf.frame, bf.tracks), bf.tracks, cam_zones, highlight)

        self.clips.start(
            PendingClip(
                incident_id=alert.incident_id,
                path=self.deps.clips_dir / f"{alert.incident_id}.mp4",
                until_ts=alert.ts + self.deps.clip_post_seconds,
                frames=self.ring.snapshot(),
                on_done=self._clip_done,
                transform=render,
                fps=self.camera.target_fps,
            )
        )
        ctx = IncidentContext(
            camera_name=self.camera.name,
            rule_name=alert.rule_name,
            rule_type=alert.rule_type,
            severity=alert.severity,
            message=alert.message,
            object_class=alert.object_class,
            occurred_at=iso(alert.ts),
        )
        self.deps.executor.submit(self._describe, alert.incident_id, jpeg, ctx)

    def _describe(self, incident_id: str, jpeg: bytes, ctx: IncidentContext) -> None:
        text, provider = self.deps.describer.describe_with_provider(jpeg, ctx)
        self.deps.bus.publish_event(
            "incident_update", {"id": incident_id, "description": text, "description_provider": provider}
        )

    def _clip_done(self, incident_id: str, path: Path | None) -> None:
        if path is None:
            return
        self.deps.bus.publish_event("incident_update", {"id": incident_id, "clip_path": f"clips/{path.name}"})

    # ------------------------------------------------------------------ status
    def _set_state(self, state: str, message: str | None) -> None:
        changed = state != self.state or message != self.message
        self.state, self.message = state, message
        if changed:
            self._publish_status(time.time())

    def status(self) -> dict[str, Any]:
        return {
            "camera_id": self.camera.id,
            "state": self.state,
            "fps": round(self.fps, 2),
            "latency_ms": round(self.latency_ms, 2),
            "message": self.message,
            "model": self.model_id,
            "frames": self.frame_idx,
        }

    def _maybe_publish_status(self, ts: float) -> None:
        if ts - self._last_status >= self.STATUS_INTERVAL:
            self._publish_status(ts)

    def _publish_status(self, ts: float) -> None:
        self._last_status = ts
        status = {**self.status(), "updated_at": iso(ts)}
        self.deps.bus.set_status(self.camera.id, status)
        self.deps.bus.publish_event("camera_status", status)
