#!/usr/bin/env bash
set -euo pipefail
echo "[INFO] Running nightly infra for svc..."

# ensure you’re in /app
cd /app

# invoke your Make target, passing DEPLOY_ENV
DEPLOY_ENV=ntly make infra.svc
DEPLOY_ENV=ntly make infra.mgmt
