"""Stateful per-camera rule engine.

Consumes tracker output frame-by-frame and emits :class:`AlertEvent` and
:class:`LineCrossing` objects. All geometry is in normalised [0, 1] coordinates and every
object is anchored at its bottom-centre ("feet") point.
"""

from __future__ import annotations

import uuid
from collections import deque
from collections.abc import Callable, Iterable
from dataclasses import dataclass, field
from typing import Literal

from visionguard_ml.geometry import point_in_polygon, segments_intersect, side_of_line
from visionguard_ml.types import (
    PERSON_CLASSES,
    VEHICLE_CLASSES,
    AlertEvent,
    CameraConfig,
    LineCrossing,
    Point,
    Rule,
    TrackState,
    Zone,
)

DEFAULT_CLASSES: dict[str, frozenset[str]] = {
    "zone_intrusion": PERSON_CLASSES,
    "dwell_time": VEHICLE_CLASSES,
    "missing_ppe": PERSON_CLASSES,
    "crowd_count": PERSON_CLASSES,
    "line_crossing": PERSON_CLASSES | VEHICLE_CLASSES,
}
STATIONARY_RADIUS = 0.015  # normalised distance considered "not moving"
TRACK_TTL_SECONDS = 10.0


@dataclass(slots=True)
class _TrackMemory:
    cls: str
    first_seen: float
    last_seen: float
    anchor: Point
    history: deque[tuple[float, Point]] = field(default_factory=lambda: deque(maxlen=4000))
    last_helmet_seen: float | None = None


@dataclass(slots=True)
class _RuleMemory:
    last_fired: float = float("-inf")
    fired_tracks: set[int] = field(default_factory=set)
    armed: bool = True  # crowd rules re-arm when the count drops back below the threshold


def _new_id() -> str:
    return str(uuid.uuid4())


class RuleEngine:
    def __init__(self, camera: CameraConfig, id_factory: Callable[[], str] = _new_id) -> None:
        self.camera = camera
        self._id_factory = id_factory
        self._tracks: dict[int, _TrackMemory] = {}
        self._rule_mem: dict[str, _RuleMemory] = {}
        self._zones: dict[str, Zone] = {z.id: z for z in camera.zones}

    def reconfigure(self, camera: CameraConfig) -> None:
        """Swap zones/rules without losing track history (rule memory resets for changed rules)."""
        old_rules = {r.id: r for r in self.camera.rules}
        self.camera = camera
        self._zones = {z.id: z for z in camera.zones}
        for r in camera.rules:
            if old_rules.get(r.id) != r:
                self._rule_mem.pop(r.id, None)

    # ------------------------------------------------------------------ public API
    def process(
        self, tracks: Iterable[TrackState], width: int, height: int, ts: float
    ) -> tuple[list[AlertEvent], list[LineCrossing]]:
        tracks = list(tracks)
        prev_anchor = {tid: m.anchor for tid, m in self._tracks.items()}
        self._update_memory(tracks, width, height, ts)

        crossings = self._line_crossings(tracks, prev_anchor, ts)
        alerts: list[AlertEvent] = []
        for rule in self.camera.rules:
            if not rule.enabled:
                continue
            zone = self._zones.get(rule.zone_id) if rule.zone_id else None
            if rule.zone_id and zone is None:
                continue  # zone deleted; ignore stale rule
            alerts.extend(self._evaluate(rule, zone, tracks, crossings, ts))
        return alerts, crossings

    # ------------------------------------------------------------------ internals
    def _update_memory(self, tracks: list[TrackState], width: int, height: int, ts: float) -> None:
        helmets = [t for t in tracks if t.cls == "helmet"]
        for t in tracks:
            anchor = t.anchor(width, height)
            mem = self._tracks.get(t.track_id)
            if mem is None:
                mem = _TrackMemory(cls=t.cls, first_seen=ts, last_seen=ts, anchor=anchor)
                self._tracks[t.track_id] = mem
            mem.last_seen, mem.anchor = ts, anchor
            mem.history.append((ts, anchor))
            if t.cls in PERSON_CLASSES and _has_helmet(t, helmets):
                mem.last_helmet_seen = ts
        stale = [tid for tid, m in self._tracks.items() if ts - m.last_seen > TRACK_TTL_SECONDS]
        for tid in stale:
            del self._tracks[tid]
            for rm in self._rule_mem.values():
                rm.fired_tracks.discard(tid)

    def _line_crossings(
        self, tracks: list[TrackState], prev_anchor: dict[int, Point], ts: float
    ) -> list[LineCrossing]:
        out: list[LineCrossing] = []
        lines = [z for z in self._zones.values() if z.kind == "line" and len(z.points) == 2]
        for t in tracks:
            if t.cls == "helmet" or t.track_id not in prev_anchor:
                continue
            a0, a1 = prev_anchor[t.track_id], self._tracks[t.track_id].anchor
            for line in lines:
                p0, p1 = line.points
                if a0 == a1 or not segments_intersect(a0, a1, p0, p1):
                    continue
                s0, s1 = side_of_line(a0, p0, p1), side_of_line(a1, p0, p1)
                if s1 in (s0, 0):
                    continue
                direction: Literal["in", "out"] = "in" if s1 < 0 else "out"
                out.append(LineCrossing(self.camera.id, line.id, t.track_id, t.cls, direction, ts))
        return out

    def _classes(self, rule: Rule) -> frozenset[str]:
        return frozenset(rule.classes) if rule.classes else DEFAULT_CLASSES[rule.type]

    def _in_zone(self, track_id: int, zone: Zone | None) -> bool:
        if zone is None:
            return True
        return zone.kind == "polygon" and point_in_polygon(self._tracks[track_id].anchor, zone.points)

    def _evaluate(
        self,
        rule: Rule,
        zone: Zone | None,
        tracks: list[TrackState],
        crossings: list[LineCrossing],
        ts: float,
    ) -> list[AlertEvent]:
        mem = self._rule_mem.setdefault(rule.id, _RuleMemory())
        classes = self._classes(rule)
        candidates = [t for t in tracks if t.cls in classes]

        if rule.type == "crowd_count":
            inside = [t for t in candidates if self._in_zone(t.track_id, zone)]
            limit = rule.max_count if rule.max_count is not None else 5
            if len(inside) <= limit:
                mem.armed = True
                return []
            if not mem.armed or ts - mem.last_fired < rule.cooldown_seconds:
                return []
            mem.armed = False
            where = f" in {zone.name}" if zone else ""
            return [
                self._fire(rule, mem, inside, ts, f"Crowd of {len(inside)} people{where} (limit {limit})")
            ]

        if rule.type == "line_crossing":
            hits = [c for c in crossings if c.zone_id == rule.zone_id and c.cls in classes]
            events = []
            for c in hits:
                if ts - mem.last_fired < rule.cooldown_seconds:
                    break
                track = next(t for t in tracks if t.track_id == c.track_id)
                events.append(
                    self._fire(
                        rule,
                        mem,
                        [track],
                        ts,
                        f"{c.cls} #{c.track_id} crossed {_zname(zone)} ({c.direction})",
                    )
                )
            return events

        events = []
        for t in candidates:
            if t.track_id in mem.fired_tracks or not self._in_zone(t.track_id, zone):
                continue
            message = self._violation(rule, zone, t, ts)
            if message is None:
                continue
            if ts - mem.last_fired < rule.cooldown_seconds:
                # Suppressed by cooldown, but remember the track so it doesn't fire later.
                mem.fired_tracks.add(t.track_id)
                continue
            events.append(self._fire(rule, mem, [t], ts, message))
        return events

    def _violation(self, rule: Rule, zone: Zone | None, t: TrackState, ts: float) -> str | None:
        mem = self._tracks[t.track_id]
        if rule.type == "zone_intrusion":
            return f"{t.cls} #{t.track_id} entered restricted zone {_zname(zone)}"
        if rule.type == "dwell_time":
            threshold = rule.seconds if rule.seconds is not None else 120.0
            still = self._stationary_seconds(mem, ts)
            if still >= threshold:
                return (
                    f"{t.cls} #{t.track_id} stationary for {still:.0f}s in {_zname(zone)} "
                    f"(limit {threshold:.0f}s)"
                )
            return None
        if rule.type == "missing_ppe":
            grace = rule.seconds if rule.seconds is not None else 3.0
            since = mem.last_helmet_seen if mem.last_helmet_seen is not None else mem.first_seen
            if ts - since >= grace and ts - mem.first_seen >= grace:
                return f"worker #{t.track_id} without helmet for {ts - since:.0f}s"
            return None
        return None

    @staticmethod
    def _stationary_seconds(mem: _TrackMemory, ts: float) -> float:
        cx, cy = mem.anchor
        start = ts
        for t, (x, y) in reversed(mem.history):
            if (x - cx) ** 2 + (y - cy) ** 2 > STATIONARY_RADIUS**2:
                break
            start = t
        return ts - start

    def _fire(
        self, rule: Rule, mem: _RuleMemory, tracks: list[TrackState], ts: float, message: str
    ) -> AlertEvent:
        mem.last_fired = ts
        mem.fired_tracks.update(t.track_id for t in tracks)
        classes = sorted({t.cls for t in tracks})
        return AlertEvent(
            incident_id=self._id_factory(),
            camera_id=self.camera.id,
            rule_id=rule.id,
            rule_name=rule.name,
            rule_type=rule.type,
            severity=rule.severity,
            ts=ts,
            track_ids=[t.track_id for t in tracks],
            object_class=",".join(classes),
            message=message,
            zone_id=rule.zone_id,
        )


def _zname(zone: Zone | None) -> str:
    return f"'{zone.name}'" if zone else "frame"


def _has_helmet(person: TrackState, helmets: list[TrackState]) -> bool:
    """A helmet counts if its centre lies inside the top 40% of the person's box."""
    head_bottom = person.y1 + 0.4 * (person.y2 - person.y1)
    for h in helmets:
        cx, cy = (h.x1 + h.x2) / 2, (h.y1 + h.y2) / 2
        if person.x1 <= cx <= person.x2 and person.y1 - 6 <= cy <= head_bottom:
            return True
    return False
