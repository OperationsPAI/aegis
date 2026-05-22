#!/bin/bash
set -eux
cd /app

exec .venv/bin/python -m rcabench_platform.v2.cli.container run \
    --algorithm random \
    --input-path "${INPUT_PATH}" \
    --output-path "${OUTPUT_PATH}"
