# syntax=docker/dockerfile:1.7
# Self-contained slim detector image. Builds from python:3.13-slim and
# installs only the [detector] extra (~640 MB final, vs the previous ~4.5 GB
# inheritance from rcabench-platform:latest with --all-extras).
#
# Build context must be the project root (so we can COPY pyproject.toml,
# uv.lock, src/, cli/). Run:
#   docker build -f docker/detector/Dockerfile -t docker.io/opspai/detector:slim .

# ---------- builder: full uv venv install in a throw-away stage ----------
FROM python:3.13-slim AS builder

ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never

# uv binary; nothing else needed at build time besides the python that
# python:3.13-slim already ships.
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

WORKDIR /app

# 1. Lockfile-only sync first so the venv layer caches across source edits.
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --extra detector --no-dev --frozen --no-install-project

# 2. Now copy the source the detector actually imports and install the project.
#    README.md is required because pyproject.toml's `readme = "README.md"` is
#    consulted by the build backend even though we never serve the doc.
COPY README.md ./
COPY src/ ./src/
COPY cli/detector.py ./cli/detector.py
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --extra detector --no-dev --frozen

# ---------- runtime: copy only what's needed ----------
FROM python:3.13-slim

ENV TZ=Asia/Shanghai \
    PATH="/app/.venv/bin:$PATH"
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /app

# Bring over the venv (pre-resolved with all wheels) plus the source the
# entrypoint dispatches to. No uv, no apt-get, no project metadata.
COPY --from=builder /app/.venv /app/.venv
COPY --from=builder /app/src /app/src
COPY --from=builder /app/cli /app/cli

COPY docker/detector/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
