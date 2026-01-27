#!/bin/bash
# Shared functions for Cosmos DB emulator management

# Constants
DEFAULT_COSMOS_ENDPOINT="https://localhost:8081"

# Choose container runtime (prefer podman, fallback to docker)
get_container_runtime() {
    if command -v podman >/dev/null 2>&1; then
        echo "podman"
    elif command -v docker >/dev/null 2>&1; then
        echo "docker"
    else
        echo "Error: Neither podman nor docker found. Please install one of them." >&2
        exit 1
    fi
}
CONTAINER_RUNTIME=$(get_container_runtime)

get_running_emulator_container_name() {
    ${CONTAINER_RUNTIME} ps --filter "name=local-cosmos-emulator-*" --format "{{.Names}}" | head -n 1
}

# Stop and remove emulator containers
stop_emulator() {
    local save_logs=${1:-false}

    echo "Stopping and removing existing container(s)..."

    if [ "${save_logs}" = "true" ]; then
        # Save logs before stopping
        local tmp_data_dir="${ARTIFACT_DIR:-/tmp}"
        mkdir -p "$tmp_data_dir"

        local containers
        containers=$(${CONTAINER_RUNTIME} ps -aq --filter "name=local-cosmos-emulator-*" 2>/dev/null || true)

        for container in $containers; do
            local container_name
            container_name=$(${CONTAINER_RUNTIME} inspect --format='{{.Name}}' "$container" | sed 's|^/||')
            if ${CONTAINER_RUNTIME} ps -q --filter "id=$container" | grep -q .; then
                echo "Saving logs for container: $container_name"
                ${CONTAINER_RUNTIME} logs "$container" > "${tmp_data_dir}/${container_name}.log" 2>&1 || true
            fi
        done
    fi

    ${CONTAINER_RUNTIME} ps -q --filter "name=local-cosmos-emulator-*" | xargs -r "${CONTAINER_RUNTIME}" stop
    ${CONTAINER_RUNTIME} ps -aq --filter "name=local-cosmos-emulator-*" | xargs -r "${CONTAINER_RUNTIME}" rm
}

# Start the emulator container and wait until ready (handles OS differences internally)
start_emulator() {
    local container_name=$1
    local partition_count=$2
    local os_type
    local container_image
    local ready_log_message

    os_type=$(uname -s)
    container_image="mcr.microsoft.com/cosmosdb/linux/azure-cosmos-emulator:latest"
    ready_log_message="Started $((partition_count+1))/$((partition_count+1)) partitions"

    if [ "${os_type}" = "Darwin" ]; then
        # on OSX we need to use the vnext-preview image because the regular one does not support ARM64
        # and also fails when running in qemu emulation mode under podman.
        # vnext-preview docs: https://learn.microsoft.com/en-gb/azure/cosmos-db/emulator-linux#docker-commands
        container_image="mcr.microsoft.com/cosmosdb/linux/azure-cosmos-emulator:vnext-preview"
        # the vnext-preview image logs a different message when ready
        ready_log_message="PostgreSQL and pgcosmos extension are ready"
    fi

    echo "Starting Cosmos DB emulator with container name: ${container_name}"
    ${CONTAINER_RUNTIME} run \
      --publish 8081:8081 \
      --publish 10250-10255:10250-10255 \
      -e AZURE_COSMOS_EMULATOR_IP_ADDRESS_OVERRIDE=127.0.0.1 \
      -e AZURE_COSMOS_EMULATOR_PARTITION_COUNT="${partition_count}" \
      -e PROTOCOL=https \
      --name "${container_name}" \
      --detach \
      "${container_image}"

    # Wait for emulator to be ready by checking logs
    echo "Waiting for Cosmos DB emulator to be ready..."
    for i in {1..60}; do
        logs_output=$(${CONTAINER_RUNTIME} logs "${container_name}" 2>&1)
        if echo "${logs_output}" | grep -q "${ready_log_message}"; then
            echo "Cosmos DB emulator is ready!"
            break
        fi
        if [ "$i" -eq 60 ]; then
            echo "Timeout waiting for Cosmos DB emulator to be ready"
            ${CONTAINER_RUNTIME} logs "${container_name}"
            exit 1
        fi
        echo "Attempt $i/60: Waiting for emulator to start..."
        sleep 5
    done

    # Wait for HTTPS endpoint to be available
    echo "Waiting for HTTPS endpoint to be available..."
    for i in {1..30}; do
        if curl --insecure -s "${DEFAULT_COSMOS_ENDPOINT}" >/dev/null 2>&1; then
            echo "HTTPS endpoint is ready!"
            break
        fi
        if [ "$i" -eq 30 ]; then
            echo "Error: Timeout waiting for HTTPS endpoint to be available"
            ${CONTAINER_RUNTIME} logs "${container_name}"
            return 1
        fi
        echo "Attempt $i/30: Waiting for HTTPS endpoint..."
        sleep 2
    done

    echo "Cosmos DB emulator started successfully!"
    echo "Container name: ${CONTAINER_NAME}"
    echo "Endpoint: ${DEFAULT_COSMOS_ENDPOINT}"
}
