"""PyTorch backend via Ultralytics (optional extra ``visionguard-ml[yolo]``)."""

from __future__ import annotations

from pathlib import Path

from visionguard_ml.detectors.base import DetectorUnavailableError
from visionguard_ml.types import Detection, Frame


class UltralyticsDetector:
    def __init__(
        self,
        path: Path,
        model_id: str,
        conf_thresh: float = 0.35,
        keep_classes: set[str] | None = None,
        imgsz: int = 640,
    ) -> None:
        if not path.exists():
            raise DetectorUnavailableError(f"PyTorch weights not found at {path}")
        try:
            from ultralytics import YOLO
        except ImportError as exc:  # pragma: no cover - depends on optional extra
            raise DetectorUnavailableError("ultralytics is not installed (pip install '.[yolo]')") from exc
        self.model = YOLO(str(path))
        self.names: dict[int, str] = dict(self.model.names)
        self.model_id = model_id
        self.keep_classes = keep_classes
        self.classes = tuple(n for n in self.names.values() if keep_classes is None or n in keep_classes)
        self.conf_thresh = conf_thresh
        self.imgsz = imgsz

    def detect(self, frame: Frame) -> list[Detection]:
        result = self.model.predict(
            frame, conf=self.conf_thresh, imgsz=self.imgsz, verbose=False, device="cpu"
        )[0]
        out: list[Detection] = []
        for (x1, y1, x2, y2), conf, cls in zip(
            result.boxes.xyxy.tolist(), result.boxes.conf.tolist(), result.boxes.cls.tolist(), strict=True
        ):
            name = self.names[int(cls)]
            if self.keep_classes is None or name in self.keep_classes:
                out.append(Detection(x1, y1, x2, y2, float(conf), name))
        return out
