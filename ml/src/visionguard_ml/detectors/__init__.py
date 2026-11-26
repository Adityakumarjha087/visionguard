"""Pluggable detector backends and the model catalogue."""

from __future__ import annotations

import json
import logging
from importlib.util import find_spec
from pathlib import Path

from visionguard_ml.detectors.base import Detector, DetectorUnavailableError, ModelInfo
from visionguard_ml.detectors.color import ColorDetector
from visionguard_ml.detectors.motion import MotionDetector
from visionguard_ml.detectors.onnx_yolo import COCO_KEEP, OnnxYoloDetector

log = logging.getLogger(__name__)

__all__ = ["Detector", "DetectorUnavailableError", "ModelCatalog", "ModelInfo"]

PPE_CLASSES = ("person", "helmet", "no_helmet")


class ModelCatalog:
    """Knows which models exist, whether their weights are present, and how to build them."""

    def __init__(self, models_dir: Path, benchmarks_file: Path | None = None) -> None:
        self.models_dir = models_dir
        self.benchmarks = _load_benchmarks(benchmarks_file)

    def list(self) -> list[ModelInfo]:
        torch_ok = find_spec("ultralytics") is not None
        d = self.models_dir
        specs: list[tuple[str, str, str, tuple[str, ...], bool, Path | None]] = [
            (
                "color-synthetic",
                "Colour segmentation (synthetic demo)",
                "color",
                ColorDetector.classes,
                True,
                None,
            ),
            ("motion-mog2", "MOG2 motion detector", "motion", MotionDetector.classes, True, None),
            (
                "yolo11n-onnx",
                "YOLO11n COCO (ONNX Runtime)",
                "onnx",
                tuple(sorted(COCO_KEEP)),
                (d / "yolo11n.onnx").exists(),
                d / "yolo11n.onnx",
            ),
            (
                "yolo11n",
                "YOLO11n COCO (PyTorch)",
                "pytorch",
                tuple(sorted(COCO_KEEP)),
                torch_ok and (d / "yolo11n.pt").exists(),
                d / "yolo11n.pt",
            ),
            (
                "ppe-yolo11n-onnx",
                "YOLO11n fine-tuned PPE (ONNX)",
                "onnx",
                PPE_CLASSES,
                (d / "ppe-yolo11n.onnx").exists(),
                d / "ppe-yolo11n.onnx",
            ),
        ]
        return [
            ModelInfo(
                mid, name, backend, classes, avail, str(path) if path else None, self.benchmarks.get(mid, {})
            )
            for mid, name, backend, classes, avail, path in specs
        ]

    def get(self, model_id: str) -> ModelInfo:
        for m in self.list():
            if m.id == model_id:
                return m
        raise DetectorUnavailableError(f"unknown model {model_id!r}")

    def create(self, model_id: str) -> Detector:
        info = self.get(model_id)
        if not info.available:
            raise DetectorUnavailableError(f"model {model_id!r} is not available on this host")
        if info.backend == "color":
            return ColorDetector()
        if info.backend == "motion":
            return MotionDetector()
        assert info.path is not None
        keep = set(COCO_KEEP) if model_id.startswith("yolo11n") else None
        if info.backend == "onnx":
            return OnnxYoloDetector(Path(info.path), model_id, keep_classes=keep)
        from visionguard_ml.detectors.ultralytics_yolo import UltralyticsDetector

        return UltralyticsDetector(Path(info.path), model_id, keep_classes=keep)

    def create_or_fallback(self, model_id: str, fallback: str = "color-synthetic") -> Detector:
        try:
            return self.create(model_id)
        except DetectorUnavailableError as exc:
            log.warning("model unavailable, falling back", extra={"model": model_id, "error": str(exc)})
            return self.create(fallback)


def _load_benchmarks(path: Path | None) -> dict[str, dict[str, float | str | None]]:
    if path is None or not path.exists():
        return {}
    try:
        data = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        log.warning("could not parse benchmarks file %s", path)
        return {}
    models = data.get("models", {})
    return {str(k): dict(v) for k, v in models.items() if isinstance(v, dict)}
