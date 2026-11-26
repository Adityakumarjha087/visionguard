"""FastAPI surface of the inference service (internal; the browser goes through the Go API)."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Any

import uvicorn
from fastapi import FastAPI, HTTPException, Response
from fastapi.responses import StreamingResponse
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

from visionguard_ml.bus import EventBus
from visionguard_ml.config import Settings, get_settings
from visionguard_ml.describe import build_describer
from visionguard_ml.detectors import ModelCatalog
from visionguard_ml.logging_setup import configure_logging
from visionguard_ml.manager import PipelineManager
from visionguard_ml.metrics import ResourceSampler
from visionguard_ml.samples import ensure_samples

BOUNDARY = "vgframe"


def create_app(settings: Settings | None = None, manager: PipelineManager | None = None) -> FastAPI:
    settings = settings or get_settings()

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        sampler = ResourceSampler()
        owned = manager is None
        if owned:
            configure_logging(settings.log_level)
            settings.ensure_dirs()
            if settings.generate_samples:
                await asyncio.to_thread(ensure_samples, settings.video_dir / "samples")
            bus = EventBus.from_url(
                settings.redis_url,
                frames_maxlen=settings.frames_stream_maxlen,
                events_maxlen=settings.events_stream_maxlen,
            )
            app.state.manager = PipelineManager(
                settings,
                bus,
                ModelCatalog(settings.models_dir, settings.benchmarks_file),
                build_describer(settings),
            )
            app.state.manager.start()
            sampler.start()
        else:
            app.state.manager = manager
        try:
            yield
        finally:
            if owned:
                sampler.stop()
                app.state.manager.stop()

    app = FastAPI(title="VisionGuard Inference", version="0.1.0", lifespan=lifespan)

    def mgr() -> PipelineManager:
        result: PipelineManager = app.state.manager
        return result

    @app.get("/health")
    def health() -> dict[str, Any]:
        manager_ = mgr()
        redis_ok = manager_.bus.ping()
        return {
            "status": "ok" if redis_ok else "degraded",
            "redis": redis_ok,
            "pipelines": len(manager_.pipelines),
            "active_model": manager_.active_model,
        }

    @app.get("/metrics")
    def metrics() -> Response:
        return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)

    @app.get("/pipelines")
    def pipelines() -> list[dict[str, Any]]:
        return mgr().statuses()

    @app.get("/models")
    def models() -> list[dict[str, Any]]:
        return mgr().publish_catalog()

    @app.get("/snapshot/{camera_id}")
    def snapshot(camera_id: str) -> Response:
        p = mgr().get(camera_id)
        if p is None or p.latest_jpeg is None:
            raise HTTPException(status_code=404, detail="no frame available for camera")
        return Response(p.latest_jpeg, media_type="image/jpeg", headers={"Cache-Control": "no-store"})

    @app.get("/stream/{camera_id}")
    async def stream(camera_id: str) -> StreamingResponse:
        if mgr().get(camera_id) is None:
            raise HTTPException(status_code=404, detail="camera pipeline not running")

        async def frames() -> AsyncIterator[bytes]:
            last = -1
            idle = 0
            while True:
                p = mgr().get(camera_id)
                if p is None:
                    return
                if p.latest_seq != last and p.latest_jpeg is not None:
                    last, idle = p.latest_seq, 0
                    jpeg = p.latest_jpeg
                    yield (
                        (
                            f"--{BOUNDARY}\r\nContent-Type: image/jpeg\r\nContent-Length: {len(jpeg)}\r\n\r\n"
                        ).encode()
                        + jpeg
                        + b"\r\n"
                    )
                else:
                    idle += 1
                    if idle > 1500:  # ~30s without frames
                        return
                await asyncio.sleep(0.02)

        return StreamingResponse(
            frames(),
            media_type=f"multipart/x-mixed-replace; boundary={BOUNDARY}",
            headers={"Cache-Control": "no-store", "X-Accel-Buffering": "no"},
        )

    return app


def main() -> None:
    settings = get_settings()
    uvicorn.run(create_app(settings), host=settings.host, port=settings.port, log_config=None)


if __name__ == "__main__":
    main()
