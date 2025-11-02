#!/bin/bash

# Script to list and delete Maestro bundles created before a specific date
# Usage: ./delete_maestro_bundles.sh [maestro-url] [cutoff-date] [dry-run]

set -euo pipefail

# Configuration
MAESTRO_URL="${1:-http://localhost:8002}"
CUTOFF_DATE="${2:-2025-10-21T12:00:00.000000Z}"
DRY_RUN="${3:-false}"
API_ENDPOINT="${MAESTRO_URL}/api/maestro/v1/resource-bundles"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to list maestro bundles
list_maestro_bundles() {
    local search_query="created_at<'${CUTOFF_DATE}'"
    
    echo -e "${YELLOW}Fetching bundles created before ${CUTOFF_DATE}...${NC}" >&2
    
    # Make GET request to list bundles with search filter
    local response=$(curl -s -k \
        --connect-timeout 30 \
        --max-time 30 \
        "${API_ENDPOINT}?size=-1&search=${search_query}")
    
    if [ $? -ne 0 ]; then
        echo -e "${RED}Error: Failed to fetch bundles from Maestro API${NC}" >&2
        exit 1
    fi
    
    echo "$response"
}

# Function to verify bundle is deleted (returns 404)
verify_bundle_deleted() {
    local bundle_id="$1"
    local max_attempts=180  # 180 attempts = 30 minutes with 10s sleep
    local sleep_interval=10  # Check every 10 seconds
    local attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        local http_code=$(curl -s -k -w "%{http_code}" -o /dev/null \
            --connect-timeout 5 \
            --max-time 10 \
            "${API_ENDPOINT}/${bundle_id}")
        
        if [ "$http_code" -eq 404 ]; then
            local elapsed=$((attempt * sleep_interval))
            echo -e "  (Deleted after ~${elapsed}s)"
            return 0  # Bundle is deleted
        fi
        
        # Show progress every minute (every 6 attempts)
        if [ $((attempt % 6)) -eq 0 ]; then
            local elapsed=$((attempt * sleep_interval))
            echo -e "  Still waiting... (${elapsed}s elapsed)"
        fi
        
        sleep $sleep_interval
        ((attempt++)) || true
    done
    
    return 1  # Timeout waiting for deletion after 30 minutes
}

# Function to trigger deletion of a maestro bundle by ID
delete_maestro_bundle() {
    local bundle_id="$1"
    
    if [ "$DRY_RUN" = "true" ]; then
        echo -e "${YELLOW}[DRY RUN] Would delete maestro bundle '${bundle_id}'${NC}"
        return 0
    fi
    
    echo -e "${YELLOW}Triggering deletion for maestro bundle '${bundle_id}'${NC}"
    
    # Make DELETE request
    local http_code=$(curl -s -k -w "%{http_code}" -o /dev/null \
        --connect-timeout 30 \
        --max-time 30 \
        -X DELETE \
        "${API_ENDPOINT}/${bundle_id}")
    
    if [ "$http_code" -eq 200 ] || [ "$http_code" -eq 204 ]; then
        echo -e "${GREEN}✓ Deletion triggered for bundle '${bundle_id}'${NC}"
        return 0
    else
        echo -e "${RED}✗ Failed to trigger deletion for bundle '${bundle_id}' (HTTP ${http_code})${NC}" >&2
        return 1
    fi
}

# Main execution
main() {
    echo -e "${GREEN}=== Maestro Bundle Cleanup Script ===${NC}"
    if [ "$DRY_RUN" = "true" ]; then
        echo -e "${YELLOW}🔍 DRY RUN MODE - No bundles will be deleted${NC}"
    fi
    echo "Maestro URL: ${MAESTRO_URL}"
    echo "Cutoff Date: ${CUTOFF_DATE}"
    echo ""
    
    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        echo -e "${RED}Error: jq is required but not installed. Install it with: brew install jq${NC}" >&2
        exit 1
    fi
    
    # List bundles
    bundles_json=$(list_maestro_bundles)
    
    # Extract bundle IDs
    bundle_ids=$(echo "$bundles_json" | jq -r '.items[]? | .id // empty' 2>/dev/null)
    
    if [ -z "$bundle_ids" ]; then
        echo -e "${YELLOW}No bundles found matching the criteria${NC}"
        exit 0
    fi
    
    # Count total bundles
    total_bundles=$(echo "$bundle_ids" | wc -l | tr -d ' ')
    echo -e "${GREEN}Found ${total_bundles} bundle(s) to delete${NC}"
    echo ""
    
    # Trigger deletion for each bundle
    echo -e "${YELLOW}=== Phase 1: Triggering Deletions ===${NC}"
    triggered_count=0
    failed_trigger_count=0
    declare -a triggered_bundles=()
    
    while IFS= read -r bundle_id; do
        if [ -n "$bundle_id" ]; then
            if delete_maestro_bundle "$bundle_id"; then
                triggered_bundles+=("$bundle_id")
                ((triggered_count++)) || true
            else
                ((failed_trigger_count++)) || true
            fi
        fi
    done <<< "$bundle_ids"
    
    echo ""
    echo -e "${GREEN}Triggered deletions: ${triggered_count}${NC}"
    if [ $failed_trigger_count -gt 0 ]; then
        echo -e "${RED}Failed to trigger: ${failed_trigger_count}${NC}"
    fi
    
    # Verify all deletions (skip in dry-run mode)
    if [ "$DRY_RUN" = "true" ]; then
        echo ""
        echo -e "${YELLOW}🔍 DRY RUN - Skipping deletion verification${NC}"
        echo -e "${GREEN}=== Summary ===${NC}"
        echo -e "Total bundles found: ${total_bundles}"
        echo -e "Bundles that would be deleted: ${triggered_count}"
        if [ $failed_trigger_count -gt 0 ]; then
            echo -e "${RED}Would fail to trigger: ${failed_trigger_count}${NC}"
        fi
        echo -e "${YELLOW}No bundles were actually deleted${NC}"
        return 0
    elif [ ${#triggered_bundles[@]} -gt 0 ]; then
        echo ""
        echo -e "${YELLOW}=== Phase 2: Verifying Deletions ===${NC}"
        verified_count=0
        failed_verify_count=0
        
        for bundle_id in "${triggered_bundles[@]}"; do
            echo -e "Verifying deletion of bundle '${bundle_id}'..."
            if verify_bundle_deleted "$bundle_id"; then
                echo -e "${GREEN}✓ Confirmed: bundle '${bundle_id}' deleted${NC}"
                ((verified_count++)) || true
            else
                echo -e "${RED}✗ Timeout: bundle '${bundle_id}' still exists after 30 minutes${NC}"
                ((failed_verify_count++)) || true
            fi
        done
        
        echo ""
        echo -e "${GREEN}=== Summary ===${NC}"
        echo -e "Total bundles found: ${total_bundles}"
        echo -e "Deletions triggered: ${triggered_count}"
        echo -e "${GREEN}Deletions confirmed: ${verified_count}${NC}"
        
        if [ $failed_trigger_count -gt 0 ]; then
            echo -e "${RED}Failed to trigger: ${failed_trigger_count}${NC}"
        fi
        
        if [ $failed_verify_count -gt 0 ]; then
            echo -e "${RED}Failed verification: ${failed_verify_count}${NC}"
            exit 1
        else
            echo -e "${GREEN}All bundles deleted and verified successfully!${NC}"
        fi
    else
        echo ""
        echo -e "${RED}No deletions were triggered successfully${NC}"
        exit 1
    fi
}

# Run main function
main
