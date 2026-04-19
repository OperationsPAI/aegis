#!/bin/bash -ex
cd /app
echo "Running ts-anomaly-detector"
LOGURU_COLORIZE=0 .venv/bin/python cli/detector.py run --convert --online
