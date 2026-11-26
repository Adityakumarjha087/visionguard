"""COCO-style mean average precision (all-point interpolation) for detector evaluation."""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Sequence

import numpy as np

from visionguard_ml.geometry import iou_matrix
from visionguard_ml.types import Detection

Box = tuple[float, float, float, float]
GroundTruth = Sequence[tuple[str, Box]]


def average_precision(recall: np.ndarray, precision: np.ndarray) -> float:
    mrec = np.concatenate(([0.0], recall, [1.0]))
    mpre = np.concatenate(([1.0], precision, [0.0]))
    mpre = np.flip(np.maximum.accumulate(np.flip(mpre)))
    idx = np.where(mrec[1:] != mrec[:-1])[0]
    return float(np.sum((mrec[idx + 1] - mrec[idx]) * mpre[idx + 1]))


def evaluate(
    predictions: Sequence[Sequence[Detection]],
    ground_truth: Sequence[GroundTruth],
    iou_thresholds: Sequence[float] = tuple(np.round(np.arange(0.5, 0.96, 0.05), 2)),
    classes: Sequence[str] | None = None,
) -> dict[str, float]:
    """Return ``{"map50": ..., "map50_95": ...}`` over images (lists aligned by index)."""
    cls_set = set(classes) if classes else {c for gt in ground_truth for c, _ in gt}
    aps: dict[float, list[float]] = defaultdict(list)
    for cls in sorted(cls_set):
        n_gt = sum(1 for gt in ground_truth for c, _ in gt if c == cls)
        if n_gt == 0:
            continue
        scored: list[tuple[float, int, Box]] = [
            (d.conf, i, d.xyxy) for i, preds in enumerate(predictions) for d in preds if d.cls == cls
        ]
        scored.sort(key=lambda s: -s[0])
        for thr in iou_thresholds:
            matched: dict[int, set[int]] = defaultdict(set)
            tp = np.zeros(len(scored))
            for k, (_, img, box) in enumerate(scored):
                gts = [b for c, b in ground_truth[img] if c == cls]
                if not gts:
                    continue
                ious = iou_matrix([box], gts)[0]
                best = int(np.argmax(ious))
                if ious[best] >= thr and best not in matched[img]:
                    matched[img].add(best)
                    tp[k] = 1
            if len(scored) == 0:
                aps[thr].append(0.0)
                continue
            ctp = np.cumsum(tp)
            recall = ctp / n_gt
            precision = ctp / (np.arange(len(scored)) + 1)
            aps[thr].append(average_precision(recall, precision))
    if not aps:
        return {"map50": 0.0, "map50_95": 0.0}
    per_thr = {t: float(np.mean(v)) for t, v in aps.items()}
    return {"map50": per_thr.get(0.5, 0.0), "map50_95": float(np.mean(list(per_thr.values())))}
