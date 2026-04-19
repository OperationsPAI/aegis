#!/usr/bin/env -S uv run -s
import os
import shutil
import subprocess
from pathlib import Path

from rcabench_platform.v3.cli.main import app, logger, timeit

CONTEXTS: dict[str, Path] = {
    "rcabench-platform": Path.cwd(),
    "clickhouse_dataset": Path.cwd() / "docker/clickhouse_dataset",
    "detector": Path.cwd() / "docker/detector",
}

HARBOR_REPO = "10.10.10.240/library"


def sh(cmd: list[str]) -> None:
    try:
        subprocess.run(cmd, check=True)
    except subprocess.CalledProcessError as e:
        logger.error(f"Command '{' '.join(cmd)}' failed with exit code {e.returncode}")
        raise


def sh_out(cmd: list[str]) -> str:
    result = subprocess.run(cmd, capture_output=True, text=True, check=True)
    return result.stdout.strip()


def get_commit_hash() -> str:
    return sh_out(["git", "rev-parse", "--short", "HEAD"])


def assert_clean():
    out = sh_out(["git", "status", "--porcelain"])
    if out:
        logger.error("Working directory is not clean. Uncommitted changes detected:\n" + out)
        raise RuntimeError("Please commit or stash your changes before proceeding.")

    logger.info("Working directory is clean.")


def get_project_version() -> str:
    return sh_out(["uv", "version", "--short"])


def _build(image_name: str, image_prefix: str, no_cache: bool = False):
    logger.info(f"--- Starting build for image: {image_name} (Context: {CONTEXTS[image_name]}) ---")

    directory = CONTEXTS[image_name]
    os.chdir(directory)

    commit_hash = get_commit_hash()
    version = get_project_version()
    logger.info(f"Current commit hash: {commit_hash}, project version: {version}")

    tmp = None
    if image_name == "rcabench-platform":
        tmp = Path(".docker-build")
        tmp.mkdir(exist_ok=True)

        sh(["uv", "version", "0.0.0"])
        shutil.copyfile("pyproject.toml", tmp / "pyproject.toml")
        shutil.copyfile("uv.lock", tmp / "uv.lock")

    http_proxy = os.getenv("HTTP_PROXY", "")
    https_proxy = os.getenv("HTTPS_PROXY", "")

    cmd = ["docker", "build"]
    if no_cache:
        cmd.append("--no-cache")
    cmd.extend(["--network=host"])

    if http_proxy:
        cmd.extend(["--build-arg", f"HTTP_PROXY={http_proxy}"])

    if https_proxy:
        cmd.extend(["--build-arg", f"HTTPS_PROXY={https_proxy}"])

    # For child images, pass the base image as a build arg
    if image_name != "rcabench-platform":
        base_image = f"{image_prefix}/rcabench-platform:latest"
        cmd.extend(["--build-arg", f"BASE_IMAGE={base_image}"])
        logger.info(f"Using base image: {base_image}")

    image_full = f"{image_prefix}/{image_name}:{commit_hash}"
    cmd.extend(["-t", image_full])
    cmd.extend(["-f", "Dockerfile", "."])

    logger.info(f"Building full tag: {image_full}")

    try:
        sh(cmd)
    finally:
        if tmp:
            shutil.rmtree(tmp)
        sh(["uv", "version", version])

    image_latest = f"{image_prefix}/{image_name}:latest"
    sh(["docker", "tag", image_full, image_latest])

    logger.info(f"Successfully built image {image_full} and tagged as {image_latest}")
    logger.info(f"--- Finished build for image: {image_name} ---")


@app.command()
@timeit()
def build(
    image_name: str = "all",
    image_prefix: str = HARBOR_REPO,
    skip_clean_check: bool = False,
    no_cache: bool = False,
):
    if not skip_clean_check:
        assert_clean()

    if image_name == "all":
        logger.info(f"Building all images with prefix: {image_prefix}")
        for name in CONTEXTS:
            _build(name, image_prefix=image_prefix, no_cache=no_cache)
    else:
        _build(image_name, image_prefix=image_prefix, no_cache=no_cache)


def _push(image_name: str, image_prefix: str = HARBOR_REPO):
    logger.info(f"--- Starting push for image: {image_name} (Repo: {image_prefix}) ---")

    directory = CONTEXTS[image_name]
    os.chdir(directory)

    commit_hash = get_commit_hash()

    image_full = f"{image_prefix}/{image_name}:{commit_hash}"
    logger.info(f"Pushing full tag: {image_full}")
    sh(["docker", "push", image_full])

    image_latest = f"{image_prefix}/{image_name}:latest"
    logger.info(f"Pushing latest tag: {image_latest}")
    sh(["docker", "push", image_latest])

    logger.info(f"Successfully pushed image {image_full} and {image_latest}")
    logger.info(f"--- Finished push for image: {image_name} ---")


@app.command()
@timeit()
def push(image_name: str = "all", image_prefix: str = HARBOR_REPO):
    if image_name == "all":
        logger.info(f"Pushing all images to: {image_prefix}")
        for name in CONTEXTS:
            _push(name, image_prefix=image_prefix)
    else:
        _push(image_name, image_prefix=image_prefix)


@app.command()
@timeit()
def update_all(
    image_prefix: str = HARBOR_REPO,
    skip_clean_check: bool = False,
    no_cache: bool = False,
):
    logger.info("============== Starting FULL Image Update Process ==============")

    if not skip_clean_check:
        assert_clean()

    try:
        sh(["just", "ci"])
    except subprocess.CalledProcessError:
        logger.error("CI checks failed. Aborting the update process.")
        return

    logger.info("--- Phase 1: Building all images ---")
    build(
        "all",
        image_prefix=image_prefix,
        skip_clean_check=skip_clean_check,
        no_cache=no_cache,
    )

    logger.info("--- Phase 2: Pushing all images ---")
    push("all", image_prefix=image_prefix)

    logger.info("============== FULL Image Update Process Complete ==============")


if __name__ == "__main__":
    app()
