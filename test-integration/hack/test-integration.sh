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

# Keep phase timings beside the existing JUnit and emulator artifacts.
PHASE_TIMINGS="${ARTIFACT_DIR}/integration-phase-timings.log"
TIMED_PHASE=""
PHASE_START_SECONDS=0

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
    local exit_status=$?
    if [ -n "${TIMED_PHASE}" ]; then
        printf '%s phase=%s event=end duration_seconds=%s status=%s\n' \
            "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${TIMED_PHASE}" \
            "$((SECONDS - PHASE_START_SECONDS))" "${exit_status}" | tee -a "${PHASE_TIMINGS}"
    fi

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

# Explicit assets must match the requested version. Image-cached assets are an
# optimization: a different requested version uses the writable Makefile cache,
# so version bumps do not depend on the shared image being published first.
TIMED_PHASE="envtest-preparation"
PHASE_START_SECONDS=${SECONDS}
printf '%s phase=%s event=start\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${TIMED_PHASE}" | tee -a "${PHASE_TIMINGS}"
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# Read only the shared inventory, avoiding unrelated root Makefile bootstrap
# when the requested assets are already available.
EXPECTED_ENVTEST_VERSION="$(make --no-print-directory -s \
    -f "${REPO_ROOT}/dev-infrastructure/openshift-ci/versions.mk" \
    --eval='print-envtest-version:;@echo $(ENVTEST_K8S_VERSION)' print-envtest-version)"
EXPLICIT_ASSETS=false
if [ "${KUBEBUILDER_ASSETS+x}" = "x" ]; then
    EXPLICIT_ASSETS=true
elif [ -n "${ENVTEST_PREBUILT_ASSETS:-}" ]; then
    KUBEBUILDER_ASSETS="${ENVTEST_PREBUILT_ASSETS}"
fi
if [ "${KUBEBUILDER_ASSETS+x}" = "x" ]; then
    for binary in kube-apiserver etcd; do
        if [ -z "${KUBEBUILDER_ASSETS}" ] || [ ! -x "${KUBEBUILDER_ASSETS}/${binary}" ]; then
            echo "Error: envtest assets must contain executable ${binary}: ${KUBEBUILDER_ASSETS}" >&2
            exit 1
        fi
    done
    ACTUAL_ENVTEST_VERSION="$("${KUBEBUILDER_ASSETS}/kube-apiserver" --version)"
    if [ "${ACTUAL_ENVTEST_VERSION}" != "Kubernetes v${EXPECTED_ENVTEST_VERSION}" ]; then
        if [ "${EXPLICIT_ASSETS}" = true ]; then
            echo "Error: explicit envtest assets report '${ACTUAL_ENVTEST_VERSION}', expected Kubernetes v${EXPECTED_ENVTEST_VERSION}" >&2
            exit 1
        fi
        echo "Cached envtest assets report '${ACTUAL_ENVTEST_VERSION}'; bootstrapping requested Kubernetes v${EXPECTED_ENVTEST_VERSION}"
        unset KUBEBUILDER_ASSETS
    fi
fi
if [ "${KUBEBUILDER_ASSETS+x}" != "x" ]; then
    KUBEBUILDER_ASSETS="$(make -C "${REPO_ROOT}" -s envtest-setup)"
fi
export KUBEBUILDER_ASSETS
echo "envtest binaries: ${KUBEBUILDER_ASSETS}"
printf '%s phase=%s event=end duration_seconds=%s status=0\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${TIMED_PHASE}" \
    "$((SECONDS - PHASE_START_SECONDS))" | tee -a "${PHASE_TIMINGS}"
TIMED_PHASE=""

TIMED_PHASE="go-invocation"
PHASE_START_SECONDS=${SECONDS}
printf '%s phase=%s event=start\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${TIMED_PHASE}" | tee -a "${PHASE_TIMINGS}"
if [ "${USE_GOTESTSUM}" = "true" ]; then
    echo "Running tests with gotestsum (CI mode) with ARTIFACTS in ${ARTIFACT_DIR} and GOPATH=${GOPATH}..."
    gotestsum --junitfile "${ARTIFACT_DIR}/junit-integration-test-junit.xml" -- -p 1 -race github.com/Azure/ARO-HCP/test-integration/...
else
    echo "Running tests with go test (local mode)..."
    go test -p 1 -race github.com/Azure/ARO-HCP/test-integration/...
fi
printf '%s phase=%s event=end duration_seconds=%s status=0\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${TIMED_PHASE}" \
    "$((SECONDS - PHASE_START_SECONDS))" | tee -a "${PHASE_TIMINGS}"
TIMED_PHASE=""
if [ "${USE_GOTESTSUM}" = "true" ]; then
    sed -i 's/\(<testsuite.*\)name=""/\1 name="go-unit"/' "${ARTIFACT_DIR}/junit-integration-test-junit.xml"
fi