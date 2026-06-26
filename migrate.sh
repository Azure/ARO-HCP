#!/bin/bash
set -euo pipefail

BASE="/workspace/ARO-HCP/backend/pkg/controllers"

# Create all new directories
mkdir -p "$BASE/cluster/delete"
mkdir -p "$BASE/cluster/properties"
mkdir -p "$BASE/cluster/version"
mkdir -p "$BASE/cluster/validation"
mkdir -p "$BASE/cluster/status"
mkdir -p "$BASE/cluster/operations"
mkdir -p "$BASE/cluster/billing"
mkdir -p "$BASE/cluster/datadump"
mkdir -p "$BASE/cluster/mismatch"
mkdir -p "$BASE/cluster/maestro"
mkdir -p "$BASE/cluster/placement"
mkdir -p "$BASE/cluster/metrics"
mkdir -p "$BASE/nodepool/delete"
mkdir -p "$BASE/nodepool/version"
mkdir -p "$BASE/nodepool/validation"
mkdir -p "$BASE/nodepool/status"
mkdir -p "$BASE/nodepool/operations"
mkdir -p "$BASE/nodepool/maestro"
mkdir -p "$BASE/externalauth/delete"
mkdir -p "$BASE/externalauth/status"
mkdir -p "$BASE/externalauth/operations"
mkdir -p "$BASE/subscription"
mkdir -p "$BASE/managementcluster"
mkdir -p "$BASE/shared/status"
mkdir -p "$BASE/shared/operations"
mkdir -p "$BASE/shared/metrics"
mkdir -p "$BASE/shared/mismatch"
mkdir -p "$BASE/shared/validation"
mkdir -p "$BASE/shared/billing"
mkdir -p "$BASE/shared/maestro"
mkdir -p "$BASE/shared/version"

echo "=== Directories created ==="

# --- cluster/delete/ ← from clusterdeletion/ ---
for f in "$BASE/clusterdeletion/"*.go; do
  cp "$f" "$BASE/cluster/delete/"
done

# --- cluster/properties/ ← from clusterpropertiescontroller/ ---
for f in "$BASE/clusterpropertiescontroller/"*.go; do
  cp "$f" "$BASE/cluster/properties/"
done

# --- cluster/version/ ← from upgradecontrollers/ (control_plane_* + trigger_control_plane_* + utils.go) ---
cp "$BASE/upgradecontrollers/control_plane_active_version_controller.go" "$BASE/cluster/version/"
cp "$BASE/upgradecontrollers/control_plane_active_version_controller_test.go" "$BASE/cluster/version/"
cp "$BASE/upgradecontrollers/control_plane_desired_version_controller.go" "$BASE/cluster/version/"
cp "$BASE/upgradecontrollers/control_plane_desired_version_controller_test.go" "$BASE/cluster/version/"
cp "$BASE/upgradecontrollers/trigger_control_plane_upgrade_controller.go" "$BASE/cluster/version/"
cp "$BASE/upgradecontrollers/trigger_control_plane_upgrade_controller_test.go" "$BASE/cluster/version/"
# utils.go - isGatewayToNextMinor is unexported and only used by control_plane files
cp "$BASE/upgradecontrollers/utils.go" "$BASE/cluster/version/"

# --- cluster/validation/ ← from validationcontrollers/ (cluster_validation_controller*) ---
cp "$BASE/validationcontrollers/cluster_validation_controller.go" "$BASE/cluster/validation/"

# --- cluster/status/ ← from statuscontrollers/ (cluster_degraded_aggregator*) ---
cp "$BASE/statuscontrollers/cluster_degraded_aggregator.go" "$BASE/cluster/status/"
cp "$BASE/statuscontrollers/cluster_degraded_aggregator_test.go" "$BASE/cluster/status/"

# --- cluster/operations/ ← from operationcontrollers/ (operation_cluster_*, operation_*credential*, dispatch_*credential*) ---
cp "$BASE/operationcontrollers/operation_cluster_create.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_cluster_create_test.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_cluster_delete.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_cluster_delete_legacy.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_cluster_delete_test.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_cluster_update.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_cluster_update_test.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_request_credential.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_request_credential_test.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_revoke_credentials.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/operation_revoke_credentials_test.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/dispatch_request_credential.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/dispatch_request_credential_test.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/dispatch_revoke_credentials.go" "$BASE/cluster/operations/"
cp "$BASE/operationcontrollers/dispatch_revoke_credentials_test.go" "$BASE/cluster/operations/"

# --- cluster/billing/ ← from billingcontrollers/ (create_billing_doc*) ---
cp "$BASE/billingcontrollers/create_billing_doc.go" "$BASE/cluster/billing/"
cp "$BASE/billingcontrollers/create_billing_doc_test.go" "$BASE/cluster/billing/"

# --- cluster/datadump/ ← from datadumpcontrollers/ (billing_dump*, cs_state_dump*, dump_cluster_recursive*) ---
cp "$BASE/datadumpcontrollers/billing_dump.go" "$BASE/cluster/datadump/"
cp "$BASE/datadumpcontrollers/billing_dump_test.go" "$BASE/cluster/datadump/"
cp "$BASE/datadumpcontrollers/cs_state_dump.go" "$BASE/cluster/datadump/"
cp "$BASE/datadumpcontrollers/cs_state_dump_test.go" "$BASE/cluster/datadump/"
cp "$BASE/datadumpcontrollers/dump_cluster_recursive.go" "$BASE/cluster/datadump/"

# --- cluster/mismatch/ ← from mismatchcontrollers/ (backfill_cluster_uid*, cosmos_cluster_matching*, cosmos_externalauth_matching*, cosmos_nodepool_matching*) ---
cp "$BASE/mismatchcontrollers/backfill_cluster_uid.go" "$BASE/cluster/mismatch/"
cp "$BASE/mismatchcontrollers/cosmos_cluster_matching.go" "$BASE/cluster/mismatch/"
cp "$BASE/mismatchcontrollers/cosmos_externalauth_matching.go" "$BASE/cluster/mismatch/"
cp "$BASE/mismatchcontrollers/cosmos_nodepool_matching.go" "$BASE/cluster/mismatch/"

# --- cluster/maestro/ ← from root (create_cluster_scoped_read_desires_controller*, cleanup_legacy_maestro_readonly_bundles_controller*) ---
cp "$BASE/create_cluster_scoped_read_desires_controller.go" "$BASE/cluster/maestro/"
cp "$BASE/cleanup_legacy_maestro_readonly_bundles_controller.go" "$BASE/cluster/maestro/"

# --- cluster/placement/ ← from managementclustercontrollers/ (management_cluster_placement_sync*) ---
cp "$BASE/managementclustercontrollers/management_cluster_placement_sync.go" "$BASE/cluster/placement/"
cp "$BASE/managementclustercontrollers/management_cluster_placement_sync_test.go" "$BASE/cluster/placement/"

# --- cluster/metrics/ ← from metricscontrollers/ (cluster_version_metrics_handler*) ---
cp "$BASE/metricscontrollers/cluster_version_metrics_handler.go" "$BASE/cluster/metrics/"
cp "$BASE/metricscontrollers/cluster_version_metrics_handler_test.go" "$BASE/cluster/metrics/"

# --- cluster/ ← do_nothing* ---
cp "$BASE/do_nothing.go" "$BASE/cluster/"

# --- nodepool/delete/ ← from nodepooldeletion/ ---
for f in "$BASE/nodepooldeletion/"*.go; do
  cp "$f" "$BASE/nodepool/delete/"
done

# --- nodepool/version/ ← from upgradecontrollers/ (nodepool_*) ---
cp "$BASE/upgradecontrollers/nodepool_active_version_controller.go" "$BASE/nodepool/version/"
cp "$BASE/upgradecontrollers/nodepool_active_version_controller_test.go" "$BASE/nodepool/version/"
cp "$BASE/upgradecontrollers/nodepool_active_version_real_cosmos_test.go" "$BASE/nodepool/version/"
cp "$BASE/upgradecontrollers/nodepool_version_controller.go" "$BASE/nodepool/version/"
cp "$BASE/upgradecontrollers/nodepool_version_controller_test.go" "$BASE/nodepool/version/"
cp "$BASE/upgradecontrollers/trigger_node_pool_upgrade_controller.go" "$BASE/nodepool/version/"
cp "$BASE/upgradecontrollers/trigger_node_pool_upgrade_controller_test.go" "$BASE/nodepool/version/"

# --- nodepool/validation/ ← from validationcontrollers/ (nodepool_validation_controller*) ---
cp "$BASE/validationcontrollers/nodepool_validation_controller.go" "$BASE/nodepool/validation/"
cp "$BASE/validationcontrollers/nodepool_validation_controller_test.go" "$BASE/nodepool/validation/"

# --- nodepool/status/ ← from statuscontrollers/ (nodepool_degraded_aggregator*) ---
cp "$BASE/statuscontrollers/nodepool_degraded_aggregator.go" "$BASE/nodepool/status/"
cp "$BASE/statuscontrollers/nodepool_degraded_aggregator_test.go" "$BASE/nodepool/status/"

# --- nodepool/operations/ ← from operationcontrollers/ (operation_node_pool_*) ---
cp "$BASE/operationcontrollers/operation_node_pool_create.go" "$BASE/nodepool/operations/"
cp "$BASE/operationcontrollers/operation_node_pool_create_test.go" "$BASE/nodepool/operations/"
cp "$BASE/operationcontrollers/operation_node_pool_delete.go" "$BASE/nodepool/operations/"
cp "$BASE/operationcontrollers/operation_node_pool_delete_test.go" "$BASE/nodepool/operations/"
cp "$BASE/operationcontrollers/operation_node_pool_update.go" "$BASE/nodepool/operations/"
cp "$BASE/operationcontrollers/operation_node_pool_update_test.go" "$BASE/nodepool/operations/"

# --- nodepool/maestro/ ← from root (create_nodepool_scoped_read_desires_controller*) ---
cp "$BASE/create_nodepool_scoped_read_desires_controller.go" "$BASE/nodepool/maestro/"

# --- externalauth/delete/ ← from externalauthdeletion/ ---
for f in "$BASE/externalauthdeletion/"*.go; do
  cp "$f" "$BASE/externalauth/delete/"
done

# --- externalauth/status/ ← from statuscontrollers/ (externalauth_degraded_aggregator*) ---
cp "$BASE/statuscontrollers/externalauth_degraded_aggregator.go" "$BASE/externalauth/status/"
cp "$BASE/statuscontrollers/externalauth_degraded_aggregator_test.go" "$BASE/externalauth/status/"

# --- externalauth/operations/ ← from operationcontrollers/ (operation_external_auth_*) ---
cp "$BASE/operationcontrollers/operation_external_auth_create.go" "$BASE/externalauth/operations/"
cp "$BASE/operationcontrollers/operation_external_auth_create_test.go" "$BASE/externalauth/operations/"
cp "$BASE/operationcontrollers/operation_external_auth_delete.go" "$BASE/externalauth/operations/"
cp "$BASE/operationcontrollers/operation_external_auth_delete_legacy.go" "$BASE/externalauth/operations/"
cp "$BASE/operationcontrollers/operation_external_auth_delete_test.go" "$BASE/externalauth/operations/"
cp "$BASE/operationcontrollers/operation_external_auth_update.go" "$BASE/externalauth/operations/"
cp "$BASE/operationcontrollers/operation_external_auth_update_test.go" "$BASE/externalauth/operations/"

# --- subscription/ ← from datadumpcontrollers/ (dump_subscription_non_cluster*), root (cosmos_migration*, clean_orphaned_cluster_managed_resource_group_controller*) ---
cp "$BASE/datadumpcontrollers/dump_subscription_non_cluster.go" "$BASE/subscription/"
cp "$BASE/cosmos_migration.go" "$BASE/subscription/"
cp "$BASE/cosmos_migration_test.go" "$BASE/subscription/"
cp "$BASE/clean_orphaned_cluster_managed_resource_group_controller.go" "$BASE/subscription/"
cp "$BASE/clean_orphaned_cluster_managed_resource_group_controller_test.go" "$BASE/subscription/"

# --- managementcluster/ ← from datadumpcontrollers/ (dump_management_cluster*) ---
cp "$BASE/datadumpcontrollers/dump_management_cluster.go" "$BASE/managementcluster/"

# --- shared/status/ ← from statuscontrollers/ (firstobservedbad*, helpers*, inertia*, union_condition*, aggregator_testhelpers_test*) ---
cp "$BASE/statuscontrollers/firstobservedbad.go" "$BASE/shared/status/"
cp "$BASE/statuscontrollers/helpers.go" "$BASE/shared/status/"
cp "$BASE/statuscontrollers/helpers_test.go" "$BASE/shared/status/"
cp "$BASE/statuscontrollers/inertia.go" "$BASE/shared/status/"
cp "$BASE/statuscontrollers/inertia_test.go" "$BASE/shared/status/"
cp "$BASE/statuscontrollers/union_condition.go" "$BASE/shared/status/"
cp "$BASE/statuscontrollers/union_condition_test.go" "$BASE/shared/status/"
cp "$BASE/statuscontrollers/aggregator_testhelpers_test.go" "$BASE/shared/status/"

# --- shared/operations/ ← from operationcontrollers/ (generic_operation*, operation_state*, utils*, doc*, test_helpers_test*) ---
cp "$BASE/operationcontrollers/generic_operation.go" "$BASE/shared/operations/"
cp "$BASE/operationcontrollers/operation_state.go" "$BASE/shared/operations/"
cp "$BASE/operationcontrollers/operation_state_test.go" "$BASE/shared/operations/"
cp "$BASE/operationcontrollers/utils.go" "$BASE/shared/operations/"
cp "$BASE/operationcontrollers/utils_test.go" "$BASE/shared/operations/"
cp "$BASE/operationcontrollers/doc.go" "$BASE/shared/operations/"
cp "$BASE/operationcontrollers/test_helpers_test.go" "$BASE/shared/operations/"

# --- shared/metrics/ ← from metricscontrollers/ (metrics_controller*, operation_phase_metrics_controller*, resource_metrics_controller*) ---
cp "$BASE/metricscontrollers/metrics_controller.go" "$BASE/shared/metrics/"
cp "$BASE/metricscontrollers/operation_phase_metrics_controller.go" "$BASE/shared/metrics/"
cp "$BASE/metricscontrollers/operation_phase_metrics_controller_test.go" "$BASE/shared/metrics/"
cp "$BASE/metricscontrollers/resource_metrics_controller.go" "$BASE/shared/metrics/"
cp "$BASE/metricscontrollers/resource_metrics_controller_test.go" "$BASE/shared/metrics/"

# --- shared/mismatch/ ← from mismatchcontrollers/ (cluster_service_cluster_matching*, delete_orphaned_cosmos*) ---
cp "$BASE/mismatchcontrollers/cluster_service_cluster_matching.go" "$BASE/shared/mismatch/"
cp "$BASE/mismatchcontrollers/delete_orphaned_cosmos.go" "$BASE/shared/mismatch/"
cp "$BASE/mismatchcontrollers/delete_orphaned_cosmos_test.go" "$BASE/shared/mismatch/"

# --- shared/validation/ ← from validationcontrollers/validations/ ---
for f in "$BASE/validationcontrollers/validations/"*.go; do
  cp "$f" "$BASE/shared/validation/"
done

# --- shared/billing/ ← from billingcontrollers/ (orphaned_billing_cleanup*) ---
cp "$BASE/billingcontrollers/orphaned_billing_cleanup.go" "$BASE/shared/billing/"
cp "$BASE/billingcontrollers/orphaned_billing_cleanup_test.go" "$BASE/shared/billing/"

# --- shared/maestro/ ← from root (cs_maestro_utils*, maestro_readonly_bundle_helpers*, delete_orphaned_maestro_readonly_bundles_controller*) ---
cp "$BASE/cs_maestro_utils.go" "$BASE/shared/maestro/"
cp "$BASE/maestro_readonly_bundle_helpers.go" "$BASE/shared/maestro/"
cp "$BASE/delete_orphaned_maestro_readonly_bundles_controller.go" "$BASE/shared/maestro/"
cp "$BASE/delete_orphaned_maestro_readonly_bundles_controller_test.go" "$BASE/shared/maestro/"
# Also move test_helpers_test.go (used only by delete_orphaned tests)
cp "$BASE/test_helpers_test.go" "$BASE/shared/maestro/"

echo "=== All files copied to new locations ==="

# Now remove old directories and root files
rm -rf "$BASE/billingcontrollers"
rm -rf "$BASE/clusterdeletion"
rm -rf "$BASE/clusterpropertiescontroller"
rm -rf "$BASE/datadumpcontrollers"
rm -rf "$BASE/externalauthdeletion"
rm -rf "$BASE/managementclustercontrollers"
rm -rf "$BASE/metricscontrollers"
rm -rf "$BASE/mismatchcontrollers"
rm -rf "$BASE/nodepooldeletion"
rm -rf "$BASE/operationcontrollers"
rm -rf "$BASE/statuscontrollers"
rm -rf "$BASE/upgradecontrollers"
rm -rf "$BASE/validationcontrollers"

# Remove root .go files that were moved
rm -f "$BASE/clean_orphaned_cluster_managed_resource_group_controller.go"
rm -f "$BASE/clean_orphaned_cluster_managed_resource_group_controller_test.go"
rm -f "$BASE/cleanup_legacy_maestro_readonly_bundles_controller.go"
rm -f "$BASE/cosmos_migration.go"
rm -f "$BASE/cosmos_migration_test.go"
rm -f "$BASE/create_cluster_scoped_read_desires_controller.go"
rm -f "$BASE/create_nodepool_scoped_read_desires_controller.go"
rm -f "$BASE/cs_maestro_utils.go"
rm -f "$BASE/delete_orphaned_maestro_readonly_bundles_controller.go"
rm -f "$BASE/delete_orphaned_maestro_readonly_bundles_controller_test.go"
rm -f "$BASE/do_nothing.go"
rm -f "$BASE/maestro_readonly_bundle_helpers.go"
rm -f "$BASE/test_helpers_test.go"

echo "=== Old directories and root files removed ==="
echo "=== Remaining structure: ==="
ls "$BASE/"
