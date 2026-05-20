#!/bin/bash
set -e

az login --identity

PRINCIPALS=("$@")
MAX_WAIT=120  # Maximum 2 minutes per principal
POLL_INTERVAL=5  # Check every 5 seconds

echo "Validating ${#PRINCIPALS[@]} managed identity principals are propagated to AAD..."

for PRINCIPAL_ID in "${PRINCIPALS[@]}"; do
  echo "Checking principal: $PRINCIPAL_ID"
  elapsed=0

  # List role assignments to force ARM to validate the principal exists in AAD
  until az role assignment list --assignee "$PRINCIPAL_ID" --output none; do
    if [ $elapsed -ge $MAX_WAIT ]; then
      echo "✗ Principal $PRINCIPAL_ID still not available after ${MAX_WAIT}s"
      exit 1
    fi
    sleep $POLL_INTERVAL
    elapsed=$((elapsed + POLL_INTERVAL))
  done
  echo "✓ Principal $PRINCIPAL_ID available in AAD (${elapsed}s)"
done

echo "✓ All ${#PRINCIPALS[@]} principals validated in AAD"
