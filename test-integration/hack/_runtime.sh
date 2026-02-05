#!/bin/bash

# Concurrency configuration
# CONCURRENT_TESTS controls both test parallelism
CONCURRENT_TESTS="${CONCURRENT_TESTS:-8}"

# !!!
# PARTITIONS means something different in the emulator
# It is the number of backend processes handling containers in parllel
# Rule of thumb: PARTITIONS = CONCURRENT_TESTS + 2 buffer for internal emulator processes
DEFAULT_PARTITION_COUNT=$((CONCURRENT_TESTS + 2))
PARTITION_COUNT="${PARTITION_COUNT:-${DEFAULT_PARTITION_COUNT}}"

if [ -z "${GOPATH:-}" ]; then
    GOPATH=$(go env GOPATH)
    export GOPATH
fi
