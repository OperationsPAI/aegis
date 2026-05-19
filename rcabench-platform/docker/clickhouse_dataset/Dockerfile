# syntax=docker/dockerfile:1.7
# Self-contained slim image for the offline dataset transform job
# (BuildDatapack pipeline). Mirrors docker/detector's structure: builds the
# venv with ONLY the extras this image actually uses (sdk + internal), then
# COPYs the resolved venv into python:3.13-slim. No ubuntu base, no
# --all-extras, no marimo/streamlit/jupyter/openai. Previous build inherited
# rcabench-platform:latest at ~1.5 GB pushed; this lands ~600 MB.
#
# Build context must be the project root so we can COPY pyproject.toml,
# uv.lock, src/, cli/. skaffold.yaml profiles use `context: rcabench-platform`
# and `dockerfile: docker/clickhouse_dataset/Dockerfile`.

# ---------- builder: full uv venv install in a throw-away stage ----------
FROM python:3.13-slim AS builder

ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never

COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

WORKDIR /app

# 1. Lockfile-only sync first so the venv layer caches across source edits.
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --extra sdk --extra internal --no-dev --frozen --no-install-project

# 2. Now copy the source and install the project.
COPY README.md ./
COPY src/ ./src/
COPY cli/prepare_inputs.py ./cli/prepare_inputs.py
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --extra sdk --extra internal --no-dev --frozen

# ---------- runtime: copy only what's needed ----------
FROM python:3.13-slim

ENV TZ=Asia/Shanghai \
    PATH="/app/.venv/bin:$PATH"
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /app

COPY --from=builder /app/.venv /app/.venv
COPY --from=builder /app/src /app/src
COPY --from=builder /app/cli /app/cli

COPY docker/clickhouse_dataset/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Optional build-time defaults that the orchestrator overrides per-job.
ARG ABNORMAL_START_VAR
ARG ABNORMAL_END_VAR
ARG NORMAL_START_VAR
ARG NORMAL_END_VAR
ENV ABNORMAL_START=${ABNORMAL_START_VAR}
ENV ABNORMAL_END=${ABNORMAL_END_VAR}
ENV NORMAL_START=${NORMAL_START_VAR}
ENV NORMAL_END=${NORMAL_END_VAR}

ENTRYPOINT ["/entrypoint.sh"]
