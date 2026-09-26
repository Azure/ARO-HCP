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
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billing"
	clusterazureresources "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/azureresources"
	clusterbackups "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
	clustercreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/creation"
	credentialrequestcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/creation"
	credentialrequestdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/deletion"
	credentialrequestoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/operations"
	credentialrevocationcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/creation"
	credentialrevocationdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/deletion"
	credentialrevocationoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/operations"
	clusterdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/deletion"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/denyassignments"
	clusteridentity "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/identity"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/legacycredentialrequest"
	clusteroperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/operations"
	clusterplacement "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/placement"
	clusterproperties "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/properties"
	clusterreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/readdesires"
	clusterroleassignments "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/roleassignments"
	clusterstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/status"
	clusterupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/update"
	clustervalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/validation"
	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterresources"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cosmosmigration"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadump"
	externalauthcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/creation"
	externalauthdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/deletion"
	externalauthoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/operations"
	externalauthstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/status"
	externalauthupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/update"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	nodepoolcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/creation"
	nodepooldeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/deletion"
	nodepooloperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/operations"
	nodepoolreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/readdesires"
	nodepoolstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/status"
	nodepoolupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/update"
	nodepoolvalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/validation"
	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

const (
	BackendInformersStorageName      = "BackendInformers"
	FleetInformersStorageName        = "FleetInformers"
	BackfillClusterUIDControllerName = "BackfillClusterUID"
	CreateBillingDocControllerName   = "CreateBillingDoc"
)

// BackendStorageControllerNames lists the storage consumers registered by the
// backend, including unlimited shared informer clients. Read-only metrics
// controllers do not use storage clients. The FPA-only controller is included
// only when enabled.
func BackendStorageControllerNames(hasRealFPA bool) []string {
	names := []string{
		BackendInformersStorageName,
		FleetInformersStorageName,
		unionkubeapplierinformers.UnionKubeApplierInformersControllerName,
		datadump.SubscriptionNonClusterDataDumpControllerName,
		datadump.ClusterRecursiveDataDumpControllerName,
		datadump.CSStateDumpControllerName,
		datadump.BillingDumpControllerName,
		datadump.ManagementClusterDataDumpControllerName,
		legacycredentialrequest.DispatchRequestCredentialControllerName,
		credentialrequestoperations.DispatchRequestCredentialControllerName,
		credentialrevocationoperations.DispatchRevokeCredentialsControllerName,
		credentialrequestoperations.OperationRequestCredentialPollControllerName,
		credentialrevocationoperations.OperationRevokeCredentialsPollControllerName,
		credentialrequestcreation.IssuanceObserverControllerName,
		credentialrequestcreation.DesiresCreatorControllerName,
		credentialrequestdeletion.PostIssuanceCleanupControllerName,
		credentialrequestdeletion.RevokedGCControllerName,
		credentialrequestdeletion.ClusterDeletionCleanupControllerName,
		credentialrevocationcreation.RevocationMarkRequestsControllerName,
		credentialrevocationcreation.RevocationDesiresControllerName,
		credentialrevocationdeletion.RevocationCompletionControllerName,
		credentialrevocationdeletion.RevocationDeletionControllerName,
		clusteroperations.OperationClusterCreateControllerName,
		clusteroperations.OperationClusterUpdateControllerName,
		clusteroperations.OperationClusterDeleteControllerName,
		nodepooloperations.OperationNodePoolCreateControllerName,
		nodepooloperations.OperationNodePoolUpdateControllerName,
		nodepooloperations.OperationNodePoolDeleteControllerName,
		externalauthoperations.OperationExternalAuthCreateControllerName,
		externalauthoperations.OperationExternalAuthUpdateControllerName,
		externalauthoperations.OperationExternalAuthDeleteControllerName,
		legacycredentialrequest.OperationRequestCredentialControllerName,
		mismatch.ClusterServiceClusterMatchingControllerName,
		clustervalidation.ControllerName((&validationutils.AlwaysSuccessValidation{}).Name()),
		mismatch.DeleteOrphanedCosmosResourcesControllerName,
		mismatch.MissingResourceIDControllerName,
		BackfillClusterUIDControllerName,
		billing.OrphanedBillingCleanupControllerName,
		CreateBillingDocControllerName,
		clusterversion.ControlPlaneActiveVersionControllerName,
		clusterversion.ControlPlaneDesiredVersionControllerName,
		clusterversion.TriggerControlPlaneUpgradeControllerName,
		clusterproperties.ClusterBaseDomainPrefixSyncControllerName,
		clusterproperties.ClusterPropertiesSyncControllerName,
		clusteridentity.ClusterIdentitySyncControllerName,
		clusterproperties.DesiredControlPlaneSizeControllerName,
		clusterproperties.ServiceProviderClusterPropertiesSyncControllerName,
		clusterbackups.BackupScheduleControllerName,
		clusterbackups.KeyRotationBackupControllerName,
		clusterstatus.ClusterDegradedAggregatorControllerName,
		clusterstatus.ClusterRequirementsValidAggregatorControllerName,
		nodepoolstatus.NodePoolDegradedAggregatorControllerName,
		nodepoolstatus.NodePoolRequirementsValidAggregatorControllerName,
		externalauthstatus.ExternalAuthDegradedAggregatorControllerName,
		clusterreaddesires.CreateClusterScopedReadDesiresControllerName,
		nodepoolreaddesires.CreateNodePoolScopedReadDesiresControllerName,
		cosmosmigration.CosmosMigrationControllerName,
		clustercreation.CreateServiceProviderClusterControllerName,
		nodepoolcreation.CreateServiceProviderNodePoolControllerName,
		clusterdeletion.CleanOrphanedClusterManagedResourceGroupControllerName,
		clusterazureresources.ManagedResourceGroupControllerName,
		clustervalidation.ControllerName((&validationutils.AzureResourceProvidersRegistrationValidation{}).Name()),
		clustervalidation.ControllerName((&validationutils.AzureClusterResourceGroupExistenceValidation{}).Name()),
		clustervalidation.ControllerName((&validationutils.AzureClusterManagedIdentitiesExistenceValidation{}).Name()),
		clustervalidation.ControllerName((&validationutils.ContainerRegistryPullCredentialsPermissionValidation{}).Name()),
		nodepoolvalidation.ControllerName((&validationutils.AzureVMSizeSupportsEphemeralOSDiskValidation{}).Name()),
		nodepoolvalidation.ControllerName((&validationutils.AzureNodePoolVMQuotaValidation{}).Name()),
		clustervalidation.ControllerName((&validationutils.ControlPlaneIdentitiesPermissionsClusterValidation{}).Name()),
		clustervalidation.ControllerName((&validationutils.DataPlaneIdentitiesPermissionsValidation{}).Name()),
		nodepoolvalidation.ControllerName((&validationutils.AzureNodePoolNSGBasedRequiredConnectivityValidation{}).Name()),
		nodepoolversion.NodepoolVersionControllerName,
		nodepoolversion.NodePoolActiveVersionsControllerName,
		nodepoolversion.TriggerNodePoolUpgradeControllerName,
		clusterplacement.ManagementClusterPlacementSyncControllerName,
		clusterplacement.PlacementControllerName,
		clusterplacement.PendingCleanupControllerName,
		nodepoolcreation.NodePoolClusterServiceCreateControllerName,
		externalauthcreation.ExternalAuthClusterServiceCreateControllerName,
		nodepooldeletion.NodePoolClusterServiceDeleteDispatchControllerName,
		nodepooldeletion.NodePoolClusterServiceIDClearerControllerName,
		nodepooldeletion.NodePoolChildResourcesCleanupControllerName,
		nodepooldeletion.NodePoolDeletionControllerName,
		externalauthdeletion.ExternalAuthClusterServiceDeleteDispatchControllerName,
		externalauthdeletion.ExternalAuthClusterServiceIDClearerControllerName,
		externalauthdeletion.ExternalAuthChildResourcesCleanupControllerName,
		externalauthdeletion.ExternalAuthDeletionControllerName,
		clustercreation.ClusterPendingClusterServiceIDAssignControllerName,
		clustercreation.ClusterClusterServiceCreateControllerName,
		clusterdeletion.ClusterClusterServiceDeleteDispatchControllerName,
		clusterdeletion.ClusterClusterServiceIDClearerControllerName,
		clusterdeletion.ClusterCredentialDeletionMarkerControllerName,
		clusterdeletion.ClusterChildResourcesCleanupControllerName,
		clusterdeletion.ClusterDeletionControllerName,
		clusterupdate.ClusterClusterServiceUpdateDispatchControllerName,
		nodepoolupdate.NodePoolClusterServiceUpdateDispatchControllerName,
		externalauthupdate.ExternalAuthClusterServiceUpdateDispatchControllerName,
		clusteridentity.FetchMSIIdentitiesInfoControllerName,
		clusteridentity.FetchDataPlaneOperatorsManagedIdentitiesInfoControllerName,
		clusterroleassignments.RoleAssignmentsControllerName,
		clusterresources.ClusterResourcesControllerName,
	}
	if hasRealFPA {
		names = append(names, denyassignments.ClusterDenyAssignmentControllerName)
	}
	return names
}

// BackendCleanupControllerFractions reduces background cleanup work's RU share.
// Controllers on the customer deletion path retain the normal budget.
func BackendCleanupControllerFractions(fraction float64) map[string]float64 {
	return map[string]float64{
		mismatch.DeleteOrphanedCosmosResourcesControllerName:                   fraction,
		billing.OrphanedBillingCleanupControllerName:                           fraction,
		clusterplacement.PendingCleanupControllerName:                          fraction,
		clusterdeletion.CleanOrphanedClusterManagedResourceGroupControllerName: fraction,
		credentialrequestdeletion.RevokedGCControllerName:                      fraction,
	}
}

// BackendStorageFactoryOptions assigns independent RU budgets to each physical
// container. Resources, Billing, and Fleet use 80% of their configured maxima.
// Each MC container reserves 30% for the backend and 50% for kube-applier, leaving
// 20% headroom. Cleanup controllers get 10% of a normal controller's share.
// Shared backend informers use unlimited clients outside these allocations.
func BackendStorageFactoryOptions(hasRealFPA bool) StorageFactoryOptions {
	return StorageFactoryOptions{
		ResourcesRUsPerSecond:   19000,
		BillingRUsPerSecond:     4000,
		FleetRUsPerSecond:       4000,
		KubeApplierRUsPerSecond: 19000,
		KubeApplierUtilization:  0.3,
		ControllerNames:         BackendStorageControllerNames(hasRealFPA),
		ControllerFractions:     BackendCleanupControllerFractions(0.1),
		UnlimitedControllerNames: []string{
			BackendInformersStorageName,
			FleetInformersStorageName,
			unionkubeapplierinformers.UnionKubeApplierInformersControllerName,
		},
	}
}
