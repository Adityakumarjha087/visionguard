"""Redis integration: desired-state config in, Streams out.

Contract (mirrored in backend/internal/bus):
  * ``vg:config``        JSON document written by the API (cameras + zones + rules + active model)
  * ``vg:frames``        stream, one entry per processed frame (ephemeral, capped)
  * ``vg:events``        stream, durable domain events consumed by the API with a consumer group
  * ``vg:status:<cam>``  JSON with a TTL, the camera's live status
  * ``vg:models``        JSON model catalogue advertised by this service
"""

from __future__ import annotations

import json
import logging
import time
from typing import Any

import redis

from visionguard_ml.types import CameraConfig, Rule, Zone

log = logging.getLogger(__name__)

CONFIG_KEY = "vg:config"
FRAMES_STREAM = "vg:frames"
EVENTS_STREAM = "vg:events"
MODELS_KEY = "vg:models"
STATUS_PREFIX = "vg:status:"


class EventBus:
    def __init__(self, client: redis.Redis, frames_maxlen: int = 2000, events_maxlen: int = 20000) -> None:
        self.r = client
        self.frames_maxlen = frames_maxlen
        self.events_maxlen = events_maxlen
        self._last_error_log = 0.0

    @classmethod
    def from_url(cls, url: str, **kw: Any) -> EventBus:
        return cls(redis.Redis.from_url(url, socket_timeout=5, socket_connect_timeout=5), **kw)

    def ping(self) -> bool:
        try:
            return bool(self.r.ping())
        except redis.RedisError:
            return False

    def _safe(self, fn: Any, *args: Any, **kwargs: Any) -> Any:
        try:
            return fn(*args, **kwargs)
        except redis.RedisError as exc:
            now = time.monotonic()
            if now - self._last_error_log > 10:
                log.warning("redis operation failed", extra={"error": str(exc)})
                self._last_error_log = now
            return None

    def publish_frame(self, message: dict[str, Any]) -> None:
        self._safe(
            self.r.xadd,
            FRAMES_STREAM,
            {"data": json.dumps(message, separators=(",", ":"))},
            maxlen=self.frames_maxlen,
            approximate=True,
        )

    def publish_event(self, event_type: str, data: dict[str, Any]) -> None:
        self._safe(
            self.r.xadd,
            EVENTS_STREAM,
            {"type": event_type, "data": json.dumps(data, separators=(",", ":"))},
            maxlen=self.events_maxlen,
            approximate=True,
        )

    def set_status(self, camera_id: str, status: dict[str, Any], ttl: int = 15) -> None:
        self._safe(self.r.set, STATUS_PREFIX + camera_id, json.dumps(status), ex=ttl)

    def publish_models(self, models: list[dict[str, Any]]) -> None:
        self._safe(self.r.set, MODELS_KEY, json.dumps(models))

    def read_config(self) -> dict[str, Any] | None:
        raw = self._safe(self.r.get, CONFIG_KEY)
        if not raw:
            return None
        try:
            doc = json.loads(raw)
        except json.JSONDecodeError:
            log.error("invalid config document in redis")
            return None
        return doc if isinstance(doc, dict) else None


def parse_config(doc: dict[str, Any]) -> tuple[str | None, list[CameraConfig]]:
    """Parse the desired-state document; malformed cameras are skipped, not fatal."""
    cameras: list[CameraConfig] = []
    for c in doc.get("cameras") or []:
        try:
            zones = tuple(
                Zone(
                    id=str(z["id"]),
                    name=str(z["name"]),
                    kind=z["kind"],
                    points=tuple((float(p[0]), float(p[1])) for p in z["points"]),
                )
                for z in c.get("zones") or []
            )
            rules = tuple(_parse_rule(r) for r in c.get("rules") or [])
            cameras.append(
                CameraConfig(
                    id=str(c["id"]),
                    name=str(c.get("name", "")),
                    source_type=c["source_type"],
                    source_uri=str(c["source_uri"]),
                    enabled=bool(c.get("enabled", True)),
                    privacy_mode=bool(c.get("privacy_mode", True)),
                    target_fps=float(c.get("target_fps") or 10),
                    zones=zones,
                    rules=rules,
                )
            )
        except (KeyError, TypeError, ValueError, IndexError) as exc:
            log.warning("skipping malformed camera config", extra={"error": str(exc)})
    model = doc.get("active_model")
    return (str(model) if model else None), cameras


def _parse_rule(r: dict[str, Any]) -> Rule:
    params = r.get("params") or {}
    return Rule(
        id=str(r["id"]),
        name=str(r["name"]),
        type=r["type"],
        zone_id=str(r["zone_id"]) if r.get("zone_id") else None,
        severity=str(r.get("severity", "medium")),
        enabled=bool(r.get("enabled", True)),
        classes=tuple(str(c) for c in params.get("classes") or ()),
        seconds=float(params["seconds"]) if params.get("seconds") is not None else None,
        max_count=int(params["max_count"]) if params.get("max_count") is not None else None,
        cooldown_seconds=float(params.get("cooldown_seconds", 30)),
    )
