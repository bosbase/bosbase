#!/bin/bash
set -e

# Change to functions directory
cd /pb/functions || { echo "ERROR: Cannot cd to /pb/functions"; exit 1; }


# Set default workers if not set
WORKERS=${SCRIPT_CONCURRENCY:-1}
echo "SCRIPT WORKERS: ${WORKERS}"

# Start gunicorn
uv run gunicorn main:app -k uvicorn.workers.UvicornWorker --workers ${WORKERS} --bind 0.0.0.0:8000 --timeout 120

