"""Constant-velocity Kalman filter in (cx, cy, aspect, height) space, as used by SORT/ByteTrack."""

from __future__ import annotations

import numpy as np
import numpy.typing as npt

FloatArray = npt.NDArray[np.float64]


class KalmanFilterXYAH:
    """8-dim state ``[cx, cy, a, h, vcx, vcy, va, vh]``; measurement ``[cx, cy, a, h]``."""

    def __init__(self) -> None:
        ndim, dt = 4, 1.0
        self._motion = np.eye(2 * ndim)
        for i in range(ndim):
            self._motion[i, ndim + i] = dt
        self._update = np.eye(ndim, 2 * ndim)
        self._std_pos = 1.0 / 20
        self._std_vel = 1.0 / 160

    def initiate(self, measurement: FloatArray) -> tuple[FloatArray, FloatArray]:
        mean = np.r_[measurement, np.zeros_like(measurement)]
        h = measurement[3]
        std = [
            2 * self._std_pos * h,
            2 * self._std_pos * h,
            1e-2,
            2 * self._std_pos * h,
            10 * self._std_vel * h,
            10 * self._std_vel * h,
            1e-5,
            10 * self._std_vel * h,
        ]
        return mean, np.diag(np.square(std))

    def predict(self, mean: FloatArray, cov: FloatArray) -> tuple[FloatArray, FloatArray]:
        h = mean[3]
        std = [
            self._std_pos * h,
            self._std_pos * h,
            1e-2,
            self._std_pos * h,
            self._std_vel * h,
            self._std_vel * h,
            1e-5,
            self._std_vel * h,
        ]
        motion_cov = np.diag(np.square(std))
        mean = self._motion @ mean
        cov = self._motion @ cov @ self._motion.T + motion_cov
        return mean, cov

    def project(self, mean: FloatArray, cov: FloatArray) -> tuple[FloatArray, FloatArray]:
        h = mean[3]
        std = [self._std_pos * h, self._std_pos * h, 1e-1, self._std_pos * h]
        innovation_cov = np.diag(np.square(std))
        return self._update @ mean, self._update @ cov @ self._update.T + innovation_cov

    def update(
        self, mean: FloatArray, cov: FloatArray, measurement: FloatArray
    ) -> tuple[FloatArray, FloatArray]:
        proj_mean, proj_cov = self.project(mean, cov)
        # Solve instead of inverting for numerical stability.
        gain = np.linalg.solve(proj_cov, (cov @ self._update.T).T).T
        innovation = measurement - proj_mean
        new_mean = mean + innovation @ gain.T
        new_cov = cov - gain @ proj_cov @ gain.T
        return new_mean, new_cov


def xyxy_to_xyah(box: tuple[float, float, float, float]) -> FloatArray:
    x1, y1, x2, y2 = box
    w, h = max(x2 - x1, 1e-6), max(y2 - y1, 1e-6)
    return np.array([x1 + w / 2, y1 + h / 2, w / h, h], dtype=np.float64)


def xyah_to_xyxy(state: FloatArray) -> tuple[float, float, float, float]:
    cx, cy, a, h = (float(v) for v in state[:4])
    w = a * h
    return (cx - w / 2, cy - h / 2, cx + w / 2, cy + h / 2)
