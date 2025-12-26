#!/bin/bash
set -e

export BOOSTER_PATH=${BOOSTER_PATH:-/pb/booster-wasm}
export BOOSTER_POOL_MAX=${BOOSTER_POOL_MAX:-64}

exec /pb/booster
