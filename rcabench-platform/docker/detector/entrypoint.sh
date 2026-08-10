#!/bin/bash -ex
cd /app

if [ -z "${BENCHMARK_SYSTEM:-}" ]; then
    echo "ERROR: BENCHMARK_SYSTEM env var is not set." >&2
    echo "The detector cannot infer which pedestal (ts/hs/otel-demo/...) the" >&2
    echo "datapack came from, and silently defaulting to 'ts' fails on every" >&2
    echo "non-train-ticket run. The aegis backend must set BENCHMARK_SYSTEM" >&2
    echo "to the pedestal name when dispatching the detector job." >&2
    exit 2
fi

echo "Running anomaly-detector for system=${BENCHMARK_SYSTEM}"
LOGURU_COLORIZE=0 .venv/bin/python cli/detector.py run \
    --system "${BENCHMARK_SYSTEM}" --convert --online
