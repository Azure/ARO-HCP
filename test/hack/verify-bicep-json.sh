#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail


ACTUAL_OUTPUT_DIR="test/e2e/test-artifacts/generated-test-artifacts"

ARTIFACT_DIR=${ARTIFACT_DIR:-$(mktemp -d)}
TEMP_DIR="${ARTIFACT_DIR}"/bicep-json
mkdir "${TEMP_DIR}"

"$(dirname "${BASH_SOURCE}")/update-bicep-json.sh" "${TEMP_DIR}"

project_root="$(dirname "${BASH_SOURCE}")/../.."
echo "checking tmp content in ${TEMP_DIR} against the actual output dir ${project_root}/${ACTUAL_OUTPUT_DIR}"
diff -r "${TEMP_DIR}" "${project_root}/${ACTUAL_OUTPUT_DIR}"
