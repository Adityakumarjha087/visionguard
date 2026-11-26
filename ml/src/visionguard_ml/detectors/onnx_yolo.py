"""YOLO (v8/11) inference on ONNX Runtime with numpy pre/post-processing.

Avoids the PyTorch dependency at serving time: letterbox -> CHW float -> session.run ->
decode ``[1, 4 + nc, N]`` -> confidence filter -> class-aware NMS -> unletterbox.
"""

from __future__ import annotations

import ast
from pathlib import Path
from typing import cast

import cv2
import numpy as np
import numpy.typing as npt

from visionguard_ml.detectors.base import DetectorUnavailableError
from visionguard_ml.types import Detection, Frame

COCO_KEEP = {"person", "bicycle", "car", "motorcycle", "bus", "truck"}


def letterbox(img: Frame, size: int = 640, color: int = 114) -> tuple[Frame, float, tuple[float, float]]:
    """Resize keeping aspect ratio and pad to ``size`` x ``size``; returns (img, scale, (pad_x, pad_y))."""
    h, w = img.shape[:2]
    r = min(size / h, size / w)
    nw, nh = round(w * r), round(h * r)
    resized = cv2.resize(img, (nw, nh), interpolation=cv2.INTER_LINEAR) if (nw, nh) != (w, h) else img
    px, py = (size - nw) / 2, (size - nh) / 2
    top, bottom = round(py - 0.1), round(py + 0.1)
    left, right = round(px - 0.1), round(px + 0.1)
    out = cv2.copyMakeBorder(resized, top, bottom, left, right, cv2.BORDER_CONSTANT, value=(color,) * 3)
    return cast(Frame, out), float(r), (float(left), float(top))


def nms(boxes: npt.NDArray[np.float32], scores: npt.NDArray[np.float32], iou_thresh: float) -> list[int]:
    """Greedy non-maximum suppression over xyxy boxes."""
    order = scores.argsort()[::-1]
    keep: list[int] = []
    areas = (boxes[:, 2] - boxes[:, 0]) * (boxes[:, 3] - boxes[:, 1])
    while order.size:
        i = int(order[0])
        keep.append(i)
        if order.size == 1:
            break
        rest = order[1:]
        xx1 = np.maximum(boxes[i, 0], boxes[rest, 0])
        yy1 = np.maximum(boxes[i, 1], boxes[rest, 1])
        xx2 = np.minimum(boxes[i, 2], boxes[rest, 2])
        yy2 = np.minimum(boxes[i, 3], boxes[rest, 3])
        inter = np.clip(xx2 - xx1, 0, None) * np.clip(yy2 - yy1, 0, None)
        iou = inter / (areas[i] + areas[rest] - inter + 1e-9)
        order = rest[iou <= iou_thresh]
    return keep


def decode(
    output: npt.NDArray[np.float32],
    names: list[str],
    conf_thresh: float,
    iou_thresh: float,
    scale: float,
    pad: tuple[float, float],
    orig_shape: tuple[int, int],
    keep_classes: set[str] | None = None,
) -> list[Detection]:
    """Decode a raw YOLOv8/11 head output of shape (1, 4+nc, N)."""
    pred = output[0].T  # (N, 4+nc)
    cls_scores = pred[:, 4:]
    cls_ids = cls_scores.argmax(axis=1)
    confs = cls_scores[np.arange(len(cls_ids)), cls_ids]
    mask = confs >= conf_thresh
    if keep_classes is not None:
        allowed = np.array([names[i] in keep_classes for i in range(len(names))])
        mask &= allowed[cls_ids]
    if not mask.any():
        return []
    xywh, confs, cls_ids = pred[mask, :4], confs[mask], cls_ids[mask]
    boxes = np.empty_like(xywh)
    boxes[:, 0] = xywh[:, 0] - xywh[:, 2] / 2
    boxes[:, 1] = xywh[:, 1] - xywh[:, 3] / 2
    boxes[:, 2] = xywh[:, 0] + xywh[:, 2] / 2
    boxes[:, 3] = xywh[:, 1] + xywh[:, 3] / 2
    # Class-aware NMS via coordinate offset trick.
    offsets = cls_ids[:, None].astype(np.float32) * 4096.0
    keep = nms(boxes + offsets, confs, iou_thresh)
    h, w = orig_shape
    out: list[Detection] = []
    for i in keep:
        x1 = float(np.clip((boxes[i, 0] - pad[0]) / scale, 0, w))
        y1 = float(np.clip((boxes[i, 1] - pad[1]) / scale, 0, h))
        x2 = float(np.clip((boxes[i, 2] - pad[0]) / scale, 0, w))
        y2 = float(np.clip((boxes[i, 3] - pad[1]) / scale, 0, h))
        out.append(Detection(x1, y1, x2, y2, float(confs[i]), names[int(cls_ids[i])]))
    return out


class OnnxYoloDetector:
    def __init__(
        self,
        path: Path,
        model_id: str,
        conf_thresh: float = 0.35,
        iou_thresh: float = 0.5,
        keep_classes: set[str] | None = None,
        threads: int = 2,
    ) -> None:
        if not path.exists():
            raise DetectorUnavailableError(f"ONNX weights not found at {path}")
        import onnxruntime as ort

        opts = ort.SessionOptions()
        opts.intra_op_num_threads = threads
        opts.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        self.session = ort.InferenceSession(str(path), opts, providers=ort.get_available_providers())
        inp = self.session.get_inputs()[0]
        self.input_name: str = inp.name
        shape = inp.shape
        self.imgsz = int(shape[2]) if isinstance(shape[2], int) else 640
        meta = self.session.get_modelmeta().custom_metadata_map
        self.names = _parse_names(meta.get("names"))
        self.model_id = model_id
        self.classes = tuple(n for n in self.names if keep_classes is None or n in keep_classes)
        self.conf_thresh, self.iou_thresh = conf_thresh, iou_thresh
        self.keep_classes = keep_classes

    def detect(self, frame: Frame) -> list[Detection]:
        img, scale, pad = letterbox(frame, self.imgsz)
        blob = cv2.cvtColor(img, cv2.COLOR_BGR2RGB).transpose(2, 0, 1)[None].astype(np.float32) / 255.0
        (output,) = self.session.run(None, {self.input_name: blob})[:1]
        return decode(
            np.asarray(output, dtype=np.float32),
            self.names,
            self.conf_thresh,
            self.iou_thresh,
            scale,
            pad,
            (frame.shape[0], frame.shape[1]),
            self.keep_classes,
        )


def _parse_names(raw: str | None) -> list[str]:
    """Ultralytics stores ``names`` as a python-literal dict string in ONNX metadata."""
    if not raw:
        return [f"class{i}" for i in range(80)]
    parsed = ast.literal_eval(raw)
    if isinstance(parsed, dict):
        return [str(parsed[k]) for k in sorted(parsed)]
    return [str(n) for n in parsed]
