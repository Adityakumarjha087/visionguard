from __future__ import annotations

from collections.abc import Callable, Iterator
from concurrent.futures import Executor, Future
from pathlib import Path
from typing import Any, TypeVar

import fakeredis
import pytest

from visionguard_ml.bus import EventBus
from visionguard_ml.config import Settings
from visionguard_ml.types import CameraConfig, Rule, Zone

T = TypeVar("T")


class InlineExecutor(Executor):
    """Runs submitted work synchronously so tests are deterministic."""

    def submit(self, fn: Callable[..., T], /, *args: Any, **kwargs: Any) -> Future[T]:
        fut: Future[T] = Future()
        try:
            fut.set_result(fn(*args, **kwargs))
        except Exception as exc:  # pragma: no cover - surfaced via future
            fut.set_exception(exc)
        return fut


WAREHOUSE_ZONES = (
    Zone("zr", "Restricted", "polygon", ((0.70, 0.10), (0.95, 0.10), (0.95, 0.45), (0.70, 0.45))),
    Zone("zb", "Loading bay", "polygon", ((0.04, 0.58), (0.40, 0.58), (0.40, 0.97), (0.04, 0.97))),
    Zone("za", "Assembly", "polygon", ((0.40, 0.40), (0.66, 0.40), (0.66, 0.75), (0.40, 0.75))),
    Zone("zl", "Centre line", "line", ((0.5, 0.02), (0.5, 0.98))),
)
WAREHOUSE_RULES = (
    Rule("r1", "Restricted intrusion", "zone_intrusion", "zr", severity="high"),
    Rule("r2", "Truck dwell", "dwell_time", "zb", seconds=20),
    Rule("r3", "Helmet required", "missing_ppe", None, seconds=3),
    Rule("r4", "Crowd", "crowd_count", "za", max_count=4),
    Rule("r5", "Line watch", "line_crossing", "zl", cooldown_seconds=0, severity="low"),
)


@pytest.fixture
def warehouse_camera() -> CameraConfig:
    return CameraConfig(
        id="cam-1",
        name="Warehouse",
        source_type="file",
        source_uri="samples/warehouse.mp4",
        zones=WAREHOUSE_ZONES,
        rules=WAREHOUSE_RULES,
        target_fps=10,
    )


@pytest.fixture
def redis_client() -> Iterator[fakeredis.FakeRedis]:
    client = fakeredis.FakeRedis()
    yield client
    client.flushall()


@pytest.fixture
def bus(redis_client: fakeredis.FakeRedis) -> EventBus:
    return EventBus(redis_client)


@pytest.fixture
def settings(tmp_path: Path) -> Settings:
    s = Settings(
        data_dir=tmp_path / "data", models_dir=tmp_path / "models", benchmarks_file=tmp_path / "b.json"
    )
    s.ensure_dirs()
    return s


def stream_events(client: fakeredis.FakeRedis, stream: str = "vg:events") -> list[tuple[str, dict[str, Any]]]:
    import json

    out = []
    for _id, fields in client.xrange(stream):
        f = {k.decode(): v.decode() for k, v in fields.items()}
        out.append((f.get("type", "frame"), json.loads(f["data"])))
    return out
