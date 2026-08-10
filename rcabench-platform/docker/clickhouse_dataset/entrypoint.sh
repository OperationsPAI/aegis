#!/bin/bash -ex
cd /app
echo "Running ClickHouse benchmark"
LOGURU_COLORIZE=0 .venv/bin/python cli/prepare_inputs.py run
