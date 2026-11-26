"""Deterministic colour-segmentation detector for the synthetic demo scenes.

It is the default backend in tests and in the demo stack: zero weights, sub-millisecond,
and exact enough that rules fire reproducibly. Not intended for real footage.
"""

from __future__ import annotations

import cv2
import numpy as np

from visionguard_ml.types import Detection, Frame

_KERNEL = np.ones((3, 3), np.uint8)


class ColorDetector:
    model_id = "color-synthetic"
    classes: tuple[str, ...] = ("person", "car", "helmet")

    def __init__(self, min_area: int = 120) -> None:
        self.min_area = min_area

    def detect(self, frame: Frame) -> list[Detection]:
        hsv = cv2.cvtColor(frame, cv2.COLOR_BGR2HSV)
        h, s, v = hsv[..., 0], hsv[..., 1], hsv[..., 2]
        sat = (s > 150) & (v > 100)
        red = sat & ((h < 8) | (h > 172))
        blue = sat & (h > 95) & (h < 125)
        yellow = sat & (h > 18) & (h < 36)

        out: list[Detection] = []
        for x, y, w, hh, fill in self._blobs(red):
            # Body blob; extend the box upwards to include the head (head ~0.4 x body height).
            head = 0.42 * hh
            out.append(Detection(x, max(0.0, y - head), x + w, y + hh, self._conf(fill), "person"))
        for x, y, w, hh, fill in self._blobs(blue):
            out.append(Detection(x, y, x + w, y + hh, self._conf(fill), "car"))
        for x, y, w, hh, fill in self._blobs(yellow, min_area=40):
            out.append(Detection(x, y, x + w, y + hh, self._conf(fill), "helmet"))
        return out

    def _blobs(self, mask: np.ndarray, min_area: int | None = None) -> list[tuple[int, int, int, int, float]]:
        m = cv2.morphologyEx(mask.astype(np.uint8) * 255, cv2.MORPH_OPEN, _KERNEL)
        n, _, stats, _ = cv2.connectedComponentsWithStats(m, connectivity=8)
        limit = self.min_area if min_area is None else min_area
        blobs = []
        for i in range(1, n):
            x, y, w, h, area = (int(v) for v in stats[i])
            if area < limit:
                continue
            blobs.append((x, y, w, h, area / float(w * h)))
        return blobs

    @staticmethod
    def _conf(fill: float) -> float:
        return float(min(0.99, 0.55 + 0.45 * fill))
