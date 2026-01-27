#!/bin/bash

set -euo pipefail

# Source shared emulator handling functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=test-integration/hack/_emulator_handling.sh
source "${SCRIPT_DIR}/_emulator_handling.sh"

echo "Stopping all Cosmos DB emulator containers..."

# Get container runtime
CONTAINER_RUNTIME=$(get_container_runtime)
echo "Using container runtime: ${CONTAINER_RUNTIME}"

# Find all cosmos emulator containers
CONTAINERS=$(${CONTAINER_RUNTIME} ps -aq --filter "name=local-cosmos-emulator-*" 2>/dev/null || true)

if [ -z "$CONTAINERS" ]; then
    echo "No Cosmos DB emulator containers found."
    exit 0
fi

echo "Found Cosmos DB emulator containers:"
${CONTAINER_RUNTIME} ps -a --filter "name=local-cosmos-emulator-*" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# Stop and remove all containers with log saving
stop_emulator "${CONTAINER_RUNTIME}" "true"

echo "✅ All Cosmos DB emulator containers stopped and removed."
echo "Container logs saved to: ${ARTIFACT_DIR:-/tmp}"