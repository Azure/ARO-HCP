#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${SCRIPT_DIR}/../../config/config-dev-ci.yaml}"
OUTPUT_FILE="${OUTPUT_FILE:-${SCRIPT_DIR}/amw-pool.yaml}"

# Like the identity catalogs, this exports the DEV defaults, not job overrides.
# ARM IDs are deterministic, so no Azure access is needed. Deploy before use.
POOL="$(yq -o=json '.clouds.dev.defaults.ci.dev.amwPool' "${CONFIG_FILE}")"
jq -e '
    (.subscriptionId | type == "string" and length > 0) and
    (.resourceGroup | type == "string" and length > 0) and
    (.location | type == "string" and length > 0) and
    (.services.namePrefix != .hcps.namePrefix) and
    ([.services, .hcps] | all(
        (.namePrefix | type == "string" and length > 0) and
        (.size | type == "number" and . >= 0 and . <= 800 and floor == .)
    ))
' <<< "${POOL}" > /dev/null

TMPFILE="$(mktemp)"
trap 'rm -f "${TMPFILE}"' EXIT

jq '
    . as $pool |
    {amwPool: (["services", "hcps"] | map(
        . as $purpose |
        range(0; $pool[$purpose].size) |
        ($pool[$purpose].namePrefix + "-" + tostring) as $name |
        {key: $name, value: {
            workspaceId: ("/subscriptions/" + $pool.subscriptionId +
                "/resourceGroups/" + $pool.resourceGroup +
                "/providers/Microsoft.Monitor/accounts/" + $name),
            location: $pool.location,
            purpose: $purpose
        }}
    ) | from_entries)}
' <<< "${POOL}" | yq -P > "${TMPFILE}"

cp "${TMPFILE}" "${OUTPUT_FILE}"
echo "Updated ${OUTPUT_FILE} (configured inventory; deployment is not verified)"
