"""Measure detector latency/FPS on this machine's CPU and write benchmarks/results.json.

Compares Ultralytics PyTorch vs ONNX Runtime for YOLO11n on identical frames, plus the
weights-free backends and the full detect->track->rules pipeline. Also evaluates mAP of
the colour detector on held-out synthetic frames with exact ground truth.

    uv run --extra yolo python scripts/benchmark.py [--iters 100] [--threads 4]
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import statistics
import time
from collections.abc import Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import cv2
import numpy as np

from visionguard_ml.detectors.color import ColorDetector
from visionguard_ml.detectors.motion import MotionDetector
from visionguard_ml.detectors.onnx_yolo import COCO_KEEP, OnnxYoloDetector
from visionguard_ml.evaluation import evaluate
from visionguard_ml.rules.engine import RuleEngine
from visionguard_ml.samples import FPS, STREET, WAREHOUSE, SceneRenderer
from visionguard_ml.tracking.bytetrack import ByteTrackConfig, ByteTracker
from visionguard_ml.types import CameraConfig, Frame

ROOT = Path(__file__).resolve().parents[1]
# Published COCO val2017 numbers from the Ultralytics model card (not re-measured here).
YOLO11N_PUBLISHED = {"map50": None, "map50_95": 39.5}


def _frames() -> list[Frame]:
    """Real photos shipped with Ultralytics when available, else synthetic frames (both 640 wide)."""
    frames: list[Frame] = []
    try:
        import ultralytics

        assets = Path(ultralytics.__file__).parent / "assets"
        for name in ("bus.jpg", "zidane.jpg"):
            img = cv2.imread(str(assets / name))
            if img is not None:
                h, w = img.shape[:2]
                frames.append(cv2.resize(img, (640, round(640 * h / w))))
    except ImportError:
        pass
    renderer = SceneRenderer(WAREHOUSE)
    frames += [renderer.render(t) for t in (5.0, 15.0, 32.0)]
    return frames


def time_fn(fn: Callable[[Frame], Any], frames: list[Frame], iters: int, warmup: int = 10) -> dict[str, float]:
    for i in range(warmup):
        fn(frames[i % len(frames)])
    samples = []
    for i in range(iters):
        t0 = time.perf_counter()
        fn(frames[i % len(frames)])
        samples.append((time.perf_counter() - t0) * 1000)
    samples.sort()
    mean = statistics.fmean(samples)
    return {
        "latency_ms": round(mean, 2),
        "p50_ms": round(samples[len(samples) // 2], 2),
        "p95_ms": round(samples[int(len(samples) * 0.95) - 1], 2),
        "fps": round(1000 / mean, 1),
    }


def color_map() -> dict[str, float]:
    preds, gts = [], []
    det = ColorDetector()
    for scene in (WAREHOUSE, STREET):
        r = SceneRenderer(scene)
        for i in range(0, r.frame_count, 7):  # every 7th frame as a "val" split
            t = i / FPS
            preds.append(det.detect(r.render(t)))
            gts.append([(c, b) for c, b in r.ground_truth(t) if c in ("person", "car", "helmet")])
    res = evaluate(preds, gts)
    return {"map50": round(res["map50"] * 100, 1), "map50_95": round(res["map50_95"] * 100, 1)}


def pipeline_throughput(iters: int) -> dict[str, float]:
    renderer = SceneRenderer(WAREHOUSE)
    frames = [renderer.render(i / FPS) for i in range(min(iters, renderer.frame_count))]
    det, tracker = ColorDetector(), ByteTracker(ByteTrackConfig(frame_rate=FPS))
    engine = RuleEngine(CameraConfig("bench", "bench", "file", "x"))
    t0 = time.perf_counter()
    for i, f in enumerate(frames):
        engine.process(tracker.update(det.detect(f)), 640, 360, i / FPS)
    elapsed = time.perf_counter() - t0
    return {"latency_ms": round(elapsed / len(frames) * 1000, 2), "fps": round(len(frames) / elapsed, 1)}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--iters", type=int, default=100)
    parser.add_argument("--threads", type=int, default=4)
    parser.add_argument("--out", type=Path, default=ROOT / "benchmarks" / "results.json")
    args = parser.parse_args()

    frames = _frames()
    results: dict[str, dict[str, Any]] = {}
    notes: list[str] = []

    results["color-synthetic"] = {
        **time_fn(ColorDetector().detect, frames, args.iters),
        **color_map(),
        "source": "measured (synthetic val split)",
    }
    results["motion-mog2"] = {**time_fn(MotionDetector().detect, frames, args.iters), "source": "measured"}

    onnx_path = ROOT / "models" / "yolo11n.onnx"
    pt_path = ROOT / "models" / "yolo11n.pt"
    if pt_path.exists():
        try:
            import torch
            from ultralytics import YOLO

            torch.set_num_threads(args.threads)
            model = YOLO(str(pt_path))

            def pt_detect(f: Frame) -> Any:
                return model.predict(f, imgsz=640, conf=0.35, device="cpu", verbose=False)

            results["yolo11n"] = {**time_fn(pt_detect, frames, args.iters), **YOLO11N_PUBLISHED,
                                  "source": "latency measured; mAP published (COCO val2017)"}
        except ImportError:
            notes.append("ultralytics not installed: PyTorch benchmark skipped")
    else:
        notes.append("yolo11n.pt missing: PyTorch benchmark skipped")
    if onnx_path.exists():
        onnx_det = OnnxYoloDetector(onnx_path, "yolo11n-onnx", keep_classes=set(COCO_KEEP), threads=args.threads)
        results["yolo11n-onnx"] = {**time_fn(onnx_det.detect, frames, args.iters), **YOLO11N_PUBLISHED,
                                   "source": "latency measured; mAP published (COCO val2017)"}
    else:
        notes.append("yolo11n.onnx missing: ONNX benchmark skipped")

    ppe_metrics = ROOT / "benchmarks" / "ppe_train_metrics.json"
    ppe_onnx = ROOT / "models" / "ppe-yolo11n.onnx"
    if ppe_onnx.exists():
        ppe = OnnxYoloDetector(ppe_onnx, "ppe-yolo11n-onnx", threads=args.threads)
        entry: dict[str, Any] = {**time_fn(ppe.detect, frames, args.iters), "source": "measured"}
        if ppe_metrics.exists():
            entry.update(json.loads(ppe_metrics.read_text()))
        results["ppe-yolo11n-onnx"] = entry

    if "yolo11n" in results and "yolo11n-onnx" in results:
        speedup = results["yolo11n"]["latency_ms"] / results["yolo11n-onnx"]["latency_ms"]
        notes.append(f"ONNX Runtime speed-up over PyTorch: {speedup:.2f}x")

    doc = {
        "generated_at": datetime.now(UTC).isoformat(),
        "machine": {
            "platform": platform.platform(),
            "processor": platform.processor() or platform.machine(),
            "cpu_count": os.cpu_count(),
            "threads": args.threads,
            "python": platform.python_version(),
        },
        "iterations": args.iters,
        "frames": [f"{f.shape[1]}x{f.shape[0]}" for f in frames],
        "pipeline_color_track_rules": pipeline_throughput(300),
        "models": results,
        "notes": notes,
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(doc, indent=2) + "\n")
    print(json.dumps(doc, indent=2))
    _ = np  # keep numpy import for type side-effects in older cv2 stubs


if __name__ == "__main__":
    main()
