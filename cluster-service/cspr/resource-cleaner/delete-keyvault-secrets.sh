#!/bin/bash

# Script to delete Key Vault secrets with age display
# Usage: ./delete_keyvault_secrets.sh [--vault-name <name>] [--cutoff-time <epoch>] [--dry-run]

set -e

# Default values
VAULT_NAME="ah-cspr-mi-usw3-1"
DRY_RUN=false
CUTOFF_TIME=$(date +%s)

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --vault-name)
            VAULT_NAME="$2"
            shift 2
            ;;
        --cutoff-time)
            CUTOFF_TIME="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --vault-name <name>    Vault name (default: ah-cspr-mi-usw3-1)"
            echo "  --cutoff-time <epoch>  Unix epoch timestamp cutoff (default: now)"
            echo "  --dry-run              Preview what would be deleted without actually deleting"
            echo "  --help                 Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 --vault-name ah-cspr-mi-usw3-1 --cutoff-time 1234567890"
            echo "  $0 --vault-name ah-cspr-mi-usw3-1 --cutoff-time 1234567890 --dry-run"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

echo "================================================================================"
echo "🔐 Azure Key Vault Secret Deletion Script"
echo "================================================================================"
echo "Vault: $VAULT_NAME"
echo "Mode: $([ "$DRY_RUN" = true ] && echo "DRY RUN (no changes will be made)" || echo "LIVE DELETION")"
echo "Cutoff Time: $(date -u -d @${CUTOFF_TIME} '+%Y-%m-%d %H:%M:%S UTC')"
echo "Timestamp: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
echo "================================================================================"
echo ""

# List all secrets
echo "📋 Fetching secrets from vault..."
TEMP_SECRETS=$(mktemp)
TEMP_RESULTS=$(mktemp)

az keyvault secret list --vault-name "$VAULT_NAME" -o json > "$TEMP_SECRETS"

if [ $? -ne 0 ]; then
    echo "❌ Failed to list secrets from vault"
    rm -f "$TEMP_SECRETS" "$TEMP_RESULTS"
    exit 1
fi

# Count total secrets
TOTAL_SECRETS=$(jq 'length' "$TEMP_SECRETS")
echo "✅ Found $TOTAL_SECRETS total secrets"
echo ""

# Process each secret
echo "================================================================================"
echo "Processing secrets..."
echo "================================================================================"
echo ""

# Create temp file for secrets to delete
TEMP_TO_DELETE=$(mktemp)

# Use Python for better date parsing and processing
python3 << EOF
import json
from datetime import datetime, timezone

with open("$TEMP_SECRETS") as f:
    secrets = json.load(f)

cutoff_time = $CUTOFF_TIME
dry_run = "$DRY_RUN" == "true"

deleted_count = 0
skipped_count = 0
secrets_to_delete = []

for secret in secrets:
    name = secret['name']
    created_str = secret['attributes']['created']
    enabled = secret['attributes']['enabled']
    
    # Parse creation date
    try:
        created = datetime.fromisoformat(created_str.replace('+00:00', '+00:00'))
        created_timestamp = int(created.timestamp())
    except Exception as e:
        print(f"⚠️  {name} - Could not parse creation date: {created_str}")
        continue
    
    # Check if secret is older than cutoff
    if created_timestamp >= cutoff_time:
        print(f"⏭️  SKIP: {name} (created: {created_str}) - too recent")
        skipped_count += 1
        continue
    
    # Display secret info
    status_icon = "✅" if enabled else "❌"
    print(f"{status_icon} DELETE: {name}")
    print(f"   Created: {created_str}")
    
    if dry_run:
        print(f"   [DRY RUN] Would delete this secret")
        deleted_count += 1
    else:
        # Add to list for actual deletion
        secrets_to_delete.append(name)
    print()

# Save results to temp files
with open("$TEMP_RESULTS", "w") as f:
    f.write(f"{len(secrets_to_delete)}\n{skipped_count}\n")

if not dry_run and secrets_to_delete:
    with open("$TEMP_TO_DELETE", "w") as f:
        for name in secrets_to_delete:
            f.write(f"{name}\n")
EOF

# If not dry run, actually delete the secrets
if [ "$DRY_RUN" = false ] && [ -f "$TEMP_TO_DELETE" ] && [ -s "$TEMP_TO_DELETE" ]; then
    echo ""
    echo "🗑️  Executing deletions..."
    echo ""
    
    # Read the original skipped count before we overwrite the file
    ORIGINAL_SKIPPED=$(sed -n '2p' "$TEMP_RESULTS")
    
    DELETED_COUNT=0
    FAILED_COUNT=0
    
    while IFS= read -r SECRET_NAME; do
        if [ -n "$SECRET_NAME" ]; then
            if az keyvault secret delete --vault-name "$VAULT_NAME" --name "$SECRET_NAME" >/dev/null 2>&1; then
                echo "✅ Deleted: $SECRET_NAME"
                DELETED_COUNT=$((DELETED_COUNT + 1))
            else
                echo "❌ Failed to delete: $SECRET_NAME"
                FAILED_COUNT=$((FAILED_COUNT + 1))
            fi
        fi
    done < "$TEMP_TO_DELETE"
    
    # Update results file with actual counts
    echo "$DELETED_COUNT" > "$TEMP_RESULTS"
    echo "$ORIGINAL_SKIPPED" >> "$TEMP_RESULTS"
    
    if [ $FAILED_COUNT -gt 0 ]; then
        echo ""
        echo "⚠️  $FAILED_COUNT secrets failed to delete"
    fi
fi

# Read counts from temp file
DELETED_COUNT=$(sed -n '1p' "$TEMP_RESULTS")
SKIPPED_COUNT=$(sed -n '2p' "$TEMP_RESULTS")
rm -f "$TEMP_SECRETS" "$TEMP_RESULTS" "$TEMP_TO_DELETE"

# Summary
echo ""
echo "================================================================================"
echo "📊 DELETION SUMMARY"
echo "================================================================================"
echo "Total secrets found: $TOTAL_SECRETS"
echo "Secrets $([ "$DRY_RUN" = true ] && echo "that would be deleted" || echo "deleted"): $DELETED_COUNT"
echo "Secrets skipped (too recent): $SKIPPED_COUNT"
echo ""

if [ "$DRY_RUN" = true ]; then
    echo "ℹ️  This was a dry run. No secrets were actually deleted."
    echo "💡 Run without --dry-run to actually delete secrets"
else
    echo "✅ Deletion complete!"
    echo "ℹ️  Secrets are soft-deleted (recoverable for 90 days)"
    echo "💡 Use 'az keyvault secret purge' to permanently delete if needed"
fi
echo "================================================================================"

