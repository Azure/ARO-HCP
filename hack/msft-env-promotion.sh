#!/bin/bash

set -euo pipefail

# Script to promote image digests between MSFT environments
# Usage: ./msft-env-promotion.sh <target_env>
# - If target_env is "prod", copies digests from "stg"
# - If target_env is "stg", copies digests from "int"
# - Any other value fails

TARGET_ENV="${1:-}"
CONFIG_FILE="${2}"
# Validate input
if [[ -z "$TARGET_ENV" ]]; then
    echo "Error: Target environment parameter is required"
    echo "Usage: $0 <target_env> <config_file>"
    echo "  target_env: 'stg' (copies from int) or 'prod' (copies from stg)"
    echo "  config_file: path to the MSFT clouds overlay config file"
    exit 1
fi
if [[ -z "$CONFIG_FILE" ]]; then
    echo "Error: Config file parameter is required"
    echo "Usage: $0 <target_env> <config_file>"
    echo "  target_env: 'stg' (copies from int) or 'prod' (copies from stg)"
    echo "  config_file: path to the MSFT clouds overlay config file"
    exit 1
fi


# Determine source environment
case "$TARGET_ENV" in
    "stg")
        SOURCE_ENV="int"
        ;;
    "prod")
        SOURCE_ENV="stg"
        ;;
    *)
        echo "Error: Invalid target environment '$TARGET_ENV'"
        echo "Supported values: 'stg' (copies from int) or 'prod' (copies from stg)"
        exit 1
        ;;
esac


echo "Promoting image digests from $SOURCE_ENV to $TARGET_ENV"

# Check if config file exists
if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "Error: Configuration file $CONFIG_FILE not found"
    exit 1
fi

# Check if yq is available
if ! command -v yq &> /dev/null; then
    echo "Error: yq command not found. Please install yq to use this script."
    exit 1
fi

# Check if gh (GitHub CLI) is available for PR lookup
if ! command -v gh &> /dev/null; then
    echo "Error: GitHub CLI (gh) not found. This tool is required for PR lookup functionality."
    echo "Please install GitHub CLI: https://cli.github.com/"
    exit 1
fi

# Check if jq is available for JSON parsing
if ! command -v jq &> /dev/null; then
    echo "Error: jq not found. This tool is required for parsing GitHub API responses."
    echo "Please install jq: https://stedolan.github.io/jq/"
    exit 1
fi

GH_AVAILABLE=true

# Hardcoded list of image digest paths to promote
declare -a DIGEST_PATHS=(
    "clustersService.image.digest"
    "acrPull.image.digest"
    "secretSyncController.image.digest"
    "backplaneAPI.image.digest"
    "pko.imagePackage.digest"
    "pko.imageManager.digest"
    "pko.remotePhaseManager.digest"
    "svc.prometheus.prometheusOperator.image.digest"
    "svc.prometheus.prometheusSpec.image.digest"
    "mgmt.prometheus.prometheusOperator.image.digest"
    "mgmt.prometheus.prometheusSpec.image.digest"
    "frontend.image.digest"
    "backend.image.digest"
    "hypershift.image.digest"
    "maestro.image.digest"
    "imageSync.ocMirror.image.digest"
)

# Create a temporary file for processing
TEMP_FILE=$(mktemp)
git restore "${CONFIG_FILE}"
cp "$CONFIG_FILE" "$TEMP_FILE"

echo "Processing ${#DIGEST_PATHS[@]} image digest paths..."

# Arrays to track what was updated for summary
declare -a UPDATED_PATHS=()
declare -a UPDATED_PRS=()
declare -a UPDATED_COMMITS=()
declare -a OLD_DIGESTS=()
declare -a NEW_DIGESTS=()
declare -a PR_AUTHORS=()
declare -a PR_APPROVERS=()

# Process each digest path
for path in "${DIGEST_PATHS[@]}"; do
    echo "  Promoting $path..."

    # Get the digest value from source environment
    source_digest=$(yq eval ".clouds.public.environments.$SOURCE_ENV.defaults.$path" "$TEMP_FILE")

    if [[ "$source_digest" == "null" ]]; then
        echo "    Warning: No digest found at path $path in $SOURCE_ENV environment, skipping..."
        continue
    fi

    # Get the current digest in the target environment to check if it's different
    current_digest=$(yq eval ".clouds.public.environments.$TARGET_ENV.defaults.$path" "$TEMP_FILE")
    
    if [[ "$current_digest" == "$source_digest" ]]; then
        echo "    * $path: already up to date (${source_digest:0:20}...)"
        continue
    fi

    # Update the target environment with the source digest
    yq eval ".clouds.public.environments.$TARGET_ENV.defaults.$path = \"$source_digest\"" -i "$TEMP_FILE"

    echo "    * Updated $path: $source_digest"
    
    # Add to tracking arrays
    UPDATED_PATHS+=("$path")
    OLD_DIGESTS+=("$current_digest")
    NEW_DIGESTS+=("$source_digest")
    
    # Find and display PR information for this digest (only for actually updated digests)
    echo "      Looking up PR information for digest..."
    
    # Find the commit that introduced this digest (with timeout to prevent hanging)
    echo "      Searching git history..."
    pr_info=""
    commit_info=""
    
    if commit_hash=$(timeout 15s git log --format="%H" -S "$source_digest" -- "$CONFIG_FILE" 2>/dev/null | head -1); then
        if [[ -n "$commit_hash" ]]; then
            commit_info="${commit_hash:0:8}"
            
            # Attempt PR lookup with timeout
            if pr_result=$(timeout 10s gh pr list --search="${commit_hash:0:8}" --state=all --limit=1 --json number,title 2>/dev/null); then
                if echo "$pr_result" | jq -e '.[0].number' >/dev/null 2>&1; then
                    pr_number=$(echo "$pr_result" | jq -r '.[0].number' 2>/dev/null)
                    pr_title=$(echo "$pr_result" | jq -r '.[0].title' 2>/dev/null)
                    
                    if [[ -n "$pr_number" && "$pr_number" != "null" ]]; then
                        echo "      PR #$pr_number: $pr_title"
                        echo "      https://github.com/Azure/ARO-HCP/pull/$pr_number"
                        
                        # Get PR details (merge date, author, reviewers)
                        echo "      Getting PR details..."
                        merge_date=""
                        pr_author=""
                        pr_approvers=""
                        
                        if pr_details=$(timeout 15s gh pr view "$pr_number" --json mergedAt,author,reviews 2>/dev/null); then
                            # Extract merge date
                            merge_date=$(echo "$pr_details" | jq -r '.mergedAt // empty' 2>/dev/null)
                            if [[ -n "$merge_date" && "$merge_date" != "null" ]]; then
                                # Format the date and time nicely - handle both GNU date and BSD date (macOS)
                                if date -j >/dev/null 2>&1; then
                                    # BSD date (macOS)
                                    merge_date=$(date -j -f "%Y-%m-%dT%H:%M:%SZ" "$merge_date" "+%Y-%m-%d %H:%M UTC" 2>/dev/null || echo "${merge_date}")
                                else
                                    # GNU date (Linux)
                                    merge_date=$(date -d "$merge_date" '+%Y-%m-%d %H:%M UTC' 2>/dev/null || echo "${merge_date}")
                                fi
                            fi
                            
                            # Extract author
                            pr_author=$(echo "$pr_details" | jq -r '.author.login // empty' 2>/dev/null)
                            
                            # Extract approved reviewers
                            approvers=$(echo "$pr_details" | jq -r '.reviews[] | select(.state == "APPROVED") | .author.login' 2>/dev/null | sort -u | tr '\n' ' ')
                            if [[ -n "$approvers" ]]; then
                                pr_approvers=$(echo "$approvers" | sed 's/ $//' | tr ' ' ', ')
                            fi
                        fi
                        
                        if [[ -n "$merge_date" ]]; then
                            pr_info="PR #$pr_number: $pr_title (merged: $merge_date)"
                        else
                            pr_info="PR #$pr_number: $pr_title"
                        fi
                        
                        # Store author and approvers for summary
                        PR_AUTHORS+=("$pr_author")
                        PR_APPROVERS+=("$pr_approvers")
                    else
                        echo "      Commit: ${commit_hash:0:8} (no PR found)"
                        pr_info="Commit: ${commit_hash:0:8} (no PR found)"
                        PR_AUTHORS+=("")
                        PR_APPROVERS+=("")
                    fi
                else
                    echo "      Commit: ${commit_hash:0:8} (no PR found)"
                    pr_info="Commit: ${commit_hash:0:8} (no PR found)"
                    PR_AUTHORS+=("")
                    PR_APPROVERS+=("")
                fi
            else
                echo "      Commit: ${commit_hash:0:8} (GitHub API timeout or error)"
                pr_info="Commit: ${commit_hash:0:8} (GitHub API timeout or error)"
                PR_AUTHORS+=("")
                PR_APPROVERS+=("")
            fi
        else
            echo "      No commit found for this digest"
            pr_info="No commit found for this digest"
            PR_AUTHORS+=("")
            PR_APPROVERS+=("")
        fi
    else
        echo "      Git search timed out or failed"
        pr_info="Git search timed out or failed"
        PR_AUTHORS+=("")
        PR_APPROVERS+=("")
    fi
    
    # Store the results for summary
    UPDATED_PRS+=("$pr_info")
    UPDATED_COMMITS+=("$commit_info")
done

# Replace the original file with the updated version
mv "$TEMP_FILE" "$CONFIG_FILE"

echo ""
echo "Successfully promoted all image digests from $SOURCE_ENV to $TARGET_ENV"
echo "Updated file: $CONFIG_FILE"

# Print summary of what was updated in markdown format
echo ""
echo "## Promotion Summary"
echo ""
echo "**Environment:** \`$SOURCE_ENV\` → \`$TARGET_ENV\`"
echo ""

if [[ ${#UPDATED_PATHS[@]} -eq 0 ]]; then
    echo "**No updates needed** - All image digests were already up to date."
else
    echo "**Updated ${#UPDATED_PATHS[@]} image digest(s):**"
    echo ""
    
    for i in "${!UPDATED_PATHS[@]}"; do
        component_name="${UPDATED_PATHS[i]}"
        pr_info="${UPDATED_PRS[i]}"
        old_digest="${OLD_DIGESTS[i]}"
        new_digest="${NEW_DIGESTS[i]}"
        pr_author="${PR_AUTHORS[i]}"
        pr_approvers="${PR_APPROVERS[i]}"
        
        echo "### $((i+1)). \`${component_name}\`"
        echo "- [ ] **Validated and ready for promotion by**  \`<put your handle here>\`"
        echo ""
        
        # Show digest change information
        if [[ -n "$old_digest" && "$old_digest" != "null" ]]; then
            echo "**Old:** \`${old_digest:0:20}...\`"
        else
            echo "**Old:** *Not set*"
        fi
        echo "**New:** \`${new_digest:0:20}...\`"
        echo ""
        
        if [[ -n "$pr_info" && "$pr_info" == PR* ]]; then
            # Extract PR number, title, and merge date for proper markdown formatting
            pr_number=$(echo "$pr_info" | sed 's/PR #\([0-9]*\):.*/\1/')
            
            if [[ "$pr_info" == *"(merged:"* ]]; then
                # Extract title and merge date
                pr_title=$(echo "$pr_info" | sed 's/PR #[0-9]*: \(.*\) (merged:.*/\1/')
                merge_date=$(echo "$pr_info" | sed 's/.*merged: \([^)]*\)).*/\1/')
                echo "**Source:** [PR #${pr_number}](https://github.com/Azure/ARO-HCP/pull/${pr_number}) - ${pr_title}"
                echo "**PR Merged:** ${merge_date}"
            else
                # No merge date available
                pr_title=$(echo "$pr_info" | sed 's/PR #[0-9]*: \(.*\)/\1/')
                echo "**Source:** [PR #${pr_number}](https://github.com/Azure/ARO-HCP/pull/${pr_number}) - ${pr_title}"
            fi
            
            # Add author and approver information
            if [[ -n "$pr_author" ]]; then
                echo "**PR Author:** @${pr_author}"
            fi
            if [[ -n "$pr_approvers" ]]; then
                # Format approvers with @ prefix
                formatted_approvers=$(echo "$pr_approvers" | sed 's/\([^, ]*\)/@\1/g')
                echo "**PR Approved by:** ${formatted_approvers}"
            fi
        elif [[ -n "$pr_info" ]]; then
            echo "**Source:** ${pr_info}"
        fi
        echo ""
    done
fi

echo "---"
echo "*Generated on $(date '+%Y-%m-%d %H:%M:%S %Z')*"
