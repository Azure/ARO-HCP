#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Accept cutoff time as parameter
CUTOFF_TIME=${1:-$(date +%s)}

###############################################################################
# Get list of cluster IDs from MI keyvault secrets
###############################################################################
log_info "Retrieving cluster IDs from MI keyvault (${MI_KEYVAULT})..."

CLUSTER_IDS_FILE="/tmp/.cluster_ids.tmp"
> "${CLUSTER_IDS_FILE}"  # Clear the file

# Get secrets from MI keyvault that match the pattern uamsi-{cluster_id}-*
# and are older than cutoff time
mapfile -t SECRETS < <(az keyvault secret list --vault-name "${MI_KEYVAULT}" \
    --query "[?starts_with(name, 'uamsi-')].{name:name,created:attributes.created}" -o json 2>/dev/null | \
    jq -r '.[] | select((.created | sub("\\+00:00$"; "Z") | fromdateiso8601) < '"${CUTOFF_TIME}"') | .name' || true)

# Extract cluster IDs from secret names
# Pattern: uamsi-{cluster_id}-{suffix}
declare -A CLUSTER_IDS_MAP

for secret_name in "${SECRETS[@]}"; do
    if [[ -n "${secret_name}" && "${secret_name}" =~ ^uamsi-([a-z0-9]+)- ]]; then
        CLUSTER_ID="${BASH_REMATCH[1]}"
        CLUSTER_IDS_MAP["${CLUSTER_ID}"]=1
    fi
done

# Write unique cluster IDs to file
for cluster_id in "${!CLUSTER_IDS_MAP[@]}"; do
    echo "${cluster_id}" >> "${CLUSTER_IDS_FILE}"
done

CLUSTER_COUNT=$(wc -l < "${CLUSTER_IDS_FILE}" 2>/dev/null || echo 0)
log_info "Found ${CLUSTER_COUNT} unique cluster ID(s)"

