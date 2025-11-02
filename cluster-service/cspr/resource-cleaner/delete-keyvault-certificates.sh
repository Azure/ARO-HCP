#!/bin/bash

# Script to delete Key Vault certificates with age display
# Usage: ./delete_keyvault_certificates.sh [--vault-name <name>] [--cutoff-time <epoch>] [--dry-run]

set -e

# Default values
VAULT_NAME="ah-cspr-cx-usw3-1"
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
            echo "  --vault-name <name>    Vault name (default: ah-cspr-cx-usw3-1)"
            echo "  --cutoff-time <epoch>  Unix epoch timestamp cutoff (default: now)"
            echo "  --dry-run              Preview what would be deleted without actually deleting"
            echo "  --help                 Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 --vault-name ah-cspr-cx-usw3-1 --cutoff-time 1234567890"
            echo "  $0 --vault-name ah-cspr-cx-usw3-1 --cutoff-time 1234567890 --dry-run"
            echo ""
            echo "⚠️  WARNING: Script deletes certificates by default!"
            echo "    Use --dry-run flag to preview before actual deletion."
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
echo "🔐 Azure Key Vault Certificate Deletion Script"
echo "================================================================================"
echo "Vault: $VAULT_NAME"
if [ "$DRY_RUN" = true ]; then
    echo "Mode: 🔍 DRY RUN (preview only, no changes will be made)"
else
    echo "Mode: ⚠️  LIVE DELETION (certificates will be deleted!)"
fi
echo "Cutoff Time: $(date -u -d @${CUTOFF_TIME} '+%Y-%m-%d %H:%M:%S UTC')"
echo "Timestamp: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
echo "================================================================================"
echo ""

# List all certificates
echo "📋 Fetching certificates from vault..."
TEMP_CERTS=$(mktemp)
TEMP_RESULTS=$(mktemp)
TEMP_TO_DELETE=$(mktemp)

az keyvault certificate list --vault-name "$VAULT_NAME" -o json > "$TEMP_CERTS"

if [ $? -ne 0 ]; then
    echo "❌ Failed to list certificates from vault"
    rm -f "$TEMP_CERTS" "$TEMP_RESULTS" "$TEMP_TO_DELETE"
    exit 1
fi

# Count total certificates
TOTAL_CERTS=$(jq 'length' "$TEMP_CERTS")
echo "✅ Found $TOTAL_CERTS total certificates"
echo ""

# Process each certificate
echo "================================================================================"
echo "Processing certificates..."
echo "================================================================================"
echo ""

# Use Python for better date parsing and processing
python3 << EOF
import json
from datetime import datetime, timezone

with open("$TEMP_CERTS") as f:
    certs = json.load(f)

cutoff_time = $CUTOFF_TIME
dry_run = "$DRY_RUN" == "true"

deleted_count = 0
skipped_count = 0
certs_to_delete = []

for cert in certs:
    # Extract certificate name from the ID
    cert_id = cert['id']
    name = cert_id.split('/')[-1]
    created_str = cert['attributes']['created']
    enabled = cert['attributes']['enabled']

    # Parse creation date
    try:
        created = datetime.fromisoformat(created_str.replace('+00:00', '+00:00'))
        created_timestamp = int(created.timestamp())
    except Exception as e:
        print(f"⚠️  {name} - Could not parse creation date: {created_str}")
        continue

    # Check if certificate is older than cutoff
    if created_timestamp >= cutoff_time:
        print(f"⏭️  SKIP: {name} (created: {created_str}) - too recent")
        skipped_count += 1
        continue

    # Display certificate info
    status_icon = "✅" if enabled else "❌"
    print(f"{status_icon} DELETE: {name}")
    print(f"   Created: {created_str}")

    if dry_run:
        print(f"   [DRY RUN] Would delete this certificate")
    else:
        # Add to list for actual deletion
        certs_to_delete.append(name)
    
    deleted_count += 1
    print()

# Save results to temp files
with open("$TEMP_RESULTS", "w") as f:
    if dry_run:
        f.write(f"{deleted_count}\n{skipped_count}\n")
    else:
        f.write(f"{len(certs_to_delete)}\n{skipped_count}\n")

if not dry_run and certs_to_delete:
    with open("$TEMP_TO_DELETE", "w") as f:
        for name in certs_to_delete:
            f.write(f"{name}\n")
EOF

# If not dry run, actually delete the certificates
if [ "$DRY_RUN" = false ] && [ -f "$TEMP_TO_DELETE" ] && [ -s "$TEMP_TO_DELETE" ]; then
    echo ""
    echo "🗑️  Executing deletions..."
    echo ""

    # Read the original skipped count before we overwrite the file
    ORIGINAL_SKIPPED=$(sed -n '2p' "$TEMP_RESULTS")

    DELETED_COUNT=0
    FAILED_COUNT=0

    while IFS= read -r CERT_NAME; do
        if [ -n "$CERT_NAME" ]; then
            if az keyvault certificate delete --vault-name "$VAULT_NAME" --name "$CERT_NAME" >/dev/null 2>&1; then
                echo "✅ Deleted: $CERT_NAME"
                DELETED_COUNT=$((DELETED_COUNT + 1))
            else
                echo "❌ Failed to delete: $CERT_NAME"
                FAILED_COUNT=$((FAILED_COUNT + 1))
            fi
        fi
    done < "$TEMP_TO_DELETE"

    # Update results file with actual counts
    echo "$DELETED_COUNT" > "$TEMP_RESULTS"
    echo "$ORIGINAL_SKIPPED" >> "$TEMP_RESULTS"

    if [ $FAILED_COUNT -gt 0 ]; then
        echo ""
        echo "⚠️  $FAILED_COUNT certificates failed to delete"
    fi
fi

# Read counts from temp file
DELETED_COUNT=$(sed -n '1p' "$TEMP_RESULTS")
SKIPPED_COUNT=$(sed -n '2p' "$TEMP_RESULTS")
rm -f "$TEMP_CERTS" "$TEMP_RESULTS" "$TEMP_TO_DELETE"

# Summary
echo ""
echo "================================================================================"
echo "📊 DELETION SUMMARY"
echo "================================================================================"
echo "Total certificates found: $TOTAL_CERTS"
echo "Certificates $([ "$DRY_RUN" = true ] && echo "that would be deleted" || echo "deleted"): $DELETED_COUNT"
echo "Certificates skipped (too recent): $SKIPPED_COUNT"
echo ""

if [ "$DRY_RUN" = true ]; then
    echo "ℹ️  This was a dry run. No certificates were actually deleted."
    echo "💡 Run without --dry-run to actually delete certificates"
else
    echo "✅ Deletion complete!"
    echo "ℹ️  Certificates are soft-deleted (recoverable for 90 days)"
    echo "💡 Use 'az keyvault certificate purge' to permanently delete if needed"
fi

echo "================================================================================"
