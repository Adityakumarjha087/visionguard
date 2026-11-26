"""Vision-LLM incident descriptions behind one provider-agnostic interface.

``VG_VISION_PROVIDER`` selects the backend: ``fake`` (default, deterministic, offline),
``anthropic``, ``openai``, ``gemini`` or ``ollama``. Any provider failure degrades to the
fake describer so an incident always gets a description.
"""

from __future__ import annotations

import base64
import logging
import time
from dataclasses import dataclass
from typing import Any, Protocol

import cv2
import httpx2
import numpy as np

from visionguard_ml.config import Settings
from visionguard_ml.metrics import LLM_LATENCY, LLM_REQUESTS

log = logging.getLogger(__name__)

SYSTEM_PROMPT = (
    "You are a security operations assistant reviewing a CCTV alert snapshot. "
    "Write a factual 1-3 sentence incident description for an operator: what is happening, "
    "where in the frame, and why it matters for the triggered rule. Do not speculate about "
    "identity, age, gender or ethnicity. Faces may be blurred for privacy."
)

DEFAULT_MODELS = {
    "anthropic": "claude-opus-5-5",
    "openai": "gpt-4o-mini",
    "gemini": "gemini-2.0-flash",
    "ollama": "llava",
}


@dataclass(frozen=True, slots=True)
class IncidentContext:
    camera_name: str
    rule_name: str
    rule_type: str
    severity: str
    message: str
    object_class: str
    occurred_at: str

    def prompt(self) -> str:
        return (
            f"Camera: {self.camera_name}\n"
            f"Rule: {self.rule_name} ({self.rule_type}, severity {self.severity})\n"
            f"Detector message: {self.message}\nObject class: {self.object_class}\nTime: {self.occurred_at}\n"
            "Offending objects are outlined in red. Describe the incident."
        )


class VisionDescriber(Protocol):
    provider: str

    def describe(self, jpeg: bytes, ctx: IncidentContext) -> str: ...


class FakeDescriber:
    """Deterministic, offline describer: template + simple image statistics."""

    provider = "fake"

    def describe(self, jpeg: bytes, ctx: IncidentContext) -> str:
        img = cv2.imdecode(np.frombuffer(jpeg, np.uint8), cv2.IMREAD_GRAYSCALE)
        light = "a dimly lit" if img is not None and float(img.mean()) < 70 else "a well-lit"
        actions = {
            "zone_intrusion": "entered a restricted area",
            "dwell_time": "has remained stationary beyond the allowed time",
            "missing_ppe": "is not wearing a required safety helmet",
            "crowd_count": "are gathered above the permitted occupancy",
            "line_crossing": "crossed a monitored line",
        }
        subject = (
            "A group of people" if ctx.rule_type == "crowd_count" else f"A {ctx.object_class or 'object'}"
        )
        action = actions.get(ctx.rule_type, "triggered a rule")
        return (
            f"{subject} {action} on camera '{ctx.camera_name}' in {light} scene "
            f"({ctx.message}). Rule '{ctx.rule_name}' raised a {ctx.severity}-severity alert; "
            "the highlighted object is outlined in red. Operator review recommended."
        )


class AnthropicDescriber:
    provider = "anthropic"

    def __init__(
        self, api_key: str | None, model: str, timeout: float, http_client: httpx2.Client | None = None
    ):
        import anthropic

        self._anthropic = anthropic
        self.model = model
        self.client = anthropic.Anthropic(
            api_key=api_key, timeout=timeout, max_retries=1, http_client=http_client
        )

    def describe(self, jpeg: bytes, ctx: IncidentContext) -> str:
        response = self.client.beta.messages.create(
            model=self.model,
            max_tokens=1024,
            betas=["server-side-fallback-2026-07-01"],
            fallbacks="default",
            output_config={"effort": "low"},
            system=SYSTEM_PROMPT,
            messages=[
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "image",
                            "source": {"type": "base64", "media_type": "image/jpeg", "data": _b64(jpeg)},
                        },
                        {"type": "text", "text": ctx.prompt()},
                    ],
                }
            ],
        )
        if response.stop_reason == "refusal":
            raise RuntimeError("vision model declined the request")
        text = " ".join(b.text for b in response.content if b.type == "text").strip()
        if not text:
            raise RuntimeError("empty response from vision model")
        return text


class OpenAIDescriber:
    provider = "openai"

    def __init__(
        self,
        api_key: str | None,
        model: str,
        timeout: float,
        base_url: str | None = None,
        http_client: httpx2.Client | None = None,
    ):
        import openai

        self.model = model
        self.client = openai.OpenAI(
            api_key=api_key, base_url=base_url, timeout=timeout, max_retries=1, http_client=http_client
        )

    def describe(self, jpeg: bytes, ctx: IncidentContext) -> str:
        response = self.client.chat.completions.create(
            model=self.model,
            max_tokens=300,
            messages=[
                {"role": "system", "content": SYSTEM_PROMPT},
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": ctx.prompt()},
                        {"type": "image_url", "image_url": {"url": f"data:image/jpeg;base64,{_b64(jpeg)}"}},
                    ],
                },
            ],
        )
        text = (response.choices[0].message.content or "").strip()
        if not text:
            raise RuntimeError("empty response from vision model")
        return text


class GeminiDescriber:
    """Google Gemini via its REST ``generateContent`` endpoint."""

    provider = "gemini"
    base_url = "https://generativelanguage.googleapis.com/v1beta"

    def __init__(
        self, api_key: str | None, model: str, timeout: float, http_client: httpx2.Client | None = None
    ):
        if not api_key:
            raise ValueError("VG_GEMINI_API_KEY is required for the gemini provider")
        self.api_key, self.model = api_key, model
        self.http = http_client or httpx2.Client(timeout=timeout)

    def describe(self, jpeg: bytes, ctx: IncidentContext) -> str:
        body = {
            "systemInstruction": {"parts": [{"text": SYSTEM_PROMPT}]},
            "contents": [
                {
                    "parts": [
                        {"text": ctx.prompt()},
                        {"inlineData": {"mimeType": "image/jpeg", "data": _b64(jpeg)}},
                    ]
                }
            ],
            "generationConfig": {"maxOutputTokens": 300},
        }
        resp = self.http.post(
            f"{self.base_url}/models/{self.model}:generateContent",
            headers={"x-goog-api-key": self.api_key},
            json=body,
        )
        resp.raise_for_status()
        data: dict[str, Any] = resp.json()
        parts = data["candidates"][0]["content"]["parts"]
        return " ".join(str(p.get("text", "")) for p in parts).strip()


class OllamaDescriber:
    """Local vision model (e.g. llava) through Ollama's ``/api/chat``."""

    provider = "ollama"

    def __init__(self, base_url: str, model: str, timeout: float, http_client: httpx2.Client | None = None):
        self.base_url, self.model = base_url.rstrip("/"), model
        self.http = http_client or httpx2.Client(timeout=timeout)

    def describe(self, jpeg: bytes, ctx: IncidentContext) -> str:
        resp = self.http.post(
            f"{self.base_url}/api/chat",
            json={
                "model": self.model,
                "stream": False,
                "messages": [
                    {"role": "system", "content": SYSTEM_PROMPT},
                    {"role": "user", "content": ctx.prompt(), "images": [_b64(jpeg)]},
                ],
            },
        )
        resp.raise_for_status()
        return str(resp.json()["message"]["content"]).strip()


class ResilientDescriber:
    """Wraps a provider with metrics and a deterministic fallback."""

    def __init__(self, primary: VisionDescriber, fallback: VisionDescriber | None = None) -> None:
        self.primary = primary
        self.fallback = fallback or FakeDescriber()

    @property
    def provider(self) -> str:
        return self.primary.provider

    def describe_with_provider(self, jpeg: bytes, ctx: IncidentContext) -> tuple[str, str]:
        start = time.perf_counter()
        try:
            text = self.primary.describe(jpeg, ctx)
            LLM_REQUESTS.labels(self.primary.provider, "ok").inc()
            return text, self.primary.provider
        except Exception as exc:
            LLM_REQUESTS.labels(self.primary.provider, "error").inc()
            log.warning(
                "vision provider failed; using fallback",
                extra={"provider": self.primary.provider, "error": str(exc)},
            )
            return self.fallback.describe(jpeg, ctx), f"{self.fallback.provider} (fallback)"
        finally:
            LLM_LATENCY.labels(self.primary.provider).observe(time.perf_counter() - start)


def build_describer(settings: Settings, http_client: httpx2.Client | None = None) -> ResilientDescriber:
    provider = settings.vision_provider
    model = settings.vision_model or DEFAULT_MODELS.get(provider, "")
    timeout = settings.vision_timeout_seconds
    primary: VisionDescriber
    if provider == "anthropic":
        primary = AnthropicDescriber(settings.anthropic_api_key, model, timeout, http_client)
    elif provider == "openai":
        primary = OpenAIDescriber(
            settings.openai_api_key, model, timeout, settings.openai_base_url, http_client
        )
    elif provider == "gemini":
        primary = GeminiDescriber(settings.gemini_api_key, model, timeout, http_client)
    elif provider == "ollama":
        primary = OllamaDescriber(settings.ollama_base_url, model, timeout, http_client)
    else:
        primary = FakeDescriber()
    return ResilientDescriber(primary)


def _b64(data: bytes) -> str:
    return base64.standard_b64encode(data).decode("ascii")
