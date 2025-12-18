#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

set -x # Turn on command tracing

# these are the default values of the emulator container.
DEFAULT_COSMOS_ENDPOINT="https://localhost:8081"

echo "Starting Cosmos DB emulator..."

# Generate random container name
CONTAINER_NAME="local-cosmos-emulator-$(shuf -i 1000-9999 -n 1)"

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

# Stop any existing emulator first
if ${CONTAINER_RUNTIME} ps -q --filter "name=local-cosmos-emulator-*" | grep -q .; then
    echo "Found existing Cosmos DB emulator containers, stopping them..."
    ${CONTAINER_RUNTIME} ps -q --filter "name=local-cosmos-emulator-*" | xargs -r ${CONTAINER_RUNTIME} stop
    ${CONTAINER_RUNTIME} ps -aq --filter "name=local-cosmos-emulator-*" | xargs -r ${CONTAINER_RUNTIME} rm
fi

echo "Starting Cosmos DB emulator with container name: ${CONTAINER_NAME}"
${CONTAINER_RUNTIME} run \
  --publish 8081:8081 \
  --publish 10250-10255:10250-10255 \
  -e AZURE_COSMOS_EMULATOR_IP_ADDRESS_OVERRIDE=127.0.0.1 \
  --name "${CONTAINER_NAME}" \
  --detach \
  mcr.microsoft.com/cosmosdb/linux/azure-cosmos-emulator:latest

# Wait for emulator to be ready by checking logs
echo "Waiting for Cosmos DB emulator to be ready..."
for i in {1..60}; do
    if ${CONTAINER_RUNTIME} logs "${CONTAINER_NAME}" 2>&1 | grep -q "Started 11/11 partitions"; then
        echo "Cosmos DB emulator is ready!"
        break
    fi
    if [ "$i" -eq 60 ]; then
        echo "Timeout waiting for Cosmos DB emulator to be ready"
        exit 1
    fi
    echo "Attempt $i/60: Waiting for emulator to start all partitions..."
    sleep 5
done

netstat -anlp

# Wait for HTTPS endpoint to be available
echo "Waiting for HTTPS endpoint to be available..."
for i in {1..30}; do
    if curl --insecure -s "${DEFAULT_COSMOS_ENDPOINT}/_explorer/emulator.pem" >/dev/null 2>&1; then
        echo "HTTPS endpoint is ready!"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "Timeout waiting for HTTPS endpoint to be available, will continue and try anyway."
        break
    fi
    echo "Attempt $i/30: Waiting for HTTPS endpoint..."
    sleep 2
done

echo "✅ Cosmos DB emulator started successfully!"
echo "Container name: ${CONTAINER_NAME}"
echo "Endpoint: ${DEFAULT_COSMOS_ENDPOINT}"
echo ""
echo "To stop all Cosmos emulators, run: ./frontend/hack/stop-all-cosmos-emulators.sh"