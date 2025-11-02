#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Accept dry-run parameter
DRY_RUN=${1:-false}

###############################################################################
# Delete ACR tokens for each cluster ID
###############################################################################
log_info "Deleting ACR tokens from container registry '${ACR_NAME}'..."

ACR_TOKENS_DELETED=0

# Read cluster IDs from file
CLUSTER_IDS_FILE="${SCRIPT_DIR}/.cluster_ids.tmp"

if [[ -f "${CLUSTER_IDS_FILE}" ]]; then
    while IFS= read -r cluster_id; do
        if [[ -n "${cluster_id}" ]]; then
            # Construct ACR token name from cluster ID
            # Token format: hc-ocp-pull-{cluster_id}
            TOKEN_NAME="hc-ocp-pull-${cluster_id}"
            log_info "Attempting to delete ACR token: ${TOKEN_NAME}"
            
            if az acr token show --name "${TOKEN_NAME}" --registry "${ACR_NAME}" &>/dev/null; then
                if [[ "${DRY_RUN}" == "true" ]]; then
                    log_info "[DRY RUN] Would delete ACR token: ${TOKEN_NAME}"
                    ((ACR_TOKENS_DELETED++)) || true
                else
                    if az acr token delete --name "${TOKEN_NAME}" --registry "${ACR_NAME}" --yes &>/dev/null; then
                        log_info "Deleted ACR token: ${TOKEN_NAME}"
                        ((ACR_TOKENS_DELETED++)) || true
                    else
                        log_warn "Failed to delete ACR token: ${TOKEN_NAME}"
                    fi
                fi
            else
                log_info "ACR token not found: ${TOKEN_NAME}"
            fi
        fi
    done < "${CLUSTER_IDS_FILE}"
    
    log_info "Deleted ${ACR_TOKENS_DELETED} ACR token(s)"
else
    log_warn "Cluster IDs file not found at ${CLUSTER_IDS_FILE}, skipping ACR token cleanup"
fi

