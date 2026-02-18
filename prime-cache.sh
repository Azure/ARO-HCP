#!/bin/bash
set -e

# Script to prime ACR pull-through cache for newly added Velero images
# Uses az acr commands (no Docker required)

ACR_NAME="${ACR_NAME:-arohcpsvcdev}"

echo "=========================================="
echo "Priming pull-through cache for: ${ACR_NAME}"
echo "=========================================="
echo ""

# Get current digests from config using sed
echo "Reading image digests from config..."
VELERO_SERVER_DIGEST=$(sed -n '287,292p' config/config.yaml | grep "digest:" | head -1 | awk '{print $2}')
VELERO_AZURE_DIGEST=$(sed -n '293,296p' config/config.yaml | grep "digest:" | head -1 | awk '{print $2}')

echo "Velero server digest: ${VELERO_SERVER_DIGEST}"
echo "Velero Azure plugin digest: ${VELERO_AZURE_DIGEST}"
echo ""

# Verify we have both digests
if [[ -z "${VELERO_SERVER_DIGEST}" || -z "${VELERO_AZURE_DIGEST}" ]]; then
    echo "ERROR: Could not extract image digests from config"
    exit 1
fi

# Prime Velero server using az acr import
echo "Priming: quay-cache/konveyor/velero"
az acr import \
  --name "${ACR_NAME}" \
  --source "quay.io/konveyor/velero@${VELERO_SERVER_DIGEST}" \
  --image "quay-cache/konveyor/velero@${VELERO_SERVER_DIGEST}" \
  --no-wait
echo "✓ Velero server import triggered"
echo ""

# Prime Velero Azure plugin using az acr import
echo "Priming: quay-cache/konveyor/velero-plugin-for-microsoft-azure"
az acr import \
  --name "${ACR_NAME}" \
  --source "quay.io/konveyor/velero-plugin-for-microsoft-azure@${VELERO_AZURE_DIGEST}" \
  --image "quay-cache/konveyor/velero-plugin-for-microsoft-azure@${VELERO_AZURE_DIGEST}" \
  --no-wait
echo "✓ Velero Azure plugin import triggered"
echo ""

echo "=========================================="
echo "Cache priming initiated for ${ACR_NAME}!"
echo ""
echo "To verify imports completed successfully:"
echo "  az acr repository list --name ${ACR_NAME} --output table | grep velero"
echo ""
echo "To run for other environments:"
echo "  ACR_NAME=arohcpsvcint ./prime-cache.sh"
echo "  ACR_NAME=arohcpsvcstg ./prime-cache.sh"
echo "  ACR_NAME=arohcpsvcprod ./prime-cache.sh"
echo "=========================================="
