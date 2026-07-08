#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

MSI_MOCK_POOL_SIZE="${MSI_MOCK_POOL_SIZE:-20}"
MSI_MOCK_BOSKOS_PREFIX="aro-hcp-msi-mock-cs-sp-dev"

OUTPUT_FILE="${OUTPUT_FILE:-msi-mock-pool.yaml}"
TMPFILE=$(mktemp)
trap 'rm -f "$TMPFILE"' EXIT

yq -n '.miMockPool = {}' > "$TMPFILE"

for i in $(seq 0 $((MSI_MOCK_POOL_SIZE - 1))); do
    APP_NAME="aro-dev-msi-mock-pool-${i}"
    BOSKOS_KEY="${MSI_MOCK_BOSKOS_PREFIX}-${i}"

    CLIENT_ID=$(az ad app list --filter "displayName eq '${APP_NAME}'" --query '[*].appId' -o tsv)
    PRINCIPAL_ID=$(az ad sp list --filter "displayName eq '${APP_NAME}'" --query '[*].id' -o tsv)

    if [ -z "$CLIENT_ID" ] || [ -z "$PRINCIPAL_ID" ]; then
        echo "ERROR: Could not find SP ${APP_NAME} — deploy mock-identity-apps.bicep first"
        exit 1
    fi

    echo "Pool SP ${i} (${BOSKOS_KEY}): clientId=${CLIENT_ID} principalId=${PRINCIPAL_ID}"

    yq -i "
        .miMockPool.\"${BOSKOS_KEY}\".clientId = \"${CLIENT_ID}\" |
        .miMockPool.\"${BOSKOS_KEY}\".principalId = \"${PRINCIPAL_ID}\" |
        .miMockPool.\"${BOSKOS_KEY}\".certName = \"msiMockPoolCert-${i}\"
    " "$TMPFILE"
done

cp "$TMPFILE" "$OUTPUT_FILE"

echo "Done. Updated ${OUTPUT_FILE}"
