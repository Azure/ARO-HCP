// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package app

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type Runnable interface {
	Run(ctx context.Context, threadiness int)
}

var _ Runnable = controllerutils.Controller(nil)

type ControllerRegistration struct {
	Workers     int
	Enabled     func(ControllerContext) bool
	instantiate func(ControllerContext) (Runnable, error)
}

func (registration ControllerRegistration) Instantiate(controllerContext ControllerContext) (Runnable, error) {
	return registration.instantiate(controllerContext)
}

type instantiatedController struct {
	name     string
	runnable Runnable
	workers  int
}

func instantiateControllers(registry map[string]ControllerRegistration, controllerContext ControllerContext) ([]instantiatedController, error) {
	instances := make(map[string]Runnable, len(registry))
	for _, name := range controllerConstructionOrder() {
		entry := registry[name]
		if entry.Enabled != nil && !entry.Enabled(controllerContext) {
			continue
		}
		runnable, err := entry.Instantiate(controllerContext)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to instantiate controller %q: %w", name, err))
		}
		instances[name] = runnable
	}
	controllers := make([]instantiatedController, 0, len(instances))
	for _, name := range controllerLaunchOrder() {
		if runnable, exists := instances[name]; exists {
			controllers = append(controllers, instantiatedController{name: name, runnable: runnable, workers: registry[name].Workers})
		}
	}
	return controllers, nil
}

func newControllerRegistry() map[string]ControllerRegistration {
	return map[string]ControllerRegistration{

		// billing
		orphanedBillingCleanupControllerName: registerOrphanedBillingCleanupController(),
		createBillingDocControllerName:       registerCreateBillingDocController(),

		// cluster
		dispatchRequestCredentialControllerName:                        registerDispatchRequestCredentialController(),
		adminCredentialsDispatchRequestCredentialControllerName:        registerAdminCredentialsDispatchRequestCredentialController(),
		adminCredentialsDispatchRevokeCredentialsControllerName:        registerAdminCredentialsDispatchRevokeCredentialsController(),
		adminCredentialsOperationRequestCredentialPollControllerName:   registerAdminCredentialsOperationRequestCredentialPollController(),
		adminCredentialsOperationRevokeCredentialsPollControllerName:   registerAdminCredentialsOperationRevokeCredentialsPollController(),
		adminCredentialsIssuanceObserverControllerName:                 registerAdminCredentialsIssuanceObserverController(),
		adminCredentialsDesiresCreatorControllerName:                   registerAdminCredentialsDesiresCreatorController(),
		adminCredentialsPostIssuanceCleanupControllerName:              registerAdminCredentialsPostIssuanceCleanupController(),
		adminCredentialsRevokedGCControllerName:                        registerAdminCredentialsRevokedGCController(),
		adminCredentialsClusterDeletionCleanupControllerName:           registerAdminCredentialsClusterDeletionCleanupController(),
		systemAdminCredentialRevocationMarkRequestsControllerName:      registerSystemAdminCredentialRevocationMarkRequestsController(),
		systemAdminCredentialRevocationDesiresControllerName:           registerSystemAdminCredentialRevocationDesiresController(),
		systemAdminCredentialRevocationCompletionControllerName:        registerSystemAdminCredentialRevocationCompletionController(),
		systemAdminCredentialRevocationDeletionControllerName:          registerSystemAdminCredentialRevocationDeletionController(),
		clusterDenyAssignmentControllerName:                            registerClusterDenyAssignmentController(),
		clusterPendingClusterServiceIDAssignControllerName:             registerClusterPendingClusterServiceIDAssignController(),
		clusterClusterServiceCreateControllerName:                      registerClusterClusterServiceCreateController(),
		operationClusterCreateControllerName:                           registerOperationClusterCreateController(),
		operationClusterUpdateControllerName:                           registerOperationClusterUpdateController(),
		operationClusterDeleteControllerName:                           registerOperationClusterDeleteController(),
		operationRequestCredentialControllerName:                       registerOperationRequestCredentialController(),
		alwaysSuccessClusterValidationControllerName:                   registerAlwaysSuccessClusterValidationController(),
		controlPlaneActiveVersionsControllerName:                       registerControlPlaneActiveVersionsController(),
		controlPlaneDesiredVersionControllerName:                       registerControlPlaneDesiredVersionController(),
		triggerControlPlaneUpgradeControllerName:                       registerTriggerControlPlaneUpgradeController(),
		clusterBaseDomainPrefixSyncControllerName:                      registerClusterBaseDomainPrefixSyncController(),
		clusterPropertiesSyncControllerName:                            registerClusterPropertiesSyncController(),
		clusterIdentitySyncControllerName:                              registerClusterIdentitySyncController(),
		clusterDegradedAggregatorControllerName:                        registerClusterDegradedAggregatorController(),
		clusterRequirementsValidAggregatorControllerName:               registerClusterRequirementsValidAggregatorController(),
		desiredControlPlaneSizeControllerName:                          registerDesiredControlPlaneSizeController(),
		serviceProviderClusterPropertiesSyncControllerName:             registerServiceProviderClusterPropertiesSyncController(),
		azureRPRegistrationValidationControllerName:                    registerAzureRPRegistrationValidationController(),
		azureClusterResourceGroupExistenceValidationControllerName:     registerAzureClusterResourceGroupExistenceValidationController(),
		azureClusterManagedIdentitiesExistenceValidationControllerName: registerAzureClusterManagedIdentitiesExistenceValidationController(),
		controlPlaneIdentitiesPermissionsValidationControllerName:      registerControlPlaneIdentitiesPermissionsValidationController(),
		dataPlaneIdentitiesPermissionsValidationControllerName:         registerDataPlaneIdentitiesPermissionsValidationController(),
		containerRegistryPullCredentialsValidationControllerName:       registerContainerRegistryPullCredentialsValidationController(),
		createClusterScopedReadDesiresControllerName:                   registerCreateClusterScopedReadDesiresController(),
		createServiceProviderClusterControllerName:                     registerCreateServiceProviderClusterController(),
		cleanOrphanedClusterManagedResourceGroupControllerName:         registerCleanOrphanedClusterManagedResourceGroupController(),
		ensureManagedResourceGroupControllerName:                       registerEnsureManagedResourceGroupController(),
		clusterDeletionClusterServiceDeleteDispatchControllerName:      registerClusterDeletionClusterServiceDeleteDispatchController(),
		clusterClusterServiceIDClearerControllerName:                   registerClusterClusterServiceIDClearerController(),
		clusterCredentialDeletionMarkerControllerName:                  registerClusterCredentialDeletionMarkerController(),
		clusterChildResourcesCleanupControllerName:                     registerClusterChildResourcesCleanupController(),
		clusterDeletionControllerName:                                  registerClusterDeletionController(),
		clusterClusterServiceUpdateDispatchControllerName:              registerClusterClusterServiceUpdateDispatchController(),
		placementSyncControllerName:                                    registerPlacementSyncController(),
		placementControllerName:                                        registerPlacementController(),
		pendingCleanupControllerName:                                   registerPendingCleanupController(),
		backupScheduleControllerName:                                   registerBackupScheduleController(),
		fetchMSIIdentitiesInfoControllerName:                           registerFetchMSIIdentitiesInfoController(),
		fetchDataPlaneOperatorsManagedIdentitiesInfoControllerName:     registerFetchDataPlaneOperatorsManagedIdentitiesInfoController(),
		identityRoleAssignmentsControllerName:                          registerIdentityRoleAssignmentsController(),
		keyRotationBackupControllerName:                                registerKeyRotationBackupController(),

		// clusterresources
		clusterResourcesControllerName: registerClusterResourcesController(),

		// cosmosmigration
		cosmosMigrationControllerName: registerCosmosMigrationController(),

		// datadump
		subscriptionNonClusterDataDumpControllerName: registerSubscriptionNonClusterDataDumpController(),
		clusterRecursiveDataDumpControllerName:       registerClusterRecursiveDataDumpController(),
		csStateDumpControllerName:                    registerCsStateDumpController(),
		billingDumpControllerName:                    registerBillingDumpController(),
		managementClusterDumpControllerName:          registerManagementClusterDumpController(),

		// externalauth
		externalAuthClusterServiceCreateControllerName:                 registerExternalAuthClusterServiceCreateController(),
		operationExternalAuthCreateControllerName:                      registerOperationExternalAuthCreateController(),
		operationExternalAuthUpdateControllerName:                      registerOperationExternalAuthUpdateController(),
		operationExternalAuthDeleteControllerName:                      registerOperationExternalAuthDeleteController(),
		externalAuthDegradedAggregatorControllerName:                   registerExternalAuthDegradedAggregatorController(),
		externalAuthDeletionClusterServiceDeleteDispatchControllerName: registerExternalAuthDeletionClusterServiceDeleteDispatchController(),
		externalAuthClusterServiceIDClearerControllerName:              registerExternalAuthClusterServiceIDClearerController(),
		externalAuthChildResourcesCleanupControllerName:                registerExternalAuthChildResourcesCleanupController(),
		externalAuthDeletionControllerName:                             registerExternalAuthDeletionController(),
		externalAuthClusterServiceUpdateDispatchControllerName:         registerExternalAuthClusterServiceUpdateDispatchController(),

		// metrics
		operationPhaseMetricsControllerName: registerOperationPhaseMetricsController(),
		clusterMetricsControllerName:        registerClusterMetricsController(),
		clusterVersionMetricsControllerName: registerClusterVersionMetricsController(),
		nodePoolMetricsControllerName:       registerNodePoolMetricsController(),
		externalAuthMetricsControllerName:   registerExternalAuthMetricsController(),
		clusterInfoMetricsControllerName:    registerClusterInfoMetricsController(),

		// mismatch
		clusterServiceMatchingClusterControllerName: registerClusterServiceMatchingClusterController(),
		deleteOrphanedCosmosResourcesControllerName: registerDeleteOrphanedCosmosResourcesController(),
		missingResourceIDControllerName:             registerMissingResourceIDController(),
		backfillClusterUIDControllerName:            registerBackfillClusterUIDController(),

		// nodepool
		nodePoolClusterServiceCreateControllerName:                   registerNodePoolClusterServiceCreateController(),
		operationNodePoolCreateControllerName:                        registerOperationNodePoolCreateController(),
		operationNodePoolUpdateControllerName:                        registerOperationNodePoolUpdateController(),
		operationNodePoolDeleteControllerName:                        registerOperationNodePoolDeleteController(),
		nodePoolDegradedAggregatorControllerName:                     registerNodePoolDegradedAggregatorController(),
		nodePoolRequirementsValidAggregatorControllerName:            registerNodePoolRequirementsValidAggregatorController(),
		azureVMSizeSupportsEphemeralOSDiskValidationControllerName:   registerAzureVMSizeSupportsEphemeralOSDiskValidationController(),
		azureNodePoolVMQuotaValidationControllerName:                 registerAzureNodePoolVMQuotaValidationController(),
		nodePoolNSGBasedRequiredConnectivityValidationControllerName: registerNodePoolNSGBasedRequiredConnectivityValidationController(),
		nodePoolVersionControllerName:                                registerNodePoolVersionController(),
		nodePoolActiveVersionControllerName:                          registerNodePoolActiveVersionController(),
		createNodePoolScopedReadDesiresControllerName:                registerCreateNodePoolScopedReadDesiresController(),
		createServiceProviderNodePoolControllerName:                  registerCreateServiceProviderNodePoolController(),
		triggerNodePoolUpgradeControllerName:                         registerTriggerNodePoolUpgradeController(),
		nodePoolDeletionClusterServiceDeleteDispatchControllerName:   registerNodePoolDeletionClusterServiceDeleteDispatchController(),
		nodePoolClusterServiceIDClearerControllerName:                registerNodePoolClusterServiceIDClearerController(),
		nodePoolChildResourcesCleanupControllerName:                  registerNodePoolChildResourcesCleanupController(),
		nodePoolDeletionControllerName:                               registerNodePoolDeletionController(),
		nodePoolClusterServiceUpdateDispatchControllerName:           registerNodePoolClusterServiceUpdateDispatchController(),

		// Supporting controllers
		unionKubeApplierInformersControllerName:              registerUnionKubeApplierInformersController(),
		virtualMachineResourceSKUsCachedReaderControllerName: registerVirtualMachineResourceSKUsCachedReaderController(),
	}
}

func controllerConstructionOrder() []string {
	return []string{
		operationPhaseMetricsControllerName,
		unionKubeApplierInformersControllerName,
		clusterMetricsControllerName,
		clusterVersionMetricsControllerName,
		clusterInfoMetricsControllerName,
		nodePoolMetricsControllerName,
		externalAuthMetricsControllerName,
		subscriptionNonClusterDataDumpControllerName,
		clusterRecursiveDataDumpControllerName,
		csStateDumpControllerName,
		billingDumpControllerName,
		managementClusterDumpControllerName,
		dispatchRequestCredentialControllerName,
		adminCredentialsDispatchRequestCredentialControllerName,
		adminCredentialsDispatchRevokeCredentialsControllerName,
		adminCredentialsOperationRequestCredentialPollControllerName,
		adminCredentialsOperationRevokeCredentialsPollControllerName,
		adminCredentialsIssuanceObserverControllerName,
		adminCredentialsDesiresCreatorControllerName,
		adminCredentialsPostIssuanceCleanupControllerName,
		adminCredentialsRevokedGCControllerName,
		adminCredentialsClusterDeletionCleanupControllerName,
		systemAdminCredentialRevocationMarkRequestsControllerName,
		systemAdminCredentialRevocationDesiresControllerName,
		systemAdminCredentialRevocationCompletionControllerName,
		systemAdminCredentialRevocationDeletionControllerName,
		operationClusterCreateControllerName,
		operationClusterUpdateControllerName,
		operationClusterDeleteControllerName,
		operationNodePoolCreateControllerName,
		operationNodePoolUpdateControllerName,
		operationNodePoolDeleteControllerName,
		operationExternalAuthCreateControllerName,
		operationExternalAuthUpdateControllerName,
		operationExternalAuthDeleteControllerName,
		operationRequestCredentialControllerName,
		clusterServiceMatchingClusterControllerName,
		alwaysSuccessClusterValidationControllerName,
		deleteOrphanedCosmosResourcesControllerName,
		missingResourceIDControllerName,
		backfillClusterUIDControllerName,
		orphanedBillingCleanupControllerName,
		createBillingDocControllerName,
		controlPlaneActiveVersionsControllerName,
		controlPlaneDesiredVersionControllerName,
		triggerControlPlaneUpgradeControllerName,
		clusterBaseDomainPrefixSyncControllerName,
		clusterPropertiesSyncControllerName,
		clusterIdentitySyncControllerName,
		desiredControlPlaneSizeControllerName,
		serviceProviderClusterPropertiesSyncControllerName,
		backupScheduleControllerName,
		keyRotationBackupControllerName,
		clusterDegradedAggregatorControllerName,
		clusterRequirementsValidAggregatorControllerName,
		nodePoolDegradedAggregatorControllerName,
		nodePoolRequirementsValidAggregatorControllerName,
		externalAuthDegradedAggregatorControllerName,
		createClusterScopedReadDesiresControllerName,
		createNodePoolScopedReadDesiresControllerName,
		cosmosMigrationControllerName,
		createServiceProviderClusterControllerName,
		createServiceProviderNodePoolControllerName,
		cleanOrphanedClusterManagedResourceGroupControllerName,
		ensureManagedResourceGroupControllerName,
		virtualMachineResourceSKUsCachedReaderControllerName,
		azureRPRegistrationValidationControllerName,
		azureClusterResourceGroupExistenceValidationControllerName,
		azureClusterManagedIdentitiesExistenceValidationControllerName,
		containerRegistryPullCredentialsValidationControllerName,
		azureVMSizeSupportsEphemeralOSDiskValidationControllerName,
		azureNodePoolVMQuotaValidationControllerName,
		controlPlaneIdentitiesPermissionsValidationControllerName,
		dataPlaneIdentitiesPermissionsValidationControllerName,
		nodePoolNSGBasedRequiredConnectivityValidationControllerName,
		nodePoolVersionControllerName,
		nodePoolActiveVersionControllerName,
		triggerNodePoolUpgradeControllerName,
		placementSyncControllerName,
		placementControllerName,
		pendingCleanupControllerName,
		nodePoolClusterServiceCreateControllerName,
		externalAuthClusterServiceCreateControllerName,
		nodePoolDeletionClusterServiceDeleteDispatchControllerName,
		nodePoolClusterServiceIDClearerControllerName,
		nodePoolChildResourcesCleanupControllerName,
		nodePoolDeletionControllerName,
		externalAuthDeletionClusterServiceDeleteDispatchControllerName,
		externalAuthClusterServiceIDClearerControllerName,
		externalAuthChildResourcesCleanupControllerName,
		externalAuthDeletionControllerName,
		clusterDenyAssignmentControllerName,
		clusterPendingClusterServiceIDAssignControllerName,
		clusterClusterServiceCreateControllerName,
		clusterDeletionClusterServiceDeleteDispatchControllerName,
		clusterClusterServiceIDClearerControllerName,
		clusterCredentialDeletionMarkerControllerName,
		clusterChildResourcesCleanupControllerName,
		clusterDeletionControllerName,
		clusterClusterServiceUpdateDispatchControllerName,
		nodePoolClusterServiceUpdateDispatchControllerName,
		externalAuthClusterServiceUpdateDispatchControllerName,
		fetchMSIIdentitiesInfoControllerName,
		fetchDataPlaneOperatorsManagedIdentitiesInfoControllerName,
		identityRoleAssignmentsControllerName,
		clusterResourcesControllerName,
	}
}

func controllerLaunchOrder() []string {
	return []string{
		unionKubeApplierInformersControllerName,
		subscriptionNonClusterDataDumpControllerName,
		clusterRecursiveDataDumpControllerName,
		csStateDumpControllerName,
		billingDumpControllerName,
		managementClusterDumpControllerName,
		dispatchRequestCredentialControllerName,
		adminCredentialsDispatchRequestCredentialControllerName,
		adminCredentialsDispatchRevokeCredentialsControllerName,
		adminCredentialsOperationRequestCredentialPollControllerName,
		adminCredentialsOperationRevokeCredentialsPollControllerName,
		adminCredentialsIssuanceObserverControllerName,
		adminCredentialsDesiresCreatorControllerName,
		adminCredentialsPostIssuanceCleanupControllerName,
		adminCredentialsRevokedGCControllerName,
		adminCredentialsClusterDeletionCleanupControllerName,
		systemAdminCredentialRevocationMarkRequestsControllerName,
		systemAdminCredentialRevocationDesiresControllerName,
		systemAdminCredentialRevocationCompletionControllerName,
		systemAdminCredentialRevocationDeletionControllerName,
		clusterDenyAssignmentControllerName,
		clusterPendingClusterServiceIDAssignControllerName,
		clusterClusterServiceCreateControllerName,
		nodePoolClusterServiceCreateControllerName,
		externalAuthClusterServiceCreateControllerName,
		operationClusterCreateControllerName,
		operationClusterUpdateControllerName,
		operationClusterDeleteControllerName,
		operationNodePoolCreateControllerName,
		operationNodePoolUpdateControllerName,
		operationNodePoolDeleteControllerName,
		operationExternalAuthCreateControllerName,
		operationExternalAuthUpdateControllerName,
		operationExternalAuthDeleteControllerName,
		operationRequestCredentialControllerName,
		clusterServiceMatchingClusterControllerName,
		alwaysSuccessClusterValidationControllerName,
		deleteOrphanedCosmosResourcesControllerName,
		missingResourceIDControllerName,
		backfillClusterUIDControllerName,
		orphanedBillingCleanupControllerName,
		createBillingDocControllerName,
		controlPlaneActiveVersionsControllerName,
		controlPlaneDesiredVersionControllerName,
		triggerControlPlaneUpgradeControllerName,
		clusterBaseDomainPrefixSyncControllerName,
		clusterPropertiesSyncControllerName,
		clusterIdentitySyncControllerName,
		clusterDegradedAggregatorControllerName,
		clusterRequirementsValidAggregatorControllerName,
		nodePoolDegradedAggregatorControllerName,
		nodePoolRequirementsValidAggregatorControllerName,
		externalAuthDegradedAggregatorControllerName,
		desiredControlPlaneSizeControllerName,
		serviceProviderClusterPropertiesSyncControllerName,
		azureRPRegistrationValidationControllerName,
		azureClusterResourceGroupExistenceValidationControllerName,
		azureClusterManagedIdentitiesExistenceValidationControllerName,
		azureVMSizeSupportsEphemeralOSDiskValidationControllerName,
		azureNodePoolVMQuotaValidationControllerName,
		controlPlaneIdentitiesPermissionsValidationControllerName,
		nodePoolNSGBasedRequiredConnectivityValidationControllerName,
		dataPlaneIdentitiesPermissionsValidationControllerName,
		containerRegistryPullCredentialsValidationControllerName,
		nodePoolVersionControllerName,
		nodePoolActiveVersionControllerName,
		createClusterScopedReadDesiresControllerName,
		createNodePoolScopedReadDesiresControllerName,
		createServiceProviderClusterControllerName,
		createServiceProviderNodePoolControllerName,
		cleanOrphanedClusterManagedResourceGroupControllerName,
		ensureManagedResourceGroupControllerName,
		triggerNodePoolUpgradeControllerName,
		nodePoolDeletionClusterServiceDeleteDispatchControllerName,
		nodePoolClusterServiceIDClearerControllerName,
		nodePoolChildResourcesCleanupControllerName,
		nodePoolDeletionControllerName,
		externalAuthDeletionClusterServiceDeleteDispatchControllerName,
		externalAuthClusterServiceIDClearerControllerName,
		externalAuthChildResourcesCleanupControllerName,
		externalAuthDeletionControllerName,
		clusterDeletionClusterServiceDeleteDispatchControllerName,
		clusterClusterServiceIDClearerControllerName,
		clusterCredentialDeletionMarkerControllerName,
		clusterChildResourcesCleanupControllerName,
		clusterDeletionControllerName,
		clusterClusterServiceUpdateDispatchControllerName,
		nodePoolClusterServiceUpdateDispatchControllerName,
		externalAuthClusterServiceUpdateDispatchControllerName,
		operationPhaseMetricsControllerName,
		clusterMetricsControllerName,
		clusterVersionMetricsControllerName,
		nodePoolMetricsControllerName,
		externalAuthMetricsControllerName,
		clusterInfoMetricsControllerName,
		placementSyncControllerName,
		placementControllerName,
		pendingCleanupControllerName,
		cosmosMigrationControllerName,
		virtualMachineResourceSKUsCachedReaderControllerName,
		backupScheduleControllerName,
		fetchMSIIdentitiesInfoControllerName,
		fetchDataPlaneOperatorsManagedIdentitiesInfoControllerName,
		identityRoleAssignmentsControllerName,
		keyRotationBackupControllerName,
		clusterResourcesControllerName,
	}
}
