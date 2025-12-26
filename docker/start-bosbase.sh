#!/bin/bash
set -e

# Start bosbase
/pb/bosbase serve --http=0.0.0.0:8090 --encryptionEnv BS_ENCRYPTION_KEY

