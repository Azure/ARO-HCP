#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

script_dir=$(dirname "$(realpath "${BASH_SOURCE[0]}")")
export REGIONAL_RESOURCE_GROUP=job-rg
workspace_prefix=/subscriptions/job-sub/resourceGroups/job-rg/providers/Microsoft.Monitor/accounts

# Mock every Azure call; external pool workspaces must never be queried or deleted.
az() {
    case "$*" in
        "monitor account list --resource-group job-rg --query [].id -o tsv")
            printf '%s\n' "$workspace_prefix/svc" "$workspace_prefix/hcp" "$workspace_prefix/tagged" "$workspace_prefix/stale"
            ;;
        "monitor account show --name tagged --resource-group job-rg --query tags.aroHCPPurpose -o tsv")
            echo services
            ;;
        "monitor account show --name "*" --resource-group job-rg --query tags.aroHCPPurpose -o tsv")
            echo null
            ;;
        "monitor account delete --name "*" --resource-group job-rg --yes")
            echo "DELETE $5"
            ;;
        *)
            echo "Unexpected Azure call: $*" >&2
            return 1
            ;;
    esac
}

for scenario in separate shared external default dry-run; do
    unset SVC_WORKSPACE_RESOURCE_ID HCP_WORKSPACE_RESOURCE_ID DRY_RUN
    case "$scenario" in
        separate)
            # ARM resource IDs are case insensitive.
            export SVC_WORKSPACE_RESOURCE_ID="${workspace_prefix^^}/SVC"
            export HCP_WORKSPACE_RESOURCE_ID="$workspace_prefix/hcp"
            expected=$'DELETE stale'
            ;;
        shared)
            export SVC_WORKSPACE_RESOURCE_ID="$workspace_prefix/svc"
            export HCP_WORKSPACE_RESOURCE_ID="$workspace_prefix/svc"
            expected=$'DELETE hcp\nDELETE stale'
            ;;
        external)
            export SVC_WORKSPACE_RESOURCE_ID=/subscriptions/pool-sub/resourceGroups/aro-hcp-ci-amw-pool/providers/Microsoft.Monitor/accounts/svc
            export HCP_WORKSPACE_RESOURCE_ID=/subscriptions/pool-sub/resourceGroups/aro-hcp-ci-amw-pool/providers/Microsoft.Monitor/accounts/hcp
            expected=$'DELETE svc\nDELETE hcp\nDELETE stale'
            ;;
        default)
            expected=$'DELETE svc\nDELETE hcp\nDELETE stale'
            ;;
        dry-run)
            export DRY_RUN=true
            expected=''
            ;;
    esac
    output=$(source "$script_dir/regional-housekeeping.sh")
    actual=''
    while IFS= read -r line; do
        if [[ "$line" == DELETE\ * ]]; then
            actual+="${actual:+$'\n'}$line"
        fi
    done <<< "$output"
    if [[ "$actual" != "$expected" ]]; then
        printf 'FAIL %s: expected [%s], got [%s]\n%s\n' "$scenario" "$expected" "$actual" "$output" >&2
        exit 1
    fi
    echo "PASS $scenario"
done
