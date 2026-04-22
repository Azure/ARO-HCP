#!/usr/bin/env bash

# Copyright 2025 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

KQL_DIR="${REPO_ROOT}/dev-infrastructure/modules/logs/kusto/tables"
CONTAINER_NAME="kusto-emulator-$$"
EMULATOR_IMAGE="mcr.microsoft.com/azuredataexplorer/kustainer-linux:latest"
ENDPOINT="${KUSTO_ENDPOINT:-}"
READINESS_TIMEOUT=120

if [ -z "${ENDPOINT}" ]; then
  RUNTIME="$(command -v podman 2>/dev/null || command -v docker 2>/dev/null || true)"
  if [ -z "${RUNTIME}" ]; then
    echo "ERROR: podman or docker is required to run the Kusto emulator"
    exit 1
  fi

  echo "Starting Kusto emulator via ${RUNTIME}..."
  ${RUNTIME} run -e ACCEPT_EULA=Y -m 4G -d -p 8080:8080 \
    --name "${CONTAINER_NAME}" "${EMULATOR_IMAGE}" > /dev/null
  trap '${RUNTIME} rm -f ${CONTAINER_NAME} > /dev/null 2>&1' EXIT

  ENDPOINT="http://localhost:8080"

  echo "Waiting for Kusto emulator to be ready (timeout ${READINESS_TIMEOUT}s)..."
  ready=false
  for i in $(seq 1 $((READINESS_TIMEOUT / 2))); do
    if curl -sf -X POST -H 'Content-Type: application/json' \
      -d '{"csl":".show cluster"}' "${ENDPOINT}/v1/rest/mgmt" > /dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 2
  done

  if [ "${ready}" != "true" ]; then
    echo "ERROR: Kusto emulator did not become ready within ${READINESS_TIMEOUT}s"
    exit 1
  fi
  echo "Kusto emulator is ready"
fi

VERBOSE_FLAG=""
if [ "${VERBOSE:-}" = "true" ]; then
  VERBOSE_FLAG="-v 1"
fi

go run "${REPO_ROOT}/tooling/kustoctl/main.go" \
  ${VERBOSE_FLAG} \
  validate kql \
  --endpoint "${ENDPOINT}" \
  --kql-dir "${KQL_DIR}"
