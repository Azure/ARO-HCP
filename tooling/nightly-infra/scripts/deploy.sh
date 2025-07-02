#!/usr/bin/env bash
set -euo pipefail

# Set environment variables
export DEPLOY_ENV=${DEPLOY_ENV:-ntly}
export AZURE_CLIENT_ID=${AZURE_CLIENT_ID}

echo "Starting nightly tasks for environment: ${DEPLOY_ENV}"

# Change to the project root (where the main Makefile is)
cd /app

# Run the nightly infrastructure tasks
echo "Running service infrastructure tasks..."
make infra.svc

echo "Running management infrastructure tasks..."
make infra.mgmt

echo "Nightly tasks completed successfully!"
