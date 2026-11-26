"""Fine-tune YOLO11n for PPE (person / helmet / no_helmet) and export it to ONNX.

By default a YOLO-format dataset is generated from the synthetic scenes so the script is
runnable offline. For real footage point ``--data`` at any YOLO dataset yaml with the same
class names, e.g. the public "Hard Hat Workers" dataset exported in YOLOv8 format.

    uv run --extra yolo python scripts/train_ppe.py --epochs 20 --imgsz 640
    uv run --extra yolo python scripts/train_ppe.py --data /datasets/hardhat/data.yaml --epochs 50

Outputs: models/ppe-yolo11n.onnx and benchmarks/ppe_train_metrics.json (mAP50, mAP50-95 on val).
"""

from __future__ import annotations

import argparse
import json
import random
import shutil
from pathlib import Path

import cv2
import numpy as np

from visionguard_ml.samples import FPS, HEIGHT, SCENES, WIDTH, SceneRenderer

ROOT = Path(__file__).resolve().parents[1]
CLASSES = ["person", "helmet", "no_helmet"]


def build_synthetic_dataset(out: Path, every: int = 3, seed: int = 0) -> Path:
    """Render scenes into images/labels with light augmentation; 80/20 split by frame."""
    rng = random.Random(seed)
    for split in ("train", "val"):
        (out / "images" / split).mkdir(parents=True, exist_ok=True)
        (out / "labels" / split).mkdir(parents=True, exist_ok=True)
    n = 0
    for scene in SCENES.values():
        r = SceneRenderer(scene)
        for i in range(0, r.frame_count, every):
            t = i / FPS
            boxes = [(CLASSES.index(c), b) for c, b in r.ground_truth(t) if c in CLASSES]
            if not boxes:
                continue
            img = r.render(t)
            # Brightness/contrast jitter so the model doesn't overfit a single exposure.
            alpha, beta = rng.uniform(0.7, 1.3), rng.uniform(-30, 30)
            img = np.clip(img.astype(np.float32) * alpha + beta, 0, 255).astype(np.uint8)
            split = "val" if rng.random() < 0.2 else "train"
            name = f"{scene.name}_{i:05d}"
            cv2.imwrite(str(out / "images" / split / f"{name}.jpg"), img)
            lines = []
            for cls, (x1, y1, x2, y2) in boxes:
                cx, cy = (x1 + x2) / 2 / WIDTH, (y1 + y2) / 2 / HEIGHT
                w, h = (x2 - x1) / WIDTH, (y2 - y1) / HEIGHT
                lines.append(f"{cls} {cx:.6f} {cy:.6f} {w:.6f} {h:.6f}")
            (out / "labels" / split / f"{name}.txt").write_text("\n".join(lines) + "\n")
            n += 1
    yaml = out / "data.yaml"
    yaml.write_text(
        f"path: {out.resolve()}\ntrain: images/train\nval: images/val\n"
        + "names:\n"
        + "".join(f"  {i}: {c}\n" for i, c in enumerate(CLASSES))
    )
    print(f"synthetic PPE dataset: {n} images -> {yaml}")
    return yaml


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, help="YOLO data.yaml (default: generate synthetic)")
    parser.add_argument("--weights", default=str(ROOT / "models" / "yolo11n.pt"))
    parser.add_argument("--epochs", type=int, default=20)
    parser.add_argument("--imgsz", type=int, default=640)
    parser.add_argument("--batch", type=int, default=16)
    parser.add_argument("--device", default="cpu")
    args = parser.parse_args()

    from ultralytics import YOLO

    data = args.data or build_synthetic_dataset(ROOT / "datasets" / "ppe-synthetic")
    model = YOLO(args.weights)
    model.train(
        data=str(data),
        epochs=args.epochs,
        imgsz=args.imgsz,
        batch=args.batch,
        device=args.device,
        project=str(ROOT / "runs"),
        name="ppe",
        exist_ok=True,
        plots=False,
        verbose=False,
    )
    best = ROOT / "runs" / "ppe" / "weights" / "best.pt"
    tuned = YOLO(str(best))
    metrics = tuned.val(data=str(data), imgsz=args.imgsz, device=args.device, plots=False, verbose=False)
    result = {
        "map50": round(float(metrics.box.map50) * 100, 1),
        "map50_95": round(float(metrics.box.map) * 100, 1),
        "epochs": args.epochs,
        "dataset": "synthetic" if args.data is None else str(args.data),
        "source": f"measured (val split, {'synthetic' if args.data is None else 'custom'} PPE data)",
    }
    onnx = Path(tuned.export(format="onnx", imgsz=args.imgsz, simplify=True))
    target = ROOT / "models" / "ppe-yolo11n.onnx"
    shutil.move(str(onnx), target)
    (ROOT / "benchmarks" / "ppe_train_metrics.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({**result, "onnx": str(target)}, indent=2))


if __name__ == "__main__":
    main()
