"""Detector interface and registry."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Protocol, runtime_checkable

from visionguard_ml.types import Detection, Frame


@runtime_checkable
class Detector(Protocol):
    """Anything that turns a BGR frame into detections (pixel xyxy)."""

    model_id: str
    classes: tuple[str, ...]

    def detect(self, frame: Frame) -> list[Detection]: ...


@dataclass(frozen=True, slots=True)
class ModelInfo:
    """Catalogue entry advertised to the API (shown on the model management page)."""

    id: str
    name: str
    backend: str  # color | motion | pytorch | onnx
    classes: tuple[str, ...]
    available: bool
    path: str | None = None
    metrics: dict[str, float | str | None] = field(default_factory=dict)

    def to_dict(self) -> dict[str, object]:
        return {
            "id": self.id,
            "name": self.name,
            "backend": self.backend,
            "classes": list(self.classes),
            "available": self.available,
            "map50": self.metrics.get("map50"),
            "map50_95": self.metrics.get("map50_95"),
            "latency_ms": self.metrics.get("latency_ms"),
            "fps": self.metrics.get("fps"),
            "benchmark_source": self.metrics.get("source"),
        }


class DetectorUnavailableError(RuntimeError):
    """Raised when a model is requested whose weights/runtime are not present."""
