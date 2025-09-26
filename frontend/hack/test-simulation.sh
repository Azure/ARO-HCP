#!/bin/bash


TMP_DATA_DIR="${ARTIFACT_DIR}"

# these are the default values of the emulator container.
DEFAULT_COSMOS_ENDPOINT="https://127.0.01:8081"
DEFAULT_COSMOS_KEY="C2y6yDjf5/R+ob0N8A7Cgv30VRDJIWEHLM+4QDU5DE2nQ9nDuVTqobD4b8mGGyPMbIZnqyMsEcaGQy67XIw/Jw=="
DEFAULT_COSMOS_CONN_STRING="AccountEndpoint=https://127.0.01:8081/;AccountKey=C2y6yDjf5/R+ob0N8A7Cgv30VRDJIWEHLM+4QDU5DE2nQ9nDuVTqobD4b8mGGyPMbIZnqyMsEcaGQy67XIw/Jw==;"

if [ -n "${ARTIFACT_DIR}" ]; then
    TMP_DATA_DIR="${ARTIFACT_DIR}/tmp"
    mkdir -p "${TMP_DATA_DIR}"
else
    TMP_DATA_DIR="$(mktemp -d)"
fi

echo "simulation temp dir=${TMP_DATA_DIR}"

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

# Cleanup function to collect cosmos logs
cleanup() {
    echo "Collecting Cosmos DB emulator logs..."

    # Find all running cosmos emulator containers
    CONTAINERS=$(${CONTAINER_RUNTIME} ps -q --filter "name=local-cosmos-emulator-*" 2>/dev/null || true)

    if [ -n "$CONTAINERS" ]; then
        for container in $CONTAINERS; do
            container_name=$(${CONTAINER_RUNTIME} inspect --format='{{.Name}}' "$container" | sed 's|^/||')
            echo "Saving logs for container: $container_name"
            ${CONTAINER_RUNTIME} logs "$container" > "${TMP_DATA_DIR}/${container_name}.log" 2>&1 || true
        done
        echo "Cosmos container logs saved to: $TMP_DATA_DIR"
    else
        echo "No running Cosmos emulator containers found to collect logs from"
    fi
}

# Set trap to collect logs on exit
trap cleanup EXIT

# Check if Cosmos emulator is already running
echo "Checking for running Cosmos DB emulator..."
if ! curl --insecure -s "${DEFAULT_COSMOS_ENDPOINT}/_explorer/emulator.pem" >/dev/null 2>&1; then
    echo "❌ No Cosmos DB emulator found running at ${DEFAULT_COSMOS_ENDPOINT}"
    echo ""
    echo "Please start a Cosmos DB emulator first by running:"
    echo "  ./frontend/hack/start-cosmos-emulator.sh"
    echo ""
    echo "Or to stop any existing emulators, run:"
    echo "  ./frontend/hack/stop-all-cosmos-emulators.sh"
#    try anyway to see if we can run in CI
#    exit 1
fi

echo "✅ Cosmos DB emulator is running at ${DEFAULT_COSMOS_ENDPOINT}"

export FRONTEND_SIMULATION_TESTING="true"
export FRONTEND_COSMOS_ENDPOINT="${DEFAULT_COSMOS_ENDPOINT}"
#TODO these are sent over HTTP, so it's only safe because the emulator is personal and well-known.  Fix the trust before sending real creds
export FRONTEND_COSMOS_KEY="${DEFAULT_COSMOS_KEY}"

go test github.com/Azure/ARO-HCP/frontend/test/simulate/...