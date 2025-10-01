#!/bin/bash

set -euo pipefail

echo "Stopping all Cosmos DB emulator containers..."

# Choose container runtime (prefer podman, fallback to docker)
CONTAINER_RUNTIME=""
if command -v podman >/dev/null 2>&1; then
    CONTAINER_RUNTIME="podman"
elif command -v docker >/dev/null 2>&1; then
    CONTAINER_RUNTIME="docker"
else
    echo "Error: Neither podman nor docker found. Please install one of them."
    exit 1
fi

echo "Using container runtime: ${CONTAINER_RUNTIME}"

# Find all cosmos emulator containers
CONTAINERS=$(${CONTAINER_RUNTIME} ps -aq --filter "name=local-cosmos-emulator-*" 2>/dev/null || true)

if [ -z "$CONTAINERS" ]; then
    echo "No Cosmos DB emulator containers found."
    exit 0
fi

echo "Found Cosmos DB emulator containers:"
${CONTAINER_RUNTIME} ps -a --filter "name=local-cosmos-emulator-*" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# Save logs from running containers before stopping them
TMP_DATA_DIR="${ARTIFACT_DIR:-/tmp}"
mkdir -p "$TMP_DATA_DIR"

for container in $CONTAINERS; do
    container_name=$(${CONTAINER_RUNTIME} inspect --format='{{.Name}}' "$container" | sed 's|^/||')
    if ${CONTAINER_RUNTIME} ps -q --filter "id=$container" | grep -q .; then
        echo "Saving logs for container: $container_name"
        ${CONTAINER_RUNTIME} logs "$container" > "${TMP_DATA_DIR}/${container_name}.log" 2>&1 || true
    fi
done

# Stop and remove all containers
echo "Stopping containers..."
echo "$CONTAINERS" | xargs -r ${CONTAINER_RUNTIME} stop

echo "Removing containers..."
echo "$CONTAINERS" | xargs -r ${CONTAINER_RUNTIME} rm -v 

echo "✅ All Cosmos DB emulator containers stopped and removed."
echo "Container logs saved to: $TMP_DATA_DIR"