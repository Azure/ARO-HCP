#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Accept parameters: cutoff_time, prefix, dry_run
CUTOFF_TIME=${1:-$(date +%s)}
RG_PREFIX=${2:-""}
DRY_RUN=${3:-false}
CURRENT_TIME=$(date +%s)

if [[ -z "${RG_PREFIX}" ]]; then
    log_error "Resource group prefix is required"
    log_error "Usage: $0 <cutoff_time> <prefix>"
    exit 1
fi

###############################################################################
# Delete resource groups with specified prefix
###############################################################################
log_info "Deleting resource groups with prefix '${RG_PREFIX}'..."

RGS_DELETED=0
RGS_SKIPPED=0

# Get all resource groups matching the prefix with their tags
mapfile -t RG_LIST < <(az group list --query "[?starts_with(name, '${RG_PREFIX}')].{name:name,createdAt:tags.createdAt}" -o json 2>/dev/null | \
    jq -r '.[] | @json')

for rg_json in "${RG_LIST[@]}"; do
    if [[ -n "${rg_json}" ]]; then
        RG_NAME=$(echo "${rg_json}" | jq -r '.name')
        TAG_CREATED_AT=$(echo "${rg_json}" | jq -r '.createdAt // empty')
        
        # Only use createdAt tag
        if [[ -n "${TAG_CREATED_AT}" && "${TAG_CREATED_AT}" != "null" ]]; then
            RG_TIMESTAMP=$(date -d "${TAG_CREATED_AT}" +%s 2>/dev/null || echo "")
            
            if [[ -n "${RG_TIMESTAMP}" ]]; then
            if [[ ${RG_TIMESTAMP} -lt ${CUTOFF_TIME} ]]; then
                if [[ "${DRY_RUN}" == "true" ]]; then
                    log_info "  [DRY RUN] Would delete: ${RG_NAME} (created: ${TAG_CREATED_AT})"
                    ((RGS_DELETED++)) || true
                else
                    log_info "  Deleting: ${RG_NAME} (created: ${TAG_CREATED_AT})"
                    if az group delete --name "${RG_NAME}" --yes --no-wait 2>/dev/null; then
                        ((RGS_DELETED++)) || true
                    else
                        log_warn "  Failed to delete: ${RG_NAME}"
                    fi
                fi
            else
                    AGE_HOURS=$(( (CURRENT_TIME - RG_TIMESTAMP) / 3600 ))
                    log_info "  Skipping: ${RG_NAME} (age: ${AGE_HOURS}h, too recent)"
                    ((RGS_SKIPPED++)) || true
                fi
            else
                # Cannot parse createdAt timestamp
                log_warn "  Skipping: ${RG_NAME} (invalid createdAt format)"
                ((RGS_SKIPPED++)) || true
            fi
        else
            # No createdAt tag
            log_warn "  Skipping: ${RG_NAME} (no createdAt tag)"
            ((RGS_SKIPPED++)) || true
        fi
    fi
done

log_info "Resource groups with prefix '${RG_PREFIX}': ${RGS_DELETED} deleted, ${RGS_SKIPPED} skipped"
exit 0
