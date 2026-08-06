#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

: "${POOL_SIZE:?POOL_SIZE must be set}"
: "${APP_NAME_BASE:?APP_NAME_BASE must be set}"
: "${BOSKOS_PREFIX:?BOSKOS_PREFIX must be set}"
: "${CATALOG_KEY:?CATALOG_KEY must be set}"
: "${CERT_NAME_BASE:?CERT_NAME_BASE must be set}"
: "${OUTPUT_FILE:?OUTPUT_FILE must be set}"

TMPFILE=$(mktemp)
trap 'rm -f "$TMPFILE"' EXIT

yq -n ".${CATALOG_KEY} = {}" > "$TMPFILE"

for ((i = 0; i < POOL_SIZE; i++)); do
    APP_NAME="${APP_NAME_BASE}-${i}"
    BOSKOS_KEY="${BOSKOS_PREFIX}-${i}"

    mapfile -t CLIENT_IDS < <(az ad app list --filter "displayName eq '${APP_NAME}'" --query '[*].appId' -o tsv)
    mapfile -t PRINCIPAL_IDS < <(az ad sp list --filter "displayName eq '${APP_NAME}'" --query '[*].id' -o tsv)

    if [ "${#CLIENT_IDS[@]}" -ne 1 ] || [ -z "${CLIENT_IDS[0]:-}" ]; then
        echo "ERROR: Expected exactly one Entra application named ${APP_NAME}, found ${#CLIENT_IDS[@]}"
        exit 1
    fi
    if [ "${#PRINCIPAL_IDS[@]}" -ne 1 ] || [ -z "${PRINCIPAL_IDS[0]:-}" ]; then
        echo "ERROR: Expected exactly one Entra service principal named ${APP_NAME}, found ${#PRINCIPAL_IDS[@]}"
        exit 1
    fi

    CLIENT_ID="${CLIENT_IDS[0]}"
    PRINCIPAL_ID="${PRINCIPAL_IDS[0]}"

    echo "Pool SP ${i} (${BOSKOS_KEY}): clientId=${CLIENT_ID} principalId=${PRINCIPAL_ID}"

    yq -i "
        .${CATALOG_KEY}.\"${BOSKOS_KEY}\".clientId = \"${CLIENT_ID}\" |
        .${CATALOG_KEY}.\"${BOSKOS_KEY}\".principalId = \"${PRINCIPAL_ID}\" |
        .${CATALOG_KEY}.\"${BOSKOS_KEY}\".certName = \"${CERT_NAME_BASE}-${i}\"
    " "$TMPFILE"
done

cp "$TMPFILE" "$OUTPUT_FILE"

echo "Done. Updated ${OUTPUT_FILE}"
