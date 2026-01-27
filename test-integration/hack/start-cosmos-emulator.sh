#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail
set -x

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/_emulator_handling.sh"

# Control whether to restart an existing emulator
RESTART_EXISTING_EMULATOR="${RESTART_EXISTING_EMULATOR:-false}"

# Number of partitions to use for the emulator
# Increase if a lot of tests run in parallel and start failing with 503 errors
PARTITION_COUNT="${PARTITION_COUNT:-15}"

RUNNING_CONTAINER=$(get_running_emulator_container_name)
if [ -n "${RUNNING_CONTAINER}" ]; then
    if [ "${RESTART_EXISTING_EMULATOR}" != "true" ]; then
        echo "Cosmos DB emulator is already running."
        echo "Container name: ${RUNNING_CONTAINER}"
        echo "Endpoint: ${DEFAULT_COSMOS_ENDPOINT}"
        exit 0
    fi

    echo "Found existing Cosmos DB emulator container: ${RUNNING_CONTAINER}"
    stop_emulator
    echo "Will start a new emulator container..."
fi

CONTAINER_NAME="local-cosmos-emulator-$(shuf -i 1000-9999 -n 1)"
start_emulator "${CONTAINER_NAME}" "${PARTITION_COUNT}"