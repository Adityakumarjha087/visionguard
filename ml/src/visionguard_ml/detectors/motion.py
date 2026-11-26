"""Background-subtraction (MOG2) detector: class-agnostic moving-object boxes.

A weights-free fallback for real footage when YOLO weights are not installed.
"""

from __future__ import annotations

import cv2
import numpy as np

from visionguard_ml.types import Detection, Frame


class MotionDetector:
    model_id = "motion-mog2"
    classes: tuple[str, ...] = ("object",)

    def __init__(self, min_area_ratio: float = 0.002, history: int = 200) -> None:
        self.min_area_ratio = min_area_ratio
        self._bg = cv2.createBackgroundSubtractorMOG2(history=history, varThreshold=32, detectShadows=True)
        self._kernel = cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (5, 5))

    def detect(self, frame: Frame) -> list[Detection]:
        mask = self._bg.apply(frame)
        mask = np.where(mask == 255, 255, 0).astype(np.uint8)  # drop shadows (127)
        mask = cv2.morphologyEx(mask, cv2.MORPH_OPEN, self._kernel)
        mask = cv2.dilate(mask, self._kernel, iterations=2)
        min_area = self.min_area_ratio * frame.shape[0] * frame.shape[1]
        contours, _ = cv2.findContours(mask, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
        out: list[Detection] = []
        for c in contours:
            area = cv2.contourArea(c)
            if area < min_area:
                continue
            x, y, w, h = cv2.boundingRect(c)
            conf = float(min(0.95, 0.5 + area / (w * h) * 0.45))
            out.append(Detection(float(x), float(y), float(x + w), float(y + h), conf, "object"))
        return out
