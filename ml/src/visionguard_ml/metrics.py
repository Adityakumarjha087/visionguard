"""Prometheus metrics exported on ``/metrics``."""

from __future__ import annotations

import os
import threading
import time

import psutil
from prometheus_client import Counter, Gauge, Histogram

FPS = Gauge("vg_inference_fps", "Processed frames per second", ["camera"])
STAGE_LATENCY = Histogram(
    "vg_inference_stage_seconds",
    "Per-frame latency by pipeline stage",
    ["camera", "stage"],
    buckets=(0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0),
)
FRAMES = Counter("vg_frames_processed_total", "Frames processed", ["camera"])
DETECTIONS = Counter("vg_detections_total", "Detections emitted", ["camera", "cls"])
ALERTS = Counter("vg_alerts_total", "Alerts raised", ["camera", "rule_type"])
CROSSINGS = Counter("vg_line_crossings_total", "Line crossings", ["camera", "direction"])
ACTIVE_TRACKS = Gauge("vg_active_tracks", "Currently tracked objects", ["camera"])
PIPELINES = Gauge("vg_pipelines_running", "Running camera pipelines")
SOURCE_ERRORS = Counter("vg_source_errors_total", "Video source read failures", ["camera"])
LLM_REQUESTS = Counter("vg_llm_requests_total", "Vision-LLM description requests", ["provider", "status"])
LLM_LATENCY = Histogram("vg_llm_latency_seconds", "Vision-LLM latency", ["provider"])
CPU_PERCENT = Gauge("vg_process_cpu_percent", "Inference process CPU usage (percent of one core)")
SYSTEM_CPU_PERCENT = Gauge("vg_system_cpu_percent", "Host/container CPU usage percent")
MEMORY_BYTES = Gauge("vg_process_memory_bytes", "Inference process RSS bytes")
GPU_UTIL = Gauge("vg_gpu_utilization_percent", "GPU utilisation (NaN when no NVIDIA GPU)", ["gpu"])
GPU_MEM = Gauge("vg_gpu_memory_used_bytes", "GPU memory used", ["gpu"])


class ResourceSampler:
    """Background sampler for CPU / memory / GPU gauges (GPU via NVML when available)."""

    def __init__(self, interval: float = 5.0) -> None:
        self.interval = interval
        self._proc = psutil.Process(os.getpid())
        self._stop = threading.Event()
        self._nvml = self._init_nvml()

    @staticmethod
    def _init_nvml() -> object | None:
        try:
            import pynvml

            pynvml.nvmlInit()
            return pynvml  # type: ignore[no-any-return]
        except Exception:
            GPU_UTIL.labels("none").set(float("nan"))
            return None

    def sample(self) -> None:
        CPU_PERCENT.set(self._proc.cpu_percent(interval=None))
        SYSTEM_CPU_PERCENT.set(psutil.cpu_percent(interval=None))
        MEMORY_BYTES.set(self._proc.memory_info().rss)
        nv = self._nvml
        if nv is not None:  # pragma: no cover - requires NVIDIA GPU
            for i in range(nv.nvmlDeviceGetCount()):  # type: ignore[attr-defined]
                handle = nv.nvmlDeviceGetHandleByIndex(i)  # type: ignore[attr-defined]
                GPU_UTIL.labels(str(i)).set(nv.nvmlDeviceGetUtilizationRates(handle).gpu)  # type: ignore[attr-defined]
                GPU_MEM.labels(str(i)).set(nv.nvmlDeviceGetMemoryInfo(handle).used)  # type: ignore[attr-defined]

    def start(self) -> None:
        def loop() -> None:
            while not self._stop.is_set():
                self.sample()
                self._stop.wait(self.interval)

        threading.Thread(target=loop, daemon=True, name="resource-sampler").start()

    def stop(self) -> None:
        self._stop.set()


def now() -> float:
    return time.time()
