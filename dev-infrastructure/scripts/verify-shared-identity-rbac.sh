#!/bin/bash
#
# Detects RBAC drift on shared HCP service principals (first-party, arm-helper,
# msi-mock, etc.) by comparing each target identity's subscription-scope role
# assignments against a known-good reference identity.
#
# Background: AROSLSRE-1994 - three INT shared service principals were
# recreated (new object IDs) after being hard-deleted, but the recreation
# skipped the RBAC assignment step. The gap went undetected for roughly a day
# because nothing checked that a recreated identity actually has its expected
# roles. This script is meant to be run periodically (e.g. from a Prow
# periodic job, alongside cleanup-sweeper) so that a repeat of this failure
# mode is caught within hours instead of days.
#
# Usage:
#   verify-shared-identity-rbac.sh <subscription-id> <reference-object-id> <target-object-id> [<target-object-id> ...]
#
# Exit code is non-zero if any target is missing one or more of the reference
# identity's role assignments. A per-identity summary is printed to stdout in
# all cases.

set -euo pipefail

if [ "$#" -lt 3 ]; then
    echo "Usage: $0 <subscription-id> <reference-object-id> <target-object-id> [<target-object-id> ...]" >&2
    exit 2
fi

SUBSCRIPTION_ID=$1
REFERENCE_OBJECT_ID=$2
shift 2
TARGET_OBJECT_IDS=("$@")

# roleNames returns the sorted, de-duplicated list of role definition names
# assigned to $1 at subscription scope, one per line.
roleNames() {
    local objectId=$1
    az role assignment list \
        --subscription "${SUBSCRIPTION_ID}" \
        --assignee-object-id "${objectId}" \
        --scope "/subscriptions/${SUBSCRIPTION_ID}" \
        --query "[].roleDefinitionName" \
        -o tsv \
        --only-show-errors \
        | sort -u
}

if ! REFERENCE_ROLES=$(roleNames "${REFERENCE_OBJECT_ID}"); then
    echo "Error: failed to query role assignments for reference identity ${REFERENCE_OBJECT_ID}; cannot establish a baseline." >&2
    exit 2
fi

if [ -z "${REFERENCE_ROLES}" ]; then
    echo "Error: reference identity ${REFERENCE_OBJECT_ID} has no role assignments at /subscriptions/${SUBSCRIPTION_ID}; refusing to use it as a baseline." >&2
    exit 2
fi

echo "Reference identity ${REFERENCE_OBJECT_ID} roles:"
echo "${REFERENCE_ROLES}" | sed 's/^/  - /'

DRIFT_FOUND=0

for objectId in "${TARGET_OBJECT_IDS[@]}"; do
    if ! TARGET_ROLES=$(roleNames "${objectId}"); then
        echo "ERROR: ${objectId} failed to query role assignments; treating as drift"
        DRIFT_FOUND=1
        continue
    fi

    MISSING_ROLES=$(comm -23 <(echo "${REFERENCE_ROLES}") <(echo "${TARGET_ROLES}"))

    if [ -z "${TARGET_ROLES}" ]; then
        echo "MISSING RBAC: ${objectId} has zero role assignments at /subscriptions/${SUBSCRIPTION_ID} (expected: $(echo "${REFERENCE_ROLES}" | tr '\n' ',' | sed 's/,$//'))"
        DRIFT_FOUND=1
    elif [ -n "${MISSING_ROLES}" ]; then
        echo "MISSING RBAC: ${objectId} is missing role(s): $(echo "${MISSING_ROLES}" | tr '\n' ',' | sed 's/,$//')"
        DRIFT_FOUND=1
    else
        echo "OK: ${objectId} has all expected roles"
    fi
done

exit "${DRIFT_FOUND}"
