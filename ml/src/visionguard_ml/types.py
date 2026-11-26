"""Core value types shared across the pipeline."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal

import numpy as np
import numpy.typing as npt

Frame = npt.NDArray[np.uint8]
Point = tuple[float, float]

VEHICLE_CLASSES: frozenset[str] = frozenset({"car", "truck", "bus", "motorcycle", "vehicle"})
PERSON_CLASSES: frozenset[str] = frozenset({"person"})


@dataclass(frozen=True, slots=True)
class Detection:
    """A single detection in pixel coordinates (x1, y1, x2, y2)."""

    x1: float
    y1: float
    x2: float
    y2: float
    conf: float
    cls: str

    @property
    def xyxy(self) -> tuple[float, float, float, float]:
        return (self.x1, self.y1, self.x2, self.y2)


@dataclass(slots=True)
class TrackState:
    """A tracked object as emitted by the tracker for one frame (pixel coordinates)."""

    track_id: int
    cls: str
    conf: float
    x1: float
    y1: float
    x2: float
    y2: float

    def normalized(self, width: int, height: int) -> tuple[float, float, float, float]:
        return (
            _clamp01(self.x1 / width),
            _clamp01(self.y1 / height),
            _clamp01(self.x2 / width),
            _clamp01(self.y2 / height),
        )

    def anchor(self, width: int, height: int) -> Point:
        """Bottom-centre ("feet") point, normalised; used for zone membership and crossings."""
        return (_clamp01((self.x1 + self.x2) / 2 / width), _clamp01(self.y2 / height))

    def to_message(self, width: int, height: int) -> dict[str, Any]:
        return {
            "id": self.track_id,
            "cls": self.cls,
            "conf": round(self.conf, 3),
            "bbox": [round(v, 4) for v in self.normalized(width, height)],
        }


ZoneKind = Literal["polygon", "line"]
RuleType = Literal["zone_intrusion", "dwell_time", "missing_ppe", "crowd_count", "line_crossing"]


@dataclass(frozen=True, slots=True)
class Zone:
    id: str
    name: str
    kind: ZoneKind
    points: tuple[Point, ...]


@dataclass(frozen=True, slots=True)
class Rule:
    id: str
    name: str
    type: RuleType
    zone_id: str | None
    severity: str = "medium"
    enabled: bool = True
    classes: tuple[str, ...] = ()
    seconds: float | None = None
    max_count: int | None = None
    cooldown_seconds: float = 30.0


@dataclass(frozen=True, slots=True)
class CameraConfig:
    id: str
    name: str
    source_type: Literal["rtsp", "webcam", "file"]
    source_uri: str
    enabled: bool = True
    privacy_mode: bool = True
    target_fps: float = 10.0
    zones: tuple[Zone, ...] = ()
    rules: tuple[Rule, ...] = ()

    def pipeline_key(self) -> tuple[str, str, str, float]:
        """Fields whose change requires restarting the capture pipeline."""
        return (self.source_type, self.source_uri, str(self.enabled), self.target_fps)


@dataclass(slots=True)
class AlertEvent:
    incident_id: str
    camera_id: str
    rule_id: str
    rule_name: str
    rule_type: RuleType
    severity: str
    ts: float
    track_ids: list[int]
    object_class: str
    message: str
    zone_id: str | None = None
    snapshot_path: str | None = None
    extra: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, slots=True)
class LineCrossing:
    camera_id: str
    zone_id: str
    track_id: int
    cls: str
    direction: Literal["in", "out"]
    ts: float


def _clamp01(v: float) -> float:
    return 0.0 if v < 0.0 else 1.0 if v > 1.0 else v
