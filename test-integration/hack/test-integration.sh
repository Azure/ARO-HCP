#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

set -x # Turn on command tracing

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/_emulator_handling.sh"
source "${SCRIPT_DIR}/_runtime.sh"

USE_GOTESTSUM=false
if [ -n "${ARTIFACT_DIR:-}" ]; then
  echo "artifact dir found: ${ARTIFACT_DIR}"
  USE_GOTESTSUM=true
else
    export ARTIFACT_DIR="$(mktemp -d)"
    echo "created temp artifact dir: ${ARTIFACT_DIR}"
fi

trap 'save_emulator_logs "${ARTIFACT_DIR}"' EXIT

# done so that simulator tests will be skipped during normal unit testing, but fail if a connection fails.
export FRONTEND_SIMULATION_TESTING="true"
export FRONTEND_COSMOS_ENDPOINT="${DEFAULT_COSMOS_ENDPOINT}"
#TODO these are sent over HTTP, so it's only safe because the emulator is personal and well-known.  Fix the trust before sending real creds
export FRONTEND_COSMOS_KEY="${DEFAULT_COSMOS_KEY}"


if [ "${USE_GOTESTSUM}" = "true" ]; then
    echo "Running tests with gotestsum (CI mode) with ARTIFACTS in ${ARTIFACT_DIR} and GOPATH=${GOPATH}..."
    gotestsum --junitfile "${ARTIFACT_DIR}/junit-integration-test-junit.xml" -- -race -p "${CONCURRENT_TESTS}" github.com/Azure/ARO-HCP/test-integration/...
    sed -i 's/\(<testsuite.*\)name=""/\1 name="go-unit"/' "${ARTIFACT_DIR}/junit-integration-test-junit.xml"
else
    echo "Running tests with go test (local mode)..."
    go test -race -p "${CONCURRENT_TESTS}" github.com/Azure/ARO-HCP/test-integration/...
fi