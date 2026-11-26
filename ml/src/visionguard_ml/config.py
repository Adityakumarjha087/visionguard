"""Service configuration, loaded from environment variables (prefix ``VG_``)."""

from __future__ import annotations

from functools import lru_cache
from pathlib import Path
from typing import Literal

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

VisionProvider = Literal["fake", "anthropic", "openai", "gemini", "ollama"]


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="VG_", env_file=".env", extra="ignore")

    # Infrastructure
    redis_url: str = "redis://localhost:20911/0"
    host: str = "0.0.0.0"
    port: int = 8000
    log_level: str = "INFO"

    # Storage
    data_dir: Path = Path("./data")
    models_dir: Path = Path("./models")
    benchmarks_file: Path = Path("./benchmarks/results.json")

    # Pipeline
    default_model: str = "color-synthetic"
    config_poll_seconds: float = 1.0
    jpeg_quality: int = Field(default=70, ge=10, le=100)
    clip_pre_seconds: float = 4.0
    clip_post_seconds: float = 4.0
    frames_stream_maxlen: int = 2000
    events_stream_maxlen: int = 20000
    generate_samples: bool = True

    # Vision LLM for incident descriptions
    vision_provider: VisionProvider = "fake"
    vision_model: str | None = None
    vision_timeout_seconds: float = 30.0
    anthropic_api_key: str | None = None
    openai_api_key: str | None = None
    openai_base_url: str | None = None
    gemini_api_key: str | None = None
    ollama_base_url: str = "http://localhost:11434"

    @property
    def video_dir(self) -> Path:
        return self.data_dir / "videos"

    @property
    def snapshots_dir(self) -> Path:
        return self.data_dir / "media" / "snapshots"

    @property
    def clips_dir(self) -> Path:
        return self.data_dir / "media" / "clips"

    def ensure_dirs(self) -> None:
        for d in (self.video_dir / "samples", self.snapshots_dir, self.clips_dir, self.models_dir):
            d.mkdir(parents=True, exist_ok=True)


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
