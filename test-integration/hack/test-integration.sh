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

# The kube-applier integration tests need envtest's kube-apiserver + etcd
# binaries. The repo's top-level Makefile owns the download/cache logic;
# call into it so this script and `make test-unit` use the same path.
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
KUBEBUILDER_ASSETS="$(make -C "${REPO_ROOT}" -s envtest-setup)"
export KUBEBUILDER_ASSETS
echo "envtest binaries: ${KUBEBUILDER_ASSETS}"

# In CI mode, compile every integration test binary in parallel before the serial run.
# CI pods start with a cold build cache, so this pays for compilation once, up front.
# Local runs skip it: a warm cache gains nothing and the extra link pass only adds time.
# -exec /bin/true with -run '^$' builds the binaries without executing any test, and
# -race matches the serial run, so the serial -p 1 invocation reuses the warm cache.
if [ "${USE_GOTESTSUM}" = "true" ]; then
  # Prewarming is an optimization. A failure, including scratch exhaustion, falls back to
  # the serial build below, which keeps responsibility for the test result and JUnit report.
  echo "Prewarming integration test binaries with build parallelism 6..."
  if ! go test -p 6 -race -count=1 -run '^$' -exec /bin/true github.com/Azure/ARO-HCP/test-integration/...; then
    echo "Prewarm failed; the serial test run compiles the binaries itself."
  fi
fi

if [ "${USE_GOTESTSUM}" = "true" ]; then
    echo "Running tests with gotestsum (CI mode) with ARTIFACTS in ${ARTIFACT_DIR} and GOPATH=${GOPATH}..."
    gotestsum --junitfile "${ARTIFACT_DIR}/junit-integration-test-junit.xml" -- -p 1 -race github.com/Azure/ARO-HCP/test-integration/...
    sed -i 's/\(<testsuite.*\)name=""/\1 name="go-unit"/' "${ARTIFACT_DIR}/junit-integration-test-junit.xml"
else
    echo "Running tests with go test (local mode)..."
    go test -p 1 -race github.com/Azure/ARO-HCP/test-integration/...
fi