#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

set -x # Turn on command tracing

# these are the default values of the emulator container.
DEFAULT_COSMOS_ENDPOINT="https://localhost:8081"
DEFAULT_COSMOS_KEY="C2y6yDjf5/R+ob0N8A7Cgv30VRDJIWEHLM+4QDU5DE2nQ9nDuVTqobD4b8mGGyPMbIZnqyMsEcaGQy67XIw/Jw=="
DEFAULT_COSMOS_CONN_STRING="AccountEndpoint=https://localhost:8081/;AccountKey=C2y6yDjf5/R+ob0N8A7Cgv30VRDJIWEHLM+4QDU5DE2nQ9nDuVTqobD4b8mGGyPMbIZnqyMsEcaGQy67XIw/Jw==;"

USE_GOTESTSUM=false
if [ -n "${ARTIFACT_DIR:-}" ]; then
  echo "artifact dir found: ${ARTIFACT_DIR}"
  USE_GOTESTSUM=true
else
    export ARTIFACT_DIR="$(mktemp -d)"
    echo "created temp artifact dir: ${ARTIFACT_DIR}"
fi

echo "simulation temp dir=${ARTIFACT_DIR}"

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
            ${CONTAINER_RUNTIME} logs "$container" > "${ARTIFACT_DIR}/${container_name}.log" 2>&1 || true
        done
        echo "Cosmos container logs saved to: $ARTIFACT_DIR"
    else
        echo "No running Cosmos emulator containers found to collect logs from"
    fi
}

# Set trap to collect logs on exit
trap cleanup EXIT

# done so that simulator tests will be skipped during normal unit testing, but fail if a connection fails.
export FRONTEND_SIMULATION_TESTING="true"
export FRONTEND_COSMOS_ENDPOINT="${DEFAULT_COSMOS_ENDPOINT}"
#TODO these are sent over HTTP, so it's only safe because the emulator is personal and well-known.  Fix the trust before sending real creds
export FRONTEND_COSMOS_KEY="${DEFAULT_COSMOS_KEY}"


if [ "${USE_GOTESTSUM}" = "true" ]; then
    echo "Running tests with gotestsum (CI mode) with ARTIFACTS in ${ARTIFACT_DIR} and GOPATH=${GOPATH}..."
    gotestsum --junitfile "${ARTIFACT_DIR}/junit-integration-test-junit.xml" -- -race github.com/Azure/ARO-HCP/test-integration/...
    sed -i 's/\(<testsuite.*\)name=""/\1 name="go-unit"/' "${ARTIFACT_DIR}/junit-integration-test-junit.xml"
else
    echo "Running tests with go test (local mode)..."
    go test -race github.com/Azure/ARO-HCP/test-integration/...
fi