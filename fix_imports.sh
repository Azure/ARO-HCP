#!/bin/bash
set -euo pipefail

REPO="/workspace/ARO-HCP"
M="github.com/Azure/ARO-HCP/backend/pkg/controllers"

# Find all Go files
FILES=$(find "$REPO" -name '*.go' -not -path '*/.git/*' -not -path '*/vendor/*')

# ================================================================
# Fix non-split package imports (simple 1:1 mappings)
# ================================================================

# clusterdeletion → cluster/delete (already done in initial script)
sed -i "s|\"${M}/clusterdeletion\"|\"${M}/cluster/delete\"|g" $FILES

# clusterpropertiescontroller → cluster/properties
sed -i "s|\"${M}/clusterpropertiescontroller\"|\"${M}/cluster/properties\"|g" $FILES

# externalauthdeletion → externalauth/delete
sed -i "s|\"${M}/externalauthdeletion\"|\"${M}/externalauth/delete\"|g" $FILES

# managementclustercontrollers → cluster/placement
sed -i "s|\"${M}/managementclustercontrollers\"|\"${M}/cluster/placement\"|g" $FILES

# nodepooldeletion → nodepool/delete
sed -i "s|\"${M}/nodepooldeletion\"|\"${M}/nodepool/delete\"|g" $FILES

# validationcontrollers/validations → shared/validation (already done)
sed -i "s|\"${M}/validationcontrollers/validations\"|\"${M}/shared/validation\"|g" $FILES

echo "=== Simple import path replacements done ==="

# ================================================================
# Now handle the MAJOR file: backend.go
# This needs careful rewiring since old single-package imports
# now map to multiple new packages
# ================================================================

BACKEND="$REPO/backend/pkg/app/backend.go"

# Replace old imports with new ones in backend.go
# We need aliases because many packages share the same base name

sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers"|clusterpkg "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/billingcontrollers"|clusterbilling "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/billing"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadumpcontrollers"|clusterdd "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/datadump"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/metricscontrollers"|sharedmetrics "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/metrics"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatchcontrollers"|clustermismatch "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/mismatch"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"|clusterops "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/operations"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/statuscontrollers"|clusterstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/status"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/upgradecontrollers"|clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"|' "$BACKEND"
sed -i 's|"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers"|clustervalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/validation"|' "$BACKEND"

# Now add the additional imports that the split packages need
# We need to add imports for:
# - nodepool/operations, nodepool/version, nodepool/status, nodepool/delete (already done), nodepool/maestro
# - externalauth/operations, externalauth/status, externalauth/delete (already done)
# - shared/billing, shared/mismatch, shared/maestro, shared/validation (already done), shared/metrics (done as primary)
# - subscription, managementcluster
# - cluster/properties (already done), cluster/placement (already done)
# - cluster/maestro, cluster/metrics

echo "=== Backend.go base import replacements done ==="
echo "=== Now need to add additional imports and fix symbol references ==="
