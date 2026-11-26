"""Privacy mode: blur faces before any frame is persisted (snapshots and clips)."""

from __future__ import annotations

from collections.abc import Iterable
from pathlib import Path

import cv2

from visionguard_ml.types import Frame, TrackState


class FaceBlurrer:
    """Two complementary strategies.

    1. Haar-cascade frontal face detection (ships with OpenCV, no download).
    2. Head-region fallback: the top ~22% of every *person* box is blurred, which also
       covers profiles/backs of heads that Haar misses. Privacy beats precision here.
    """

    def __init__(self, head_ratio: float = 0.22, kernel: int = 31) -> None:
        haar_dir: str = cv2.data.haarcascades  # type: ignore[attr-defined]
        cascade_path = Path(haar_dir) / "haarcascade_frontalface_default.xml"
        self._cascade = (
            cv2.CascadeClassifier(str(cascade_path))  # type: ignore[attr-defined]
            if cascade_path.exists()
            else None
        )
        self.head_ratio = head_ratio
        self.kernel = kernel | 1

    def faces(self, frame: Frame) -> list[tuple[int, int, int, int]]:
        if self._cascade is None or self._cascade.empty():
            return []
        gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        found = self._cascade.detectMultiScale(gray, scaleFactor=1.15, minNeighbors=5, minSize=(16, 16))
        return [(int(x), int(y), int(x + w), int(y + h)) for x, y, w, h in found]

    def apply(self, frame: Frame, tracks: Iterable[TrackState] = ()) -> Frame:
        out = frame.copy()
        h, w = out.shape[:2]
        regions = list(self.faces(frame))
        for t in tracks:
            if t.cls != "person":
                continue
            bh = t.y2 - t.y1
            regions.append((int(t.x1), int(t.y1), int(t.x2), int(t.y1 + bh * self.head_ratio)))
        for rx1, ry1, rx2, ry2 in regions:
            x1, y1 = max(0, rx1), max(0, ry1)
            x2, y2 = min(w, rx2), min(h, ry2)
            if x2 - x1 < 2 or y2 - y1 < 2:
                continue
            roi = out[y1:y2, x1:x2]
            k = max(3, min(self.kernel, (min(roi.shape[:2]) // 2) * 2 + 1))
            out[y1:y2, x1:x2] = cv2.GaussianBlur(roi, (k, k), 0)
        return out
