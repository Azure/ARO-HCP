#!/bin/bash
set -euo pipefail

BASE="/workspace/ARO-HCP/backend/pkg/controllers"

# Update package declarations for each new directory
# The package name should match the directory name (last path component)

update_pkg() {
  local dir="$1"
  local old_pkg="$2"
  local new_pkg="$3"
  for f in "$dir"/*.go; do
    [ -f "$f" ] || continue
    sed -i "s/^package ${old_pkg}$/package ${new_pkg}/" "$f"
  done
}

# cluster/delete (was clusterdeletion)
update_pkg "$BASE/cluster/delete" "clusterdeletion" "delete"

# cluster/properties (was clusterpropertiescontroller)
update_pkg "$BASE/cluster/properties" "clusterpropertiescontroller" "properties"

# cluster/version (was upgradecontrollers)
update_pkg "$BASE/cluster/version" "upgradecontrollers" "version"

# cluster/validation (was validationcontrollers)
update_pkg "$BASE/cluster/validation" "validationcontrollers" "validation"

# cluster/status (was statuscontrollers)
update_pkg "$BASE/cluster/status" "statuscontrollers" "status"

# cluster/operations (was operationcontrollers)
update_pkg "$BASE/cluster/operations" "operationcontrollers" "operations"

# cluster/billing (was billingcontrollers)
update_pkg "$BASE/cluster/billing" "billingcontrollers" "billing"

# cluster/datadump (was datadumpcontrollers)
update_pkg "$BASE/cluster/datadump" "datadumpcontrollers" "datadump"

# cluster/mismatch (was mismatchcontrollers)
update_pkg "$BASE/cluster/mismatch" "mismatchcontrollers" "mismatch"

# cluster/maestro (was controllers - root package)
update_pkg "$BASE/cluster/maestro" "controllers" "maestro"

# cluster/placement (was managementclustercontrollers)
update_pkg "$BASE/cluster/placement" "managementclustercontrollers" "placement"

# cluster/metrics (was metricscontrollers)
update_pkg "$BASE/cluster/metrics" "metricscontrollers" "metrics"

# cluster/ - do_nothing.go (was controllers - root package)
update_pkg "$BASE/cluster" "controllers" "cluster"

# nodepool/delete (was nodepooldeletion)
update_pkg "$BASE/nodepool/delete" "nodepooldeletion" "delete"

# nodepool/version (was upgradecontrollers)
update_pkg "$BASE/nodepool/version" "upgradecontrollers" "version"

# nodepool/validation (was validationcontrollers)
update_pkg "$BASE/nodepool/validation" "validationcontrollers" "validation"

# nodepool/status (was statuscontrollers)
update_pkg "$BASE/nodepool/status" "statuscontrollers" "status"

# nodepool/operations (was operationcontrollers)
update_pkg "$BASE/nodepool/operations" "operationcontrollers" "operations"

# nodepool/maestro (was controllers - root package)
update_pkg "$BASE/nodepool/maestro" "controllers" "maestro"

# externalauth/delete (was externalauthdeletion)
update_pkg "$BASE/externalauth/delete" "externalauthdeletion" "delete"

# externalauth/status (was statuscontrollers)
update_pkg "$BASE/externalauth/status" "statuscontrollers" "status"

# externalauth/operations (was operationcontrollers)
update_pkg "$BASE/externalauth/operations" "operationcontrollers" "operations"

# subscription (was mix of datadumpcontrollers and controllers)
update_pkg "$BASE/subscription" "datadumpcontrollers" "subscription"
update_pkg "$BASE/subscription" "controllers" "subscription"

# managementcluster (was datadumpcontrollers)
update_pkg "$BASE/managementcluster" "datadumpcontrollers" "managementcluster"

# shared/status (was statuscontrollers)
update_pkg "$BASE/shared/status" "statuscontrollers" "status"

# shared/operations (was operationcontrollers)
update_pkg "$BASE/shared/operations" "operationcontrollers" "operations"

# shared/metrics (was metricscontrollers)
update_pkg "$BASE/shared/metrics" "metricscontrollers" "metrics"

# shared/mismatch (was mismatchcontrollers)
update_pkg "$BASE/shared/mismatch" "mismatchcontrollers" "mismatch"

# shared/validation (was validations)
update_pkg "$BASE/shared/validation" "validations" "validation"

# shared/billing (was billingcontrollers)
update_pkg "$BASE/shared/billing" "billingcontrollers" "billing"

# shared/maestro (was controllers - root package)
update_pkg "$BASE/shared/maestro" "controllers" "maestro"

echo "=== Package declarations updated ==="

# Verify - list unique packages
echo "=== Verifying packages ==="
find "$BASE" -name '*.go' -exec grep -l '^package ' {} \; | while read f; do
  pkg=$(grep '^package ' "$f" | head -1)
  dir=$(dirname "$f" | sed "s|$BASE/||")
  echo "$dir: $pkg"
done | sort -u
