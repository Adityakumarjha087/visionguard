from __future__ import annotations

import json
from typing import Any

import cv2
import httpx2
import numpy as np
import pytest

from visionguard_ml.config import Settings
from visionguard_ml.describe import (
    AnthropicDescriber,
    FakeDescriber,
    GeminiDescriber,
    IncidentContext,
    OllamaDescriber,
    OpenAIDescriber,
    ResilientDescriber,
    build_describer,
)

CTX = IncidentContext(
    camera_name="Dock",
    rule_name="Restricted",
    rule_type="zone_intrusion",
    severity="high",
    message="person #3 entered restricted zone",
    object_class="person",
    occurred_at="2026-01-01T00:00:00Z",
)


@pytest.fixture
def jpeg() -> bytes:
    ok, buf = cv2.imencode(".jpg", np.full((40, 40, 3), 120, np.uint8))
    assert ok
    return bytes(buf.tobytes())


def _mock(handler: Any) -> httpx2.Client:
    return httpx2.Client(transport=httpx2.MockTransport(handler))


def test_fake_describer_is_deterministic(jpeg: bytes) -> None:
    a, b = FakeDescriber().describe(jpeg, CTX), FakeDescriber().describe(jpeg, CTX)
    assert a == b
    assert "restricted area" in a and "Dock" in a and "well-lit" in a
    dark = cv2.imencode(".jpg", np.zeros((10, 10, 3), np.uint8))[1].tobytes()
    crowd = (
        IncidentContext(**{**CTX.__dict__, "rule_type": "crowd_count"}) if hasattr(CTX, "__dict__") else None
    )
    crowd = IncidentContext("Dock", "Crowd", "crowd_count", "low", "5 people", "person", "t")
    assert "group of people" in FakeDescriber().describe(dark, crowd).lower()
    assert "dimly lit" in FakeDescriber().describe(dark, crowd)


def test_anthropic_describer_request_shape(jpeg: bytes) -> None:
    seen: dict[str, Any] = {}

    def handler(request: httpx2.Request) -> httpx2.Response:
        seen["url"] = str(request.url)
        seen["body"] = json.loads(request.content)
        seen["beta"] = request.headers.get("anthropic-beta")
        return httpx2.Response(
            200,
            json={
                "id": "msg_1",
                "type": "message",
                "role": "assistant",
                "model": "claude-opus-5-5",
                "content": [{"type": "text", "text": "A worker entered the restricted area."}],
                "stop_reason": "end_turn",
                "stop_sequence": None,
                "usage": {"input_tokens": 10, "output_tokens": 8},
            },
        )

    d = AnthropicDescriber("test-key", "claude-opus-5-5", 5, http_client=_mock(handler))
    assert d.describe(jpeg, CTX) == "A worker entered the restricted area."
    body = seen["body"]
    assert seen["url"].endswith("/v1/messages?beta=true") or "/v1/messages" in seen["url"]
    assert body["model"] == "claude-opus-5-5"
    assert body["fallbacks"] == "default"
    assert body["output_config"] == {"effort": "low"}
    assert "temperature" not in body and "thinking" not in body
    image = body["messages"][0]["content"][0]
    assert image["type"] == "image" and image["source"]["media_type"] == "image/jpeg"
    assert "server-side-fallback-2026-07-01" in (seen["beta"] or "")


def test_anthropic_refusal_raises(jpeg: bytes) -> None:
    def handler(request: httpx2.Request) -> httpx2.Response:
        return httpx2.Response(
            200,
            json={
                "id": "msg_1",
                "type": "message",
                "role": "assistant",
                "model": "claude-opus-5-5",
                "content": [],
                "stop_reason": "refusal",
                "stop_sequence": None,
                "usage": {"input_tokens": 10, "output_tokens": 0},
            },
        )

    d = AnthropicDescriber("k", "claude-opus-5-5", 5, http_client=_mock(handler))
    with pytest.raises(RuntimeError):
        d.describe(jpeg, CTX)


def test_openai_describer(jpeg: bytes) -> None:
    def handler(request: httpx2.Request) -> httpx2.Response:
        body = json.loads(request.content)
        assert body["messages"][1]["content"][1]["image_url"]["url"].startswith("data:image/jpeg;base64,")
        return httpx2.Response(
            200,
            json={
                "id": "c1",
                "object": "chat.completion",
                "created": 0,
                "model": "gpt-4o-mini",
                "choices": [
                    {
                        "index": 0,
                        "finish_reason": "stop",
                        "message": {"role": "assistant", "content": "Person in zone."},
                    }
                ],
            },
        )

    assert (
        OpenAIDescriber("k", "gpt-4o-mini", 5, http_client=_mock(handler)).describe(jpeg, CTX)
        == "Person in zone."
    )


def test_gemini_and_ollama_describers(jpeg: bytes) -> None:
    def gemini(request: httpx2.Request) -> httpx2.Response:
        assert request.headers["x-goog-api-key"] == "g"
        return httpx2.Response(
            200, json={"candidates": [{"content": {"parts": [{"text": "Gemini says hi."}]}}]}
        )

    def ollama(request: httpx2.Request) -> httpx2.Response:
        assert json.loads(request.content)["messages"][1]["images"]
        return httpx2.Response(200, json={"message": {"content": "Llava says hi."}})

    assert GeminiDescriber("g", "gemini-2.0-flash", 5, _mock(gemini)).describe(jpeg, CTX) == "Gemini says hi."
    assert (
        OllamaDescriber("http://o:11434/", "llava", 5, _mock(ollama)).describe(jpeg, CTX) == "Llava says hi."
    )
    with pytest.raises(ValueError):
        GeminiDescriber(None, "m", 5)


def test_resilient_describer_falls_back(jpeg: bytes) -> None:
    def boom(request: httpx2.Request) -> httpx2.Response:
        return httpx2.Response(500, json={"error": "down"})

    d = ResilientDescriber(OllamaDescriber("http://o", "llava", 5, _mock(boom)))
    text, provider = d.describe_with_provider(jpeg, CTX)
    assert provider == "fake (fallback)"
    assert "Dock" in text
    assert d.provider == "ollama"


@pytest.mark.parametrize("provider", ["fake", "anthropic", "openai", "gemini", "ollama"])
def test_build_describer_selects_provider(provider: str) -> None:
    s = Settings(vision_provider=provider, anthropic_api_key="a", openai_api_key="o", gemini_api_key="g")
    assert build_describer(s).provider == provider
