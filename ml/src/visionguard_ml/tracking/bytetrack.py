"""ByteTrack multi-object tracker (Zhang et al., 2022), class-aware, dependency-light.

Two-stage association: high-confidence detections are matched first against all tracks
(tracked + lost) by IoU, then low-confidence detections recover the remaining tracked
objects, which is what keeps IDs stable through occlusion and motion blur.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import IntEnum
from itertools import count

import numpy as np
import numpy.typing as npt
from scipy.optimize import linear_sum_assignment

from visionguard_ml.tracking.kalman import FloatArray, KalmanFilterXYAH, xyah_to_xyxy, xyxy_to_xyah
from visionguard_ml.types import Detection, TrackState


class _State(IntEnum):
    NEW = 0
    TRACKED = 1
    LOST = 2
    REMOVED = 3


@dataclass(frozen=True, slots=True)
class ByteTrackConfig:
    track_high_thresh: float = 0.5
    track_low_thresh: float = 0.1
    new_track_thresh: float = 0.6
    match_thresh: float = 0.8
    track_buffer: int = 30
    frame_rate: float = 10.0

    @property
    def max_time_lost(self) -> int:
        return max(1, int(self.frame_rate / 30.0 * self.track_buffer))


class _STrack:
    def __init__(self, det: Detection, kf: KalmanFilterXYAH) -> None:
        self.kf = kf
        self.cls = det.cls
        self.score = det.conf
        self._box = det.xyxy
        self.mean: FloatArray | None = None
        self.cov: FloatArray | None = None
        self.state = _State.NEW
        self.is_activated = False
        self.track_id = 0
        self.frame_id = 0
        self.start_frame = 0
        self.tracklet_len = 0

    @property
    def xyxy(self) -> tuple[float, float, float, float]:
        if self.mean is None:
            return self._box
        return xyah_to_xyxy(self.mean)

    def predict(self) -> None:
        assert self.mean is not None and self.cov is not None
        mean = self.mean.copy()
        if self.state != _State.TRACKED:
            mean[7] = 0.0  # freeze height velocity of lost tracks
        self.mean, self.cov = self.kf.predict(mean, self.cov)

    def activate(self, track_id: int, frame_id: int) -> None:
        self.track_id = track_id
        self.mean, self.cov = self.kf.initiate(xyxy_to_xyah(self._box))
        self.state = _State.TRACKED
        self.is_activated = frame_id == 1
        self.frame_id = frame_id
        self.start_frame = frame_id
        self.tracklet_len = 0

    def update(self, det: Detection, frame_id: int, *, reactivate: bool = False) -> None:
        assert self.mean is not None and self.cov is not None
        self.mean, self.cov = self.kf.update(self.mean, self.cov, xyxy_to_xyah(det.xyxy))
        self.state = _State.TRACKED
        self.is_activated = True
        self.score = det.conf
        self.frame_id = frame_id
        self.tracklet_len = 0 if reactivate else self.tracklet_len + 1


def _iou_cost(tracks: list[_STrack], dets: list[Detection]) -> npt.NDArray[np.float64]:
    """1 - IoU cost; cross-class pairs are made infeasible."""
    if not tracks or not dets:
        return np.zeros((len(tracks), len(dets)), dtype=np.float64)
    a = np.array([t.xyxy for t in tracks], dtype=np.float64)
    b = np.array([d.xyxy for d in dets], dtype=np.float64)
    ix1 = np.maximum(a[:, None, 0], b[None, :, 0])
    iy1 = np.maximum(a[:, None, 1], b[None, :, 1])
    ix2 = np.minimum(a[:, None, 2], b[None, :, 2])
    iy2 = np.minimum(a[:, None, 3], b[None, :, 3])
    inter = np.clip(ix2 - ix1, 0, None) * np.clip(iy2 - iy1, 0, None)
    area_a = (a[:, 2] - a[:, 0]) * (a[:, 3] - a[:, 1])
    area_b = (b[:, 2] - b[:, 0]) * (b[:, 3] - b[:, 1])
    union = area_a[:, None] + area_b[None, :] - inter
    iou = np.where(union > 0, inter / np.maximum(union, 1e-9), 0.0)
    cost = 1.0 - iou
    same_cls = np.array([[t.cls == d.cls for d in dets] for t in tracks])
    cost[~same_cls] = 1.0
    return cost


def _fuse_score(cost: npt.NDArray[np.float64], dets: list[Detection]) -> npt.NDArray[np.float64]:
    if cost.size == 0:
        return cost
    scores = np.array([d.conf for d in dets])[None, :]
    return np.asarray(1.0 - (1.0 - cost) * scores, dtype=np.float64)


def _linear_assignment(
    cost: npt.NDArray[np.float64], thresh: float
) -> tuple[list[tuple[int, int]], list[int], list[int]]:
    if cost.size == 0:
        return [], list(range(cost.shape[0])), list(range(cost.shape[1]))
    rows, cols = linear_sum_assignment(cost)
    matches = [(int(r), int(c)) for r, c in zip(rows, cols, strict=True) if cost[r, c] <= thresh]
    matched_r = {r for r, _ in matches}
    matched_c = {c for _, c in matches}
    return (
        matches,
        [r for r in range(cost.shape[0]) if r not in matched_r],
        [c for c in range(cost.shape[1]) if c not in matched_c],
    )


class ByteTracker:
    """Stateful tracker: call :meth:`update` once per frame with that frame's detections."""

    def __init__(self, config: ByteTrackConfig | None = None) -> None:
        self.config = config or ByteTrackConfig()
        self.kf = KalmanFilterXYAH()
        self.frame_id = 0
        self._ids = count(1)
        self.tracked: list[_STrack] = []
        self.lost: list[_STrack] = []

    def reset(self) -> None:
        self.__init__(self.config)  # type: ignore[misc]

    def update(self, detections: list[Detection]) -> list[TrackState]:
        cfg = self.config
        self.frame_id += 1
        high = [d for d in detections if d.conf >= cfg.track_high_thresh]
        low = [d for d in detections if cfg.track_low_thresh < d.conf < cfg.track_high_thresh]

        unconfirmed = [t for t in self.tracked if not t.is_activated]
        confirmed = [t for t in self.tracked if t.is_activated]
        pool = confirmed + self.lost
        for t in pool:
            t.predict()

        activated: list[_STrack] = []
        refound: list[_STrack] = []

        # Stage 1: high-confidence detections vs all confirmed + lost tracks.
        cost = _fuse_score(_iou_cost(pool, high), high)
        matches, u_track, u_det = _linear_assignment(cost, cfg.match_thresh)
        for ti, di in matches:
            track = pool[ti]
            if track.state == _State.TRACKED:
                track.update(high[di], self.frame_id)
                activated.append(track)
            else:
                track.update(high[di], self.frame_id, reactivate=True)
                refound.append(track)

        # Stage 2: low-confidence detections vs the still-unmatched *tracked* tracks.
        remaining = [pool[i] for i in u_track if pool[i].state == _State.TRACKED]
        cost = _iou_cost(remaining, low)
        matches, u_remaining, _ = _linear_assignment(cost, 0.5)
        for ti, di in matches:
            remaining[ti].update(low[di], self.frame_id)
            activated.append(remaining[ti])
        newly_lost = []
        for i in u_remaining:
            track = remaining[i]
            track.state = _State.LOST
            newly_lost.append(track)

        # Unconfirmed (single-frame) tracks vs leftover high detections.
        leftover = [high[i] for i in u_det]
        for t in unconfirmed:
            t.predict()
        cost = _fuse_score(_iou_cost(unconfirmed, leftover), leftover)
        matches, u_unconf, u_det2 = _linear_assignment(cost, 0.7)
        for ti, di in matches:
            unconfirmed[ti].update(leftover[di], self.frame_id)
            activated.append(unconfirmed[ti])
        for i in u_unconf:
            unconfirmed[i].state = _State.REMOVED

        # New tracks.
        for i in u_det2:
            det = leftover[i]
            if det.conf < cfg.new_track_thresh:
                continue
            track = _STrack(det, self.kf)
            track.activate(next(self._ids), self.frame_id)
            activated.append(track)

        # Bookkeeping.
        still_lost = [
            t for t in self.lost if t.state == _State.LOST and self.frame_id - t.frame_id <= cfg.max_time_lost
        ]
        self.tracked = [t for t in activated if t.state == _State.TRACKED]
        tracked_ids = {id(t) for t in self.tracked}
        refound_ids = {id(t) for t in refound}
        self.tracked += [t for t in refound if id(t) not in tracked_ids]
        self.lost = [t for t in still_lost if id(t) not in refound_ids] + [
            t for t in newly_lost if self.frame_id - t.frame_id <= cfg.max_time_lost
        ]

        return [TrackState(t.track_id, t.cls, t.score, *t.xyxy) for t in self.tracked if t.is_activated]
