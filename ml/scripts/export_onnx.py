"""Download YOLO11n weights (if missing) and export them to ONNX for ONNX Runtime serving.

    uv run --extra yolo python scripts/export_onnx.py [--weights models/yolo11n.pt] [--imgsz 640]
"""

from __future__ import annotations

import argparse
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--weights", type=Path, default=ROOT / "models" / "yolo11n.pt")
    parser.add_argument("--imgsz", type=int, default=640)
    parser.add_argument("--opset", type=int, default=17)
    args = parser.parse_args()

    from ultralytics import YOLO

    weights: Path = args.weights
    weights.parent.mkdir(parents=True, exist_ok=True)
    if not weights.exists():
        # Ultralytics resolves release asset names and downloads them into the CWD.
        model = YOLO(weights.name)
        downloaded = Path(weights.name)
        if downloaded.exists() and downloaded.resolve() != weights.resolve():
            shutil.move(str(downloaded), weights)
    model = YOLO(str(weights))
    out = model.export(format="onnx", imgsz=args.imgsz, opset=args.opset, simplify=True, dynamic=False)
    target = weights.with_suffix(".onnx")
    if Path(out).resolve() != target.resolve():
        shutil.move(out, target)
    print(f"exported {target} ({target.stat().st_size / 1e6:.1f} MB)")


if __name__ == "__main__":
    main()
