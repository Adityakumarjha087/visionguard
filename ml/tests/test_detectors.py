from __future__ import annotations

import json
from pathlib import Path

import numpy as np
import pytest

from visionguard_ml.detectors import DetectorUnavailableError, ModelCatalog
from visionguard_ml.detectors.color import ColorDetector
from visionguard_ml.detectors.motion import MotionDetector
from visionguard_ml.detectors.onnx_yolo import _parse_names, decode, letterbox, nms
from visionguard_ml.samples import FPS, STREET, WAREHOUSE, SceneRenderer, ensure_samples

ML_ROOT = Path(__file__).resolve().parents[1]
REAL_ONNX = ML_ROOT / "models" / "yolo11n.onnx"


@pytest.mark.parametrize("t", [3.0, 12.0, 35.0, 40.0])
def test_color_detector_matches_scene_ground_truth(t: float) -> None:
    renderer = SceneRenderer(WAREHOUSE)
    dets = ColorDetector().detect(renderer.render(t))
    people = [d for d in dets if d.cls == "person"]
    cars = [d for d in dets if d.cls == "car"]
    assert len(people) == renderer.visible(t, "person")
    assert len(cars) == renderer.visible(t, "vehicle")
    assert all(0.5 < d.conf <= 0.99 for d in dets)


def test_color_detector_finds_helmets_only_on_helmeted_workers() -> None:
    renderer = SceneRenderer(WAREHOUSE)
    t = 16.0  # intruder (helmet), crossing worker (helmet), no-helmet worker
    dets = ColorDetector().detect(renderer.render(t))
    assert sum(d.cls == "helmet" for d in dets) == sum(d.cls == "person" for d in dets) - 1


def test_motion_detector_detects_moving_blob() -> None:
    det = MotionDetector(min_area_ratio=0.001)
    renderer = SceneRenderer(STREET)
    found = []
    for i in range(40):
        found = det.detect(renderer.render(i / FPS))
    assert found and all(d.cls == "object" for d in found)


def test_letterbox_and_decode_roundtrip() -> None:
    img = np.zeros((360, 640, 3), np.uint8)
    boxed, scale, pad = letterbox(img, 640)
    assert boxed.shape == (640, 640, 3)
    assert scale == pytest.approx(1.0) and pad == (0.0, 140.0)
    # Two overlapping "person" boxes + one "car", in letterboxed coordinates (cx, cy, w, h).
    names = ["person", "car", "dog"]
    raw = np.zeros((1, 4 + len(names), 3), np.float32)
    raw[0, :4, 0] = [100, 240, 40, 80]
    raw[0, 4, 0] = 0.9
    raw[0, :4, 1] = [102, 242, 40, 80]
    raw[0, 4, 1] = 0.8
    raw[0, :4, 2] = [400, 300, 100, 50]
    raw[0, 5, 2] = 0.7
    dets = decode(raw, names, 0.25, 0.5, scale, pad, (360, 640))
    assert sorted(d.cls for d in dets) == ["car", "person"]
    person = next(d for d in dets if d.cls == "person")
    assert person.y1 == pytest.approx(240 - 40 - 140)
    assert decode(raw, names, 0.95, 0.5, scale, pad, (360, 640)) == []
    assert [d.cls for d in decode(raw, names, 0.25, 0.5, scale, pad, (360, 640), {"car"})] == ["car"]


def test_nms_suppresses_overlaps() -> None:
    boxes = np.array([[0, 0, 10, 10], [1, 1, 10, 10], [20, 20, 30, 30]], np.float32)
    scores = np.array([0.9, 0.8, 0.7], np.float32)
    assert nms(boxes, scores, 0.5) == [0, 2]


def test_parse_names_formats() -> None:
    assert _parse_names("{0: 'person', 1: 'car'}") == ["person", "car"]
    assert _parse_names("['a', 'b']") == ["a", "b"]
    assert len(_parse_names(None)) == 80


def test_catalog_lists_and_builds_weightless_models(tmp_path: Path) -> None:
    bench = tmp_path / "bench.json"
    bench.write_text(
        json.dumps({"models": {"color-synthetic": {"latency_ms": 2.5, "fps": 400, "source": "t"}}})
    )
    cat = ModelCatalog(tmp_path, bench)
    models = {m.id: m for m in cat.list()}
    assert models["color-synthetic"].available and models["motion-mog2"].available
    assert not models["yolo11n-onnx"].available
    assert models["color-synthetic"].to_dict()["latency_ms"] == 2.5
    assert cat.create("color-synthetic").model_id == "color-synthetic"
    assert cat.create("motion-mog2").model_id == "motion-mog2"
    with pytest.raises(DetectorUnavailableError):
        cat.create("yolo11n-onnx")
    with pytest.raises(DetectorUnavailableError):
        cat.get("nope")
    assert cat.create_or_fallback("yolo11n-onnx").model_id == "color-synthetic"


def test_catalog_tolerates_bad_benchmarks(tmp_path: Path) -> None:
    bad = tmp_path / "bad.json"
    bad.write_text("{not json")
    assert ModelCatalog(tmp_path, bad).benchmarks == {}


def test_ensure_samples_is_idempotent(tmp_path: Path) -> None:
    paths = ensure_samples(tmp_path, ["street"])
    assert paths[0].stat().st_size > 10_000
    mtime = paths[0].stat().st_mtime
    ensure_samples(tmp_path, ["street"])
    assert paths[0].stat().st_mtime == mtime


@pytest.mark.real_model
@pytest.mark.skipif(
    not REAL_ONNX.exists(), reason="YOLO11n ONNX weights not present (run scripts/export_onnx.py)"
)
def test_real_onnx_yolo_runs() -> None:  # pragma: no cover - depends on weights
    cat = ModelCatalog(ML_ROOT / "models")
    det = cat.create("yolo11n-onnx")
    frame = SceneRenderer(WAREHOUSE).render(10.0)
    dets = det.detect(frame)
    assert isinstance(dets, list)
    assert all(d.cls in det.classes for d in dets)
