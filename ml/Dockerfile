# syntax=docker/dockerfile:1
# CPU inference image: ONNX Runtime + OpenCV, no PyTorch (keeps the image lean).
FROM python:3.12-slim-bookworm
ARG ONNX_PACKAGE=onnxruntime
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/* && useradd -u 10001 -m app && mkdir -p /data /models && chown app /data
COPY --from=ghcr.io/astral-sh/uv:0.12.9 /uv /usr/local/bin/uv
WORKDIR /app
COPY pyproject.toml uv.lock README.md ./
RUN uv sync --frozen --no-dev --no-install-project
COPY src ./src
COPY benchmarks ./benchmarks
RUN uv sync --frozen --no-dev \
    && if [ "$ONNX_PACKAGE" != "onnxruntime" ]; then uv pip install --python .venv "$ONNX_PACKAGE"; fi
ENV PATH="/app/.venv/bin:$PATH" VG_DATA_DIR=/data VG_MODELS_DIR=/models VG_PORT=8000
USER app
EXPOSE 8000
CMD ["visionguard-inference"]
