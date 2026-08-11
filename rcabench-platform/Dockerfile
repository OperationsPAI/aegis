FROM ubuntu:24.04

ENV TZ=Asia/Shanghai
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

RUN sed -i 's@//.*archive.ubuntu.com@//mirrors.tuna.tsinghua.edu.cn@g' /etc/apt/sources.list.d/ubuntu.sources && \
    sed -i 's@//.*security.ubuntu.com@//mirrors.tuna.tsinghua.edu.cn@g' /etc/apt/sources.list.d/ubuntu.sources

# https://docs.docker.com/build/cache/optimize/#use-cache-mounts
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt update && apt-get install -y \
    gcc curl wget git zip unzip just

ENV UV_LINK_MODE=copy

# https://docs.astral.sh/uv/guides/integration/docker/#installing-uv
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

RUN uv python install 3.13

WORKDIR /app

# Layer 1: resolve + install dependencies only — caches as long as
# pyproject.toml + uv.lock are unchanged. Previously this layer went through
# a `.docker-build/` tmp dir populated by scripts/docker.py with a
# version-zeroed pyproject so version bumps wouldn't bust the cache; that
# hack made `docker build` un-runnable by anything other than docker.py
# (incl. skaffold). The simpler form below loses cache only when the
# rcabench-platform version actually changes — a fair trade for one
# build entry point.
COPY pyproject.toml uv.lock /app/
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --all-extras --locked --no-install-project

ADD . /app

RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --all-extras --locked

CMD ["/bin/bash"]
