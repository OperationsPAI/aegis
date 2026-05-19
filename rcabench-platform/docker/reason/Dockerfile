# syntax=docker/dockerfile:1.7
# Self-contained slim image for the offline reasoning batch (fault
# propagation labeler). Same shape as docker/detector and
# docker/clickhouse_dataset: python:3.13-slim base + only the sdk + internal
# extras, no marimo/streamlit/jupyter/openai. Previous build inherited
# rcabench-platform:latest at ~1.5 GB pushed; this lands ~600 MB.
#
# Build context must be the project root.

# ---------- builder ----------
FROM python:3.13-slim AS builder

ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never

COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

WORKDIR /app

COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --extra sdk --extra internal --no-dev --frozen --no-install-project

COPY README.md ./
COPY src/ ./src/
COPY cli/reason.py ./cli/reason.py
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --extra sdk --extra internal --no-dev --frozen

# ---------- runtime ----------
FROM python:3.13-slim

ENV TZ=Asia/Shanghai \
    PATH="/app/.venv/bin:$PATH"
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /app

COPY --from=builder /app/.venv /app/.venv
COPY --from=builder /app/src /app/src
COPY --from=builder /app/cli /app/cli

COPY docker/reason/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
