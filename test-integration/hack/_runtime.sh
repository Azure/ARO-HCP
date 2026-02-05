#!/bin/bash

# Concurrency configuration
# CONCURRENT_TESTS controls both test parallelism
CONCURRENT_TESTS="${CONCURRENT_TESTS:-4}"

# !!!
# PARTITIONS means something different in the emulator
# It is the number of backend processes handling containers in parllel
# Rule of thumb: PARTITIONS = nr of containers a test needs * number of concurrent tests + 2 buffer for internal emulator processes
CONTAINER_PER_TEST="${CONTAINER_PER_TEST:-3}"
DEFAULT_PARTITION_COUNT=$((CONTAINER_PER_TEST * CONCURRENT_TESTS + 2))
PARTITION_COUNT="${PARTITION_COUNT:-${DEFAULT_PARTITION_COUNT}}"

if [ -z "${GOPATH:-}" ]; then
    GOPATH=$(go env GOPATH)
    export GOPATH
fi
