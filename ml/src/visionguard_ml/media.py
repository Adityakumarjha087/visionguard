"""Snapshot rendering, frame ring buffer and incident clip encoding."""

from __future__ import annotations

import logging
import shutil
import subprocess
import threading
from collections import deque
from collections.abc import Callable, Iterable
from dataclasses import dataclass, field
from pathlib import Path
from typing import cast

import cv2
import numpy as np

from visionguard_ml.types import Frame, TrackState, Zone

log = logging.getLogger(__name__)

CLASS_COLORS: dict[str, tuple[int, int, int]] = {
    "person": (80, 200, 60),
    "car": (230, 160, 40),
    "truck": (230, 160, 40),
    "bus": (230, 160, 40),
    "motorcycle": (230, 160, 40),
    "helmet": (0, 220, 255),
}


def annotate(
    frame: Frame, tracks: Iterable[TrackState], zones: Iterable[Zone] = (), highlight: set[int] | None = None
) -> Frame:
    """Draw zones and boxes; highlighted tracks (the offenders) are drawn in red."""
    img = frame.copy()
    h, w = img.shape[:2]
    overlay = img.copy()
    for z in zones:
        pts = np.array([[int(x * w), int(y * h)] for x, y in z.points], dtype=np.int32)
        if z.kind == "polygon" and len(pts) >= 3:
            cv2.fillPoly(overlay, [pts], (60, 60, 220))
            cv2.polylines(img, [pts], True, (60, 60, 220), 2)
        elif len(pts) == 2:
            cv2.line(img, tuple(pts[0]), tuple(pts[1]), (255, 200, 0), 2)
    img = cast(Frame, cv2.addWeighted(overlay, 0.18, img, 0.82, 0))
    for t in tracks:
        color = (
            (0, 0, 255) if highlight and t.track_id in highlight else CLASS_COLORS.get(t.cls, (200, 200, 200))
        )
        p1, p2 = (int(t.x1), int(t.y1)), (int(t.x2), int(t.y2))
        cv2.rectangle(img, p1, p2, color, 2)
        cv2.putText(
            img,
            f"{t.cls} #{t.track_id}",
            (p1[0], max(10, p1[1] - 4)),
            cv2.FONT_HERSHEY_SIMPLEX,
            0.4,
            color,
            1,
            cv2.LINE_AA,
        )
    return img


def encode_jpeg(frame: Frame, quality: int = 80) -> bytes:
    ok, buf = cv2.imencode(".jpg", frame, [int(cv2.IMWRITE_JPEG_QUALITY), quality])
    if not ok:
        raise RuntimeError("JPEG encoding failed")
    return bytes(buf.tobytes())


@dataclass(slots=True)
class BufferedFrame:
    ts: float
    frame: Frame
    tracks: list[TrackState]


class FrameRingBuffer:
    """Keeps the last ``seconds`` of processed frames for pre-event clip footage."""

    def __init__(self, seconds: float, fps: float) -> None:
        self._buf: deque[BufferedFrame] = deque(maxlen=max(1, int(seconds * fps)))
        self._lock = threading.Lock()

    def push(self, item: BufferedFrame) -> None:
        with self._lock:
            self._buf.append(item)

    def snapshot(self) -> list[BufferedFrame]:
        with self._lock:
            return list(self._buf)


@dataclass(slots=True)
class PendingClip:
    incident_id: str
    path: Path
    until_ts: float
    frames: list[BufferedFrame]
    on_done: Callable[[str, Path | None], None]
    transform: Callable[[BufferedFrame], Frame]
    fps: float
    done: bool = field(default=False)


class ClipRecorder:
    """Collects pre-roll + post-roll frames per incident and encodes them off the hot path."""

    def __init__(self) -> None:
        self._pending: list[PendingClip] = []
        self._lock = threading.Lock()

    def start(self, clip: PendingClip) -> None:
        with self._lock:
            self._pending.append(clip)

    def feed(self, item: BufferedFrame) -> None:
        ready: list[PendingClip] = []
        with self._lock:
            for clip in self._pending:
                clip.frames.append(item)
                if item.ts >= clip.until_ts:
                    ready.append(clip)
            self._pending = [c for c in self._pending if c not in ready]
        for clip in ready:
            threading.Thread(target=self._finish, args=(clip,), daemon=True, name="clip-encoder").start()

    def flush(self) -> None:
        """Encode whatever is pending (camera stopping)."""
        with self._lock:
            pending, self._pending = self._pending, []
        for clip in pending:
            self._finish(clip)

    @property
    def pending_count(self) -> int:
        with self._lock:
            return len(self._pending)

    @staticmethod
    def _finish(clip: PendingClip) -> None:
        try:
            frames = [clip.transform(f) for f in clip.frames]
            path = write_clip(frames, clip.path, clip.fps) if frames else None
        except Exception:
            log.exception("clip encoding failed", extra={"incident_id": clip.incident_id})
            path = None
        clip.on_done(clip.incident_id, path)


def write_clip(frames: list[Frame], path: Path, fps: float) -> Path:
    """Encode H.264 MP4 (browser playable) via ffmpeg when present, else OpenCV."""
    path.parent.mkdir(parents=True, exist_ok=True)
    h, w = frames[0].shape[:2]
    ffmpeg = shutil.which("ffmpeg")
    if ffmpeg:
        cmd = [
            ffmpeg,
            "-y",
            "-loglevel",
            "error",
            "-f",
            "rawvideo",
            "-pix_fmt",
            "bgr24",
            "-s",
            f"{w}x{h}",
            "-r",
            f"{fps:.2f}",
            "-i",
            "-",
            "-c:v",
            "libx264",
            "-preset",
            "veryfast",
            "-pix_fmt",
            "yuv420p",
            "-movflags",
            "+faststart",
            str(path),
        ]
        proc = subprocess.run(
            cmd, input=b"".join(f.tobytes() for f in frames), capture_output=True, check=False
        )
        if proc.returncode == 0:
            return path
        log.warning("ffmpeg failed, falling back to OpenCV: %s", proc.stderr.decode()[:200])
    for fourcc in ("avc1", "mp4v"):
        writer = cv2.VideoWriter(str(path), cv2.VideoWriter.fourcc(*fourcc), fps, (w, h))
        if writer.isOpened():
            for f in frames:
                writer.write(f)
            writer.release()
            return path
    raise RuntimeError("no usable video encoder")
