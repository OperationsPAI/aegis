#!/bin/bash -ex
cd /app
echo "Running reason (fault propagation labeler)"
LOGURU_COLORIZE=0 .venv/bin/python cli/reason.py reason "$@"
