#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

MSI_MOCK_POOL_SIZE="${MSI_MOCK_POOL_SIZE:-20}"

if [ $# -eq 0 ]; then
    echo "Usage: $0 <owner-email-or-object-id> [<owner-email-or-object-id> ...]"
    echo ""
    echo "Adds one or more owners to all aro-dev-msi-mock-pool-* app registrations."
    echo ""
    echo "Examples:"
    echo "  $0 user@redhat.com"
    echo "  $0 user1@redhat.com user2@redhat.com"
    echo "  $0 00000000-0000-0000-0000-000000000000"
    exit 1
fi

OWNER_OBJECT_IDS=()
for OWNER_ARG in "$@"; do
    if [[ "$OWNER_ARG" == *"@"* ]]; then
        echo "Resolving ${OWNER_ARG} to object ID..."
        OID=$(az ad user show --id "$OWNER_ARG" --query id -o tsv)
        if [ -z "$OID" ]; then
            echo "ERROR: Could not resolve user '${OWNER_ARG}'"
            exit 1
        fi
        echo "  -> ${OID}"
        OWNER_OBJECT_IDS+=("$OID")
    else
        OWNER_OBJECT_IDS+=("$OWNER_ARG")
    fi
done

for i in $(seq 0 $((MSI_MOCK_POOL_SIZE - 1))); do
    APP_NAME="aro-dev-msi-mock-pool-${i}"
    APP_OBJECT_ID=$(az ad app list --filter "displayName eq '${APP_NAME}'" --query '[0].id' -o tsv)

    if [ -z "$APP_OBJECT_ID" ]; then
        echo "WARN: App '${APP_NAME}' not found, skipping"
        continue
    fi

    for OID in "${OWNER_OBJECT_IDS[@]}"; do
        echo "Adding owner ${OID} to ${APP_NAME}..."
        if ! OUTPUT=$(az ad app owner add --id "$APP_OBJECT_ID" --owner-object-id "$OID" 2>&1); then
            echo "  WARN: Failed to add owner to ${APP_NAME}: ${OUTPUT}"
        fi
    done
done

echo "Done."
