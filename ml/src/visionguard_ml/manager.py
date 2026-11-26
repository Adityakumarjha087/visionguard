"""Reconciles running pipelines with the desired state published by the API."""

from __future__ import annotations

import logging
import threading
from concurrent.futures import ThreadPoolExecutor
from typing import Any

from visionguard_ml import metrics as m
from visionguard_ml.bus import EventBus, parse_config
from visionguard_ml.config import Settings
from visionguard_ml.describe import ResilientDescriber
from visionguard_ml.detectors import Detector, ModelCatalog
from visionguard_ml.pipeline import CameraPipeline, PipelineDeps, iso
from visionguard_ml.types import CameraConfig

log = logging.getLogger(__name__)


class PipelineManager:
    def __init__(
        self, settings: Settings, bus: EventBus, catalog: ModelCatalog, describer: ResilientDescriber
    ) -> None:
        self.settings = settings
        self.bus = bus
        self.catalog = catalog
        self.executor = ThreadPoolExecutor(max_workers=4, thread_name_prefix="describe")
        self.deps = PipelineDeps(
            bus=bus,
            detector_factory=self._make_detector,
            describer=describer,
            executor=self.executor,
            snapshots_dir=settings.snapshots_dir,
            clips_dir=settings.clips_dir,
            video_dir=settings.video_dir,
            jpeg_quality=settings.jpeg_quality,
            clip_pre_seconds=settings.clip_pre_seconds,
            clip_post_seconds=settings.clip_post_seconds,
        )
        self.active_model = settings.default_model
        self.pipelines: dict[str, CameraPipeline] = {}
        self._keys: dict[str, tuple[str, str, str, float]] = {}
        self._config_version: Any = object()
        self._stop = threading.Event()
        self._lock = threading.Lock()
        self._thread: threading.Thread | None = None

    def _make_detector(self, model_id: str) -> Detector:
        return self.catalog.create_or_fallback(model_id)

    # ------------------------------------------------------------------ reconcile
    def reconcile(self, doc: dict[str, Any]) -> None:
        model, cameras = parse_config(doc)
        with self._lock:
            if model and model != self.active_model:
                log.info("switching active model", extra={"model": model})
                self.active_model = model
                for p in self.pipelines.values():
                    p.set_model(model)
            desired = {c.id: c for c in cameras}
            for cam_id in list(self.pipelines):
                cam = desired.get(cam_id)
                if cam is None or not cam.enabled:
                    self._stop_pipeline(cam_id)
                    if cam is not None:
                        self._disabled_status(cam)
            for cam in cameras:
                if not cam.enabled:
                    self._disabled_status(cam)
                    continue
                key = cam.pipeline_key()
                existing = self.pipelines.get(cam.id)
                if existing is not None and self._keys.get(cam.id) == key:
                    existing.update_camera(cam)
                    continue
                if existing is not None:
                    self._stop_pipeline(cam.id)
                self._start_pipeline(cam)
            m.PIPELINES.set(len(self.pipelines))

    def _start_pipeline(self, cam: CameraConfig) -> None:
        log.info("starting pipeline", extra={"camera": cam.id, "source": cam.source_uri})
        p = CameraPipeline(cam, self.active_model, self.deps)
        self.pipelines[cam.id] = p
        self._keys[cam.id] = cam.pipeline_key()
        p.start()

    def _stop_pipeline(self, cam_id: str) -> None:
        p = self.pipelines.pop(cam_id, None)
        self._keys.pop(cam_id, None)
        if p is not None:
            log.info("stopping pipeline", extra={"camera": cam_id})
            p.stop()

    def _disabled_status(self, cam: CameraConfig) -> None:
        import time

        self.bus.set_status(
            cam.id,
            {
                "camera_id": cam.id,
                "state": "disabled",
                "fps": 0,
                "latency_ms": 0,
                "updated_at": iso(time.time()),
            },
            ttl=3600,
        )

    def poll_once(self) -> bool:
        doc = self.bus.read_config()
        if doc is None:
            return False
        version = doc.get("version")
        if version == self._config_version:
            return False
        self._config_version = version
        self.reconcile(doc)
        return True

    def publish_catalog(self) -> list[dict[str, Any]]:
        models = [{**mi.to_dict(), "active": mi.id == self.active_model} for mi in self.catalog.list()]
        self.bus.publish_models(models)
        self.bus.publish_event("model_catalog", {"models": models})
        return models

    # ------------------------------------------------------------------ lifecycle
    def start(self) -> None:
        def loop() -> None:
            ticks = 0
            while not self._stop.is_set():
                try:
                    self.poll_once()
                    if ticks % 30 == 0:
                        self.publish_catalog()
                except Exception:
                    log.exception("config reconcile failed")
                ticks += 1
                self._stop.wait(self.settings.config_poll_seconds)

        self._thread = threading.Thread(target=loop, daemon=True, name="config-sync")
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=5)
        with self._lock:
            for cam_id in list(self.pipelines):
                self._stop_pipeline(cam_id)
        self.executor.shutdown(wait=False, cancel_futures=True)

    def get(self, camera_id: str) -> CameraPipeline | None:
        return self.pipelines.get(camera_id)

    def statuses(self) -> list[dict[str, Any]]:
        return [p.status() for p in self.pipelines.values()]
