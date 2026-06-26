#!/bin/bash
set -euo pipefail

# This script replaces old import paths with new ones across ALL .go files in the repo.
# We must be careful with ordering - replace more specific paths first to avoid partial matches.

REPO="/workspace/ARO-HCP"
MODULE="github.com/Azure/ARO-HCP/backend/pkg/controllers"

# Find all Go files in the repo
FILES=$(find "$REPO" -name '*.go' -not -path '*/.git/*')

# Replace import paths - most specific first, then general
# Order matters: longer/more-specific paths before shorter ones

# validationcontrollers/validations → shared/validation
sed -i "s|${MODULE}/validationcontrollers/validations|${MODULE}/shared/validation|g" $FILES

# billingcontrollers → split: create_billing_doc* → cluster/billing, orphaned_billing* → shared/billing
# These were all in one package. Now split into two packages. Need to handle this carefully.
# First check what was imported and used from each:
# The import was "billingcontrollers" and callers used symbols from it.
# Since the package is split, we need to check which symbols are used where.
# For now, replace the import path and we'll fix symbol issues during build.

# clusterdeletion → cluster/delete
sed -i "s|${MODULE}/clusterdeletion|${MODULE}/cluster/delete|g" $FILES

# clusterpropertiescontroller → cluster/properties
sed -i "s|${MODULE}/clusterpropertiescontroller|${MODULE}/cluster/properties|g" $FILES

# datadumpcontrollers → need to handle per-file basis, but as a package import it was one
# The consumers imported the whole package. Now it's split across cluster/datadump, subscription, managementcluster
# We'll need to handle this after checking what symbols are used

# externalauthdeletion → externalauth/delete
sed -i "s|${MODULE}/externalauthdeletion|${MODULE}/externalauth/delete|g" $FILES

# managementclustercontrollers → cluster/placement
sed -i "s|${MODULE}/managementclustercontrollers|${MODULE}/cluster/placement|g" $FILES

# nodepooldeletion → nodepool/delete
sed -i "s|${MODULE}/nodepooldeletion|${MODULE}/nodepool/delete|g" $FILES

# upgradecontrollers → split between cluster/version and nodepool/version
# This needs to be handled carefully based on what symbols are used

# operationcontrollers → split between cluster/operations, nodepool/operations, externalauth/operations, shared/operations
# Same - needs careful handling

# statuscontrollers → split between cluster/status, nodepool/status, externalauth/status, shared/status
# Same - needs careful handling

# metricscontrollers → split between cluster/metrics and shared/metrics
# Same

# mismatchcontrollers → split between cluster/mismatch and shared/mismatch
# Same

# validationcontrollers → split between cluster/validation and nodepool/validation
# Same

echo "=== Simple import replacements done ==="
echo "=== Now need to handle split-package imports ==="
