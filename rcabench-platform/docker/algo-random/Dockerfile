# syntax=docker/dockerfile:1.7
# Self-contained slim image for the `random` RCA algorithm. Builds from
# python:3.13-slim and installs the [sdk,internal] extras — `internal`
# pulls `rcabench.openapi`, `sdk` pulls `polars`/`pandas`/`s3fs` needed
# by the v2 container CLI's S3 staging + datapack conversion path.
#
# Build context must be `rcabench-platform/` so the COPYs resolve to
# repo-subdir paths. Run:
#   docker build -f rcabench-platform/docker/algo-random/Dockerfile \
#                -t docker.io/opspai/rca-algo-random:dev rcabench-platform

# ---------- builder: full uv venv install in a throw-away stage ----------
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

COPY docker/algo-random/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
