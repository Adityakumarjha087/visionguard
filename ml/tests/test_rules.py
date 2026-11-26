from __future__ import annotations

from collections import Counter
from itertools import count

from visionguard_ml.detectors.color import ColorDetector
from visionguard_ml.rules.engine import RuleEngine
from visionguard_ml.samples import FPS, WAREHOUSE, SceneRenderer
from visionguard_ml.tracking.bytetrack import ByteTrackConfig, ByteTracker
from visionguard_ml.types import CameraConfig, Rule, TrackState, Zone

W, H = 100, 100
ZONE = Zone("z", "Box", "polygon", ((0.5, 0.5), (1.0, 0.5), (1.0, 1.0), (0.5, 1.0)))
LINE = Zone("l", "Line", "line", ((0.5, 0.0), (0.5, 1.0)))


def _engine(*rules: Rule, zones: tuple[Zone, ...] = (ZONE, LINE)) -> RuleEngine:
    ids = count(1)
    cam = CameraConfig("c", "Cam", "file", "x", zones=zones, rules=rules)
    return RuleEngine(cam, id_factory=lambda: f"inc-{next(ids)}")


def _t(tid: int, x: float, y: float, cls: str = "person", w: float = 4, h: float = 10) -> TrackState:
    """Track whose feet are at (x, y) in a 100x100 frame."""
    return TrackState(tid, cls, 0.9, x - w / 2, y - h, x + w / 2, y)


def test_zone_intrusion_fires_once_per_track() -> None:
    eng = _engine(Rule("r", "Intrusion", "zone_intrusion", "z", cooldown_seconds=0))
    assert eng.process([_t(1, 20, 20)], W, H, 0)[0] == []
    alerts, _ = eng.process([_t(1, 70, 70)], W, H, 1)
    assert [a.track_ids for a in alerts] == [[1]]
    assert alerts[0].incident_id == "inc-1"
    assert eng.process([_t(1, 72, 72)], W, H, 2)[0] == []
    # A second person entering still fires.
    assert len(eng.process([_t(1, 72, 72), _t(2, 80, 80)], W, H, 3)[0]) == 1


def test_zone_intrusion_ignores_other_classes_and_disabled_rules() -> None:
    eng = _engine(
        Rule("r", "Intrusion", "zone_intrusion", "z"),
        Rule("r2", "Disabled", "zone_intrusion", "z", enabled=False, classes=("car",)),
    )
    assert eng.process([_t(1, 70, 70, cls="car")], W, H, 0)[0] == []


def test_cooldown_suppresses_burst() -> None:
    eng = _engine(Rule("r", "Intrusion", "zone_intrusion", "z", cooldown_seconds=30))
    assert len(eng.process([_t(1, 70, 70)], W, H, 0)[0]) == 1
    assert eng.process([_t(1, 70, 70), _t(2, 75, 75)], W, H, 5)[0] == []
    # Track 2 was suppressed (not deferred) and a new track after cooldown fires.
    assert len(eng.process([_t(2, 75, 75), _t(3, 80, 80)], W, H, 40)[0]) == 1


def test_dwell_time_requires_stationary_object() -> None:
    eng = _engine(Rule("r", "Dwell", "dwell_time", "z", seconds=10, classes=("car",)))
    fired = []
    for sec in range(0, 9):  # moving inside the zone: no alert
        fired += eng.process([_t(1, 55 + sec * 4, 80, cls="car")], W, H, sec)[0]
    assert fired == []
    for sec in range(9, 25):
        fired += eng.process([_t(1, 87, 80, cls="car")], W, H, sec)[0]
    assert len(fired) == 1
    assert "stationary" in fired[0].message


def test_missing_ppe_with_and_without_helmet() -> None:
    eng = _engine(Rule("r", "PPE", "missing_ppe", None, seconds=2, cooldown_seconds=0))
    helmet = TrackState(9, "helmet", 0.9, 19, 10, 21, 12)
    alerts = []
    for sec in range(5):  # person 1 wears a helmet, person 2 doesn't
        alerts += eng.process([_t(1, 20, 30, h=20), helmet, _t(2, 60, 30, h=20)], W, H, sec)[0]
    assert [a.track_ids for a in alerts] == [[2]]
    assert "without helmet" in alerts[0].message


def test_crowd_count_rearms_after_dispersal() -> None:
    eng = _engine(Rule("r", "Crowd", "crowd_count", "z", max_count=2, cooldown_seconds=0))
    crowd = [_t(i, 60 + i * 5, 80) for i in range(3)]
    assert len(eng.process(crowd, W, H, 0)[0]) == 1
    assert eng.process(crowd, W, H, 1)[0] == []  # still crowded, not re-armed
    assert eng.process(crowd[:1], W, H, 2)[0] == []  # dispersed -> re-arm
    alerts = eng.process(crowd, W, H, 3)[0]
    assert len(alerts) == 1 and "Crowd of 3" in alerts[0].message


def test_line_crossing_directions_and_rule() -> None:
    eng = _engine(Rule("r", "Line", "line_crossing", "l", cooldown_seconds=0))
    eng.process([_t(1, 40, 50), _t(2, 60, 60)], W, H, 0)
    alerts, crossings = eng.process([_t(1, 60, 50), _t(2, 40, 60)], W, H, 1)
    directions = {c.track_id: c.direction for c in crossings}
    assert directions == {1: "in", 2: "out"}
    assert len(alerts) == 2


def test_stale_zone_rule_and_reconfigure() -> None:
    eng = _engine(Rule("r", "Ghost", "zone_intrusion", "missing"))
    assert eng.process([_t(1, 70, 70)], W, H, 0)[0] == []
    new_cam = CameraConfig(
        "c", "Cam", "file", "x", zones=(ZONE,), rules=(Rule("r", "Now", "zone_intrusion", "z"),)
    )
    eng.reconfigure(new_cam)
    assert len(eng.process([_t(1, 70, 70)], W, H, 1)[0]) == 1


def test_stale_tracks_are_forgotten() -> None:
    eng = _engine(Rule("r", "Intrusion", "zone_intrusion", "z", cooldown_seconds=0))
    eng.process([_t(1, 70, 70)], W, H, 0)
    eng.process([], W, H, 100)
    assert eng._tracks == {}


def test_full_synthetic_scene_fires_every_rule_type(warehouse_camera: CameraConfig) -> None:
    """End-to-end over the rendered warehouse scene: detector + ByteTrack + rules."""
    renderer = SceneRenderer(WAREHOUSE)
    detector = ColorDetector()
    tracker = ByteTracker(ByteTrackConfig(frame_rate=FPS))
    engine = RuleEngine(warehouse_camera)
    fired: Counter[str] = Counter()
    crossings: Counter[str] = Counter()
    for i in range(renderer.frame_count):
        t = i / FPS
        tracks = tracker.update(detector.detect(renderer.render(t)))
        alerts, cross = engine.process(tracks, 640, 360, t)
        fired.update(a.rule_type for a in alerts)
        crossings.update(c.direction for c in cross)
    assert set(fired) == {"zone_intrusion", "dwell_time", "missing_ppe", "crowd_count", "line_crossing"}
    assert fired["zone_intrusion"] == 1
    assert fired["missing_ppe"] == 1
    assert crossings["in"] >= 1 and crossings["out"] >= 2
