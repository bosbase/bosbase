#!/bin/bash
set -e

cd /app

LOG_DIR=/data
LOG_FILE="$LOG_DIR/app.log"
mkdir -p "$LOG_DIR"
# Mirror stdout/stderr to both Docker logs and a file in the mounted volume.
exec > >(tee -a "$LOG_FILE") 2>&1

WORKERS=${WEB_CONCURRENCY:-${UVICORN_WORKERS:-${GUNICORN_WORKERS:-${WORKERS:-0}}}}

if [ "$WORKERS" = "0" ] || [ -z "$WORKERS" ]; then
    CPU=$(nproc)
    MEM_GB=$(free -g | awk '/^Mem:/{print $2}')
    WORKERS=$CPU
    [ $WORKERS -gt $((MEM_GB / 3)) ] && WORKERS=$((MEM_GB / 3))
    [ $WORKERS -gt 8 ] && WORKERS=8
    [ $WORKERS -lt 1 ] && WORKERS=1
    echo "Auto-detected $WORKERS workers (CPU: $CPU, RAM: ${MEM_GB}GB)"
else
    echo "Using environment workers: $WORKERS"
fi

. /app/.venv/bin/activate

echo "Starting Python FastAPI with $WORKERS workers..."
exec gunicorn main:app \
  -k uvicorn.workers.UvicornWorker \
  -w "$WORKERS" \
  --bind 0.0.0.0:5677 \
  --timeout 120 \
  --preload \
  --access-logfile - \
  --error-logfile -
