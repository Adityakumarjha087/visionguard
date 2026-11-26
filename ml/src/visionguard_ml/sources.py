"""Video sources: looping files (real-time paced), RTSP streams and local webcams."""

from __future__ import annotations

import logging
import threading
import time
from collections.abc import Callable
from pathlib import Path
from typing import Any, Protocol, cast

import cv2

from visionguard_ml.types import CameraConfig, Frame

log = logging.getLogger(__name__)


class SourceError(RuntimeError):
    pass


class FrameSource(Protocol):
    frame_interval: float  # seconds between frames returned by read()

    def read(self) -> Frame | None: ...

    def close(self) -> None: ...


def resolve_file(uri: str, video_dir: Path) -> Path:
    """Resolve a file source relative to the video dir, refusing path traversal."""
    base = video_dir.resolve()
    path = (base / uri).resolve()
    if not path.is_relative_to(base):
        raise SourceError(f"file source escapes video dir: {uri}")
    if not path.exists():
        raise SourceError(f"video file not found: {uri}")
    return path


class FileSource:
    """Plays a file in a loop, sub-sampling frames to approximate ``target_fps``."""

    def __init__(self, path: Path, target_fps: float) -> None:
        if path.stat().st_size == 0:
            raise SourceError(f"video file is empty: {path.name}")
        self._cap = cv2.VideoCapture(str(path))
        if not self._cap.isOpened():
            raise SourceError(f"cannot open video file {path.name} (corrupt or unsupported codec)")
        ok, _ = self._cap.read()
        if not ok:
            self._cap.release()
            raise SourceError(f"cannot decode video file {path.name} (corrupt or unsupported codec)")
        self._cap.set(cv2.CAP_PROP_POS_FRAMES, 0)
        src_fps = self._cap.get(cv2.CAP_PROP_FPS) or 25.0
        if src_fps <= 0 or src_fps > 240:
            src_fps = 25.0
        self.step = max(1, round(src_fps / max(target_fps, 0.1)))
        self.frame_interval = self.step / src_fps
        self.path = path

    def read(self) -> Frame | None:
        frame = None
        for _ in range(self.step):
            ok, f = self._cap.read()
            if not ok:
                self._cap.set(cv2.CAP_PROP_POS_FRAMES, 0)  # loop
                ok, f = self._cap.read()
                if not ok:
                    return None
            frame = cast(Frame, f)
        return frame

    def close(self) -> None:
        self._cap.release()


class StreamSource:
    """RTSP / webcam reader. A grabber thread always keeps only the newest frame,
    so slow inference never builds up decoder latency."""

    def __init__(
        self,
        uri: str | int,
        target_fps: float,
        reconnect_delay: float = 2.0,
        read_timeout: float = 5.0,
        capture_factory: Callable[[str | int], Any] | None = None,
    ) -> None:
        self.uri = uri
        self.frame_interval = 1.0 / max(target_fps, 0.1)
        self._reconnect_delay = reconnect_delay
        self._read_timeout = read_timeout
        self._factory = capture_factory or _default_capture
        self.reconnects = 0
        self._latest: Frame | None = None
        self._seq = 0
        self._read_seq = 0
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._cap = self._open()
        self._thread = threading.Thread(target=self._grab, daemon=True, name=f"grab-{uri}")
        self._thread.start()

    def _open(self) -> Any:
        cap = self._factory(self.uri)
        if not cap.isOpened():
            raise SourceError(f"cannot open stream {_redact(self.uri)}")
        cap.set(cv2.CAP_PROP_BUFFERSIZE, 1)
        return cap

    def _grab(self) -> None:
        while not self._stop.is_set():
            ok, frame = self._cap.read()
            if not ok:
                log.warning("stream read failed; reconnecting", extra={"uri": _redact(self.uri)})
                self._cap.release()
                if self._stop.wait(self._reconnect_delay):
                    return
                try:
                    self._cap = self._open()
                    self.reconnects += 1
                except SourceError:
                    continue
                continue
            with self._lock:
                self._latest = cast(Frame, frame)
                self._seq += 1

    def read(self) -> Frame | None:
        deadline = time.monotonic() + self._read_timeout
        while time.monotonic() < deadline and not self._stop.is_set():
            with self._lock:
                if self._seq != self._read_seq and self._latest is not None:
                    self._read_seq = self._seq
                    return self._latest
            time.sleep(0.005)
        return None

    def close(self) -> None:
        self._stop.set()
        self._thread.join(timeout=2)
        self._cap.release()


def _default_capture(uri: str | int) -> Any:
    """FFmpeg capture with open/read timeouts so dead RTSP endpoints fail fast."""
    if isinstance(uri, str):
        params = [cv2.CAP_PROP_OPEN_TIMEOUT_MSEC, 5000, cv2.CAP_PROP_READ_TIMEOUT_MSEC, 5000]
        return cv2.VideoCapture(uri, cv2.CAP_FFMPEG, params)
    return cv2.VideoCapture(uri)


def _redact(uri: str | int) -> str:
    """Hide credentials embedded in RTSP URLs (rtsp://user:pass@host) from logs and status."""
    text = str(uri)
    if "@" in text and "://" in text:
        scheme, rest = text.split("://", 1)
        return f"{scheme}://***@{rest.rsplit('@', 1)[1]}"
    return text


def open_source(camera: CameraConfig, video_dir: Path) -> FrameSource:
    if camera.source_type == "file":
        return FileSource(resolve_file(camera.source_uri, video_dir), camera.target_fps)
    if camera.source_type == "webcam":
        try:
            index: int | str = int(camera.source_uri or "0")
        except ValueError:
            index = camera.source_uri
        return StreamSource(index, camera.target_fps)
    if camera.source_type == "rtsp":
        if not camera.source_uri.lower().startswith(("rtsp://", "rtsps://", "http://", "https://")):
            raise SourceError("RTSP source must be an rtsp:// or http(s):// URL")
        return StreamSource(camera.source_uri, camera.target_fps)
    raise SourceError(f"unsupported source type {camera.source_type}")
