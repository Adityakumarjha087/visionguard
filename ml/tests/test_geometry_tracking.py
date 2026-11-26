from __future__ import annotations

import numpy as np
import pytest

from visionguard_ml.geometry import iou_matrix, point_in_polygon, segments_intersect, side_of_line
from visionguard_ml.tracking.bytetrack import ByteTrackConfig, ByteTracker
from visionguard_ml.tracking.kalman import KalmanFilterXYAH, xyah_to_xyxy, xyxy_to_xyah
from visionguard_ml.types import Detection, TrackState

SQUARE = ((0.0, 0.0), (1.0, 0.0), (1.0, 1.0), (0.0, 1.0))


@pytest.mark.parametrize(
    ("pt", "inside"), [((0.5, 0.5), True), ((1.5, 0.5), False), ((-0.1, 0.2), False), ((0.99, 0.01), True)]
)
def test_point_in_polygon(pt: tuple[float, float], inside: bool) -> None:
    assert point_in_polygon(pt, SQUARE) is inside


def test_point_in_degenerate_polygon_is_false() -> None:
    assert point_in_polygon((0.0, 0.0), ((0, 0), (1, 1))) is False


def test_concave_polygon() -> None:
    u_shape = ((0, 0), (3, 0), (3, 3), (2, 3), (2, 1), (1, 1), (1, 3), (0, 3))
    assert point_in_polygon((1.5, 2.0), u_shape) is False
    assert point_in_polygon((0.5, 2.0), u_shape) is True


def test_segments_intersect_cases() -> None:
    assert segments_intersect((0, 0), (1, 1), (0, 1), (1, 0))
    assert not segments_intersect((0, 0), (1, 0), (0, 1), (1, 1))
    assert segments_intersect((0, 0), (1, 0), (1, 0), (2, 5))  # touching endpoint
    assert not segments_intersect((0, 0), (1, 0), (2, 0), (3, 0))  # collinear, disjoint


def test_side_of_line_sign() -> None:
    p0, p1 = (0.5, 0.0), (0.5, 1.0)  # pointing down the screen
    assert side_of_line((0.9, 0.5), p0, p1) < 0
    assert side_of_line((0.1, 0.5), p0, p1) > 0


def test_iou_matrix() -> None:
    m = iou_matrix([(0, 0, 2, 2)], [(1, 1, 3, 3), (5, 5, 6, 6), (0, 0, 2, 2)])
    assert m[0][0] == pytest.approx(1 / 7)
    assert m[0][1] == 0
    assert m[0][2] == pytest.approx(1.0)


def test_kalman_roundtrip_and_prediction() -> None:
    kf = KalmanFilterXYAH()
    box = (10.0, 20.0, 30.0, 60.0)
    mean, cov = kf.initiate(xyxy_to_xyah(box))
    assert xyah_to_xyxy(mean) == pytest.approx(box)
    for step in range(1, 6):  # moving +5px/frame in x
        mean, cov = kf.predict(mean, cov)
        mean, cov = kf.update(mean, cov, xyxy_to_xyah((10 + 5 * step, 20, 30 + 5 * step, 60)))
    pred, _ = kf.predict(mean, cov)
    assert pred[0] > mean[0]  # learned positive x velocity
    assert np.all(np.isfinite(cov))


def _det(
    x: float, y: float, conf: float = 0.9, cls: str = "person", w: float = 20, h: float = 40
) -> Detection:
    return Detection(x, y, x + w, y + h, conf, cls)


def _ids(tracks: list[TrackState]) -> dict[str, int]:
    return {t.cls: t.track_id for t in tracks}


def test_bytetrack_keeps_ids_for_moving_objects() -> None:
    tracker = ByteTracker(ByteTrackConfig(frame_rate=10))
    seen: list[set[int]] = []
    for i in range(20):
        tracks = tracker.update([_det(10 + 4 * i, 50), _det(300 - 4 * i, 200, cls="car", w=60, h=30)])
        seen.append({t.track_id for t in tracks})
    assert seen[-1] == seen[1]
    assert len(seen[-1]) == 2


def test_bytetrack_low_confidence_recovery() -> None:
    tracker = ByteTracker()
    first = None
    for i in range(5):
        first = tracker.update([_det(10 + 2 * i, 10)])
    assert first is not None
    tid = first[0].track_id
    # Occluded frames: detector confidence drops below the high threshold.
    for i in range(5, 9):
        tracks = tracker.update([_det(10 + 2 * i, 10, conf=0.3)])
        assert [t.track_id for t in tracks] == [tid]


def test_bytetrack_reacquires_lost_track() -> None:
    tracker = ByteTracker(ByteTrackConfig(frame_rate=30, track_buffer=30))
    for i in range(5):
        tracks = tracker.update([_det(100, 100 + i)])
    tid = tracks[0].track_id
    for _ in range(3):
        assert tracker.update([]) == []
    tracks = tracker.update([_det(100, 105)])
    assert tracks[0].track_id == tid


def test_bytetrack_is_class_aware_and_filters_noise() -> None:
    tracker = ByteTracker()
    tracker.update([_det(0, 0, cls="person"), _det(500, 500, conf=0.2)])
    tracks = tracker.update([_det(0, 0, cls="car")])
    assert all(t.cls == "car" for t in tracks)
    assert tracker.update([]) == []
    tracker.reset()
    assert tracker.frame_id == 0


def test_bytetrack_new_track_threshold() -> None:
    tracker = ByteTracker()
    assert tracker.update([_det(0, 0, conf=0.55)]) == []  # high but below new-track threshold
