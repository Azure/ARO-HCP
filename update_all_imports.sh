#!/bin/bash
set -euo pipefail

REPO="/workspace/ARO-HCP"
MODULE="github.com/Azure/ARO-HCP/backend/pkg/controllers"

# ================================================================
# STEP 1: Handle within-controllers imports (simple replacements)
# ================================================================

# controllerutils stays the same - no change needed

# upgradecontrollers referenced within controllers (only by nodepool/operations/operation_node_pool_update.go)
# That file uses upgradecontrollers.NodepoolVersionControllerName → now in nodepool/version
sed -i "s|\"${MODULE}/upgradecontrollers\"|\"${MODULE}/nodepool/version\"|g" \
  "$REPO/backend/pkg/controllers/nodepool/operations/operation_node_pool_update.go"
sed -i 's/upgradecontrollers\.NodepoolVersionControllerName/version.NodepoolVersionControllerName/g' \
  "$REPO/backend/pkg/controllers/nodepool/operations/operation_node_pool_update.go"

# validationcontrollers/validations → shared/validation
find "$REPO/backend/pkg/controllers/" -name '*.go' -exec \
  sed -i "s|\"${MODULE}/validationcontrollers/validations\"|\"${MODULE}/shared/validation\"|g" {} +

# Now "validations." references need to become "validation."
find "$REPO/backend/pkg/controllers/" -name '*.go' -exec \
  sed -i 's/validations\./validation./g' {} +

echo "=== Within-controllers imports updated ==="

# ================================================================
# STEP 2: Handle internal cross-references within split packages
# ================================================================

# Check if any moved files reference statuscontrollers, operationcontrollers, etc.
# The moved files were in the SAME package as the other files, so they use
# direct function calls, not imported package references. No cross-package
# imports needed for within-same-old-package references.

# BUT: some split packages now reference symbols that moved to a different new package.
# For example, cluster/operations files used generic_operation which is now in shared/operations.
# Since they were in the same package before, they referenced it directly.
# Now they need to import shared/operations.

# Let me check which symbols from shared/operations are used by cluster/operations etc.
echo "=== Checking cross-references for split operationcontrollers ==="

# Check what functions/types are defined in shared/operations (generic_operation.go, operation_state.go, utils.go, doc.go)
echo "--- shared/operations exports ---"
grep -n '^func \|^type \|^var \|^const ' "$REPO/backend/pkg/controllers/shared/operations/"*.go 2>/dev/null | grep -v _test.go || true

echo "--- cluster/operations references to shared funcs ---"
# generic_operation.go defines things like genericCreateOperation, genericUpdateOperation, genericDeleteOperation
# These are likely unexported (lowercase) - let me check
grep -c 'genericCreateOperation\|genericUpdateOperation\|genericDeleteOperation\|GenericOperation\|OperationState\|operationState' "$REPO/backend/pkg/controllers/cluster/operations/"*.go 2>/dev/null | grep -v ':0$' || echo "None found"

echo "=== Done checking ==="
