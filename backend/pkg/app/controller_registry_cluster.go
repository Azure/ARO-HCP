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
	utilsclock "k8s.io/utils/clock"

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
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
)

const (
	dispatchRequestCredentialControllerName                        = "dispatchrequestcredential"
	adminCredentialsDispatchRequestCredentialControllerName        = "systemadmincredentialdispatchrequestcredential"
	adminCredentialsDispatchRevokeCredentialsControllerName        = "systemadmincredentialdispatchrevokecredentials"
	adminCredentialsOperationRequestCredentialPollControllerName   = "systemadmincredentialoperationrequestcredentialpoll"
	adminCredentialsOperationRevokeCredentialsPollControllerName   = "systemadmincredentialoperationrevokecredentialspoll"
	adminCredentialsIssuanceObserverControllerName                 = "systemadmincredentialissuanceobserver"
	adminCredentialsDesiresCreatorControllerName                   = "systemadmincredentialdesirescreator"
	adminCredentialsPostIssuanceCleanupControllerName              = "systemadmincredentialpostissuancecleanup"
	adminCredentialsRevokedGCControllerName                        = "systemadmincredentialrevokedgc"
	adminCredentialsClusterDeletionCleanupControllerName           = "systemadmincredentialclusterdeletioncleanup"
	systemAdminCredentialRevocationMarkRequestsControllerName      = "systemadmincredentialrevocationmarkrequests"
	systemAdminCredentialRevocationDesiresControllerName           = "systemadmincredentialrevocationdesires"
	systemAdminCredentialRevocationCompletionControllerName        = "systemadmincredentialrevocationcompletion"
	systemAdminCredentialRevocationDeletionControllerName          = "systemadmincredentialrevocationdeletion"
	clusterDenyAssignmentControllerName                            = "clusterdenyassignment"
	clusterPendingClusterServiceIDAssignControllerName             = "clusterpendingclusterserviceidassign"
	clusterClusterServiceCreateControllerName                      = "clusterclusterservicecreate"
	operationClusterCreateControllerName                           = "operationclustercreate"
	operationClusterUpdateControllerName                           = "operationclusterupdate"
	operationClusterDeleteControllerName                           = "operationclusterdelete"
	operationRequestCredentialControllerName                       = "operationrequestcredential"
	alwaysSuccessClusterValidationControllerName                   = "clustervalidationalwayssuccessvalidation"
	controlPlaneActiveVersionsControllerName                       = "controlplaneactiveversions"
	controlPlaneDesiredVersionControllerName                       = "controlplanedesiredversion"
	triggerControlPlaneUpgradeControllerName                       = "triggercontrolplaneupgrade"
	clusterBaseDomainPrefixSyncControllerName                      = "clusterbasedomainprefixsync"
	clusterPropertiesSyncControllerName                            = "clusterpropertiessync"
	clusterIdentitySyncControllerName                              = "clusteridentitysync"
	clusterDegradedAggregatorControllerName                        = "clusterdegradedaggregator"
	clusterRequirementsValidAggregatorControllerName               = "clusterrequirementsvalidaggregator"
	desiredControlPlaneSizeControllerName                          = "desiredcontrolplanesize"
	serviceProviderClusterPropertiesSyncControllerName             = "serviceproviderclusterpropertiessync"
	azureRPRegistrationValidationControllerName                    = "clustervalidationazureresourceprovidersregistrationvalidation"
	azureClusterResourceGroupExistenceValidationControllerName     = "clustervalidationazureclusterresourcegroupexistencevalidation"
	azureClusterManagedIdentitiesExistenceValidationControllerName = "clustervalidationazureclustermanagedidentitiesexistencevalidation"
	controlPlaneIdentitiesPermissionsValidationControllerName      = "clustervalidationcontrolplaneidentitiespermissionsclustervalidation"
	dataPlaneIdentitiesPermissionsValidationControllerName         = "clustervalidationdataplaneidentitiespermissionsvalidation"
	containerRegistryPullCredentialsValidationControllerName       = "clustervalidationcontainerregistrypullcredentialspermissionvalidation"
	createClusterScopedReadDesiresControllerName                   = "createclusterscopedreaddesires"
	createServiceProviderClusterControllerName                     = "createserviceprovidercluster"
	cleanOrphanedClusterManagedResourceGroupControllerName         = "cleanorphanedclustermanagedresourcegroup"
	ensureManagedResourceGroupControllerName                       = "ensuremanagedresourcegroup"
	clusterDeletionClusterServiceDeleteDispatchControllerName      = "clusterclusterservicedeletedispatch"
	clusterClusterServiceIDClearerControllerName                   = "clusterdeletionclusterserviceidclearer"
	clusterCredentialDeletionMarkerControllerName                  = "clustercredentialdeletionmarkercontroller"
	clusterChildResourcesCleanupControllerName                     = "clusterchildresourcescleanupcontroller"
	clusterDeletionControllerName                                  = "clusterdeletioncontroller"
	clusterClusterServiceUpdateDispatchControllerName              = "clusterclusterserviceupdatedispatch"
	placementSyncControllerName                                    = "managementclusterplacementsync"
	placementControllerName                                        = "placement"
	pendingCleanupControllerName                                   = "pendingcleanup"
	backupScheduleControllerName                                   = "backupschedule"
	fetchMSIIdentitiesInfoControllerName                           = "fetchmsiidentitiesinfo"
	fetchDataPlaneOperatorsManagedIdentitiesInfoControllerName     = "fetchdataplaneoperatorsmanagedidentitiesinfo"
	identityRoleAssignmentsControllerName                          = "identityroleassignments"
	keyRotationBackupControllerName                                = "keyrotationbackup"
)

func registerDispatchRequestCredentialController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateDispatchRequestCredentialController,
	}
}

func instantiateDispatchRequestCredentialController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return legacycredentialrequest.NewDispatchRequestCredentialController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsDispatchRequestCredentialController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsDispatchRequestCredentialController,
	}
}

func instantiateAdminCredentialsDispatchRequestCredentialController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	return credentialrequestoperations.NewDispatchRequestCredentialController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		clusterLister,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsDispatchRevokeCredentialsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsDispatchRevokeCredentialsController,
	}
}

func instantiateAdminCredentialsDispatchRevokeCredentialsController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	return credentialrevocationoperations.NewDispatchRevokeCredentialsController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		clusterLister,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsOperationRequestCredentialPollController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsOperationRequestCredentialPollController,
	}
}

func instantiateAdminCredentialsOperationRequestCredentialPollController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return credentialrequestoperations.NewOperationRequestCredentialPollController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsOperationRevokeCredentialsPollController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsOperationRevokeCredentialsPollController,
	}
}

func instantiateAdminCredentialsOperationRevokeCredentialsPollController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	return credentialrevocationoperations.NewOperationRevokeCredentialsPollController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		clusterLister,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsIssuanceObserverController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsIssuanceObserverController,
	}
}

func instantiateAdminCredentialsIssuanceObserverController(controllerContext ControllerContext) (Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return credentialrequestcreation.NewIssuanceObserverController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerAdminCredentialsDesiresCreatorController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsDesiresCreatorController,
	}
}

func instantiateAdminCredentialsDesiresCreatorController(controllerContext ControllerContext) (Runnable, error) {
	return credentialrequestcreation.NewDesiresCreatorController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerAdminCredentialsPostIssuanceCleanupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsPostIssuanceCleanupController,
	}
}

func instantiateAdminCredentialsPostIssuanceCleanupController(controllerContext ControllerContext) (Runnable, error) {
	return credentialrequestdeletion.NewPostIssuanceCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerAdminCredentialsRevokedGCController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsRevokedGCController,
	}
}

func instantiateAdminCredentialsRevokedGCController(controllerContext ControllerContext) (Runnable, error) {
	return credentialrequestdeletion.NewRevokedGCController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerAdminCredentialsClusterDeletionCleanupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAdminCredentialsClusterDeletionCleanupController,
	}
}

func instantiateAdminCredentialsClusterDeletionCleanupController(controllerContext ControllerContext) (Runnable, error) {
	return credentialrequestdeletion.NewClusterDeletionCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerSystemAdminCredentialRevocationMarkRequestsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateSystemAdminCredentialRevocationMarkRequestsController,
	}
}

func instantiateSystemAdminCredentialRevocationMarkRequestsController(controllerContext ControllerContext) (Runnable, error) {
	return credentialrevocationcreation.NewRevocationMarkRequestsController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerSystemAdminCredentialRevocationDesiresController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateSystemAdminCredentialRevocationDesiresController,
	}
}

func instantiateSystemAdminCredentialRevocationDesiresController(controllerContext ControllerContext) (Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	_, unionApplyDesireLister := controllerContext.UnionKubeApplierInformers.ApplyDesires()
	return credentialrevocationcreation.NewRevocationDesiresController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionApplyDesireLister,
		unionReadDesireLister,
	), nil
}

func registerSystemAdminCredentialRevocationCompletionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateSystemAdminCredentialRevocationCompletionController,
	}
}

func instantiateSystemAdminCredentialRevocationCompletionController(controllerContext ControllerContext) (Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return credentialrevocationdeletion.NewRevocationCompletionController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerSystemAdminCredentialRevocationDeletionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateSystemAdminCredentialRevocationDeletionController,
	}
}

func instantiateSystemAdminCredentialRevocationDeletionController(controllerContext ControllerContext) (Runnable, error) {
	return credentialrevocationdeletion.NewRevocationDeletionController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterDenyAssignmentController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		Enabled:     func(controllerContext ControllerContext) bool { return controllerContext.HasRealFPA },
		instantiate: instantiateClusterDenyAssignmentController,
	}
}

func instantiateClusterDenyAssignmentController(controllerContext ControllerContext) (Runnable, error) {
	return denyassignments.NewClusterDenyAssignmentController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.FPAClientBuilder,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterPendingClusterServiceIDAssignController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterPendingClusterServiceIDAssignController,
	}
}

func instantiateClusterPendingClusterServiceIDAssignController(controllerContext ControllerContext) (Runnable, error) {
	return clustercreation.NewClusterPendingClusterServiceIDAssignController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterClusterServiceCreateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterClusterServiceCreateController,
	}
}

func instantiateClusterClusterServiceCreateController(controllerContext ControllerContext) (Runnable, error) {
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return clustercreation.NewClusterClusterServiceCreateController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		managementClusterLister,
		controllerContext.BackendInformers,
		controllerContext.HasRealFPA,
	), nil
}

func registerOperationClusterCreateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationClusterCreateController,
	}
}

func instantiateOperationClusterCreateController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusteroperations.NewOperationClusterCreateController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
		controllerContext.BackendInformers,
		unionReadDesireLister,
	), nil
}

func registerOperationClusterUpdateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationClusterUpdateController,
	}
}

func instantiateOperationClusterUpdateController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusteroperations.NewOperationClusterUpdateController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		unionReadDesireLister,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
		controllerContext.BackendInformers,
	), nil
}

func registerOperationClusterDeleteController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationClusterDeleteController,
	}
}

func instantiateOperationClusterDeleteController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusteroperations.NewOperationClusterDeleteController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BillingDBClient,
		controllerContext.KubeApplierDBClients,
		unionReadDesireLister,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerOperationRequestCredentialController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationRequestCredentialController,
	}
}

func instantiateOperationRequestCredentialController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return legacycredentialrequest.NewOperationRequestCredentialController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerAlwaysSuccessClusterValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAlwaysSuccessClusterValidationController,
	}
}

func instantiateAlwaysSuccessClusterValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewClusterValidationController(
		validationutils.NewAlwaysSuccessValidation(),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerControlPlaneActiveVersionsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateControlPlaneActiveVersionsController,
	}
}

func instantiateControlPlaneActiveVersionsController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusterversion.NewControlPlaneActiveVersionController(
		controllerContext.ResourcesDBClient,
		clusterLister,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerControlPlaneDesiredVersionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateControlPlaneDesiredVersionController,
	}
}

func instantiateControlPlaneDesiredVersionController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, nodePoolLister := controllerContext.BackendInformers.NodePools()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return clusterversion.NewControlPlaneDesiredVersionController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		clusterLister,
		controllerContext.ClustersServiceClient,
		activeOperationLister,
		serviceProviderClusterLister,
		nodePoolLister,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
	), nil
}

func registerTriggerControlPlaneUpgradeController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateTriggerControlPlaneUpgradeController,
	}
}

func instantiateTriggerControlPlaneUpgradeController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clusterversion.NewTriggerControlPlaneUpgradeController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		clusterLister,
		controllerContext.ClustersServiceClient,
		activeOperationLister,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterBaseDomainPrefixSyncController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterBaseDomainPrefixSyncController,
	}
}

func instantiateClusterBaseDomainPrefixSyncController(controllerContext ControllerContext) (Runnable, error) {
	return clusterproperties.NewClusterBaseDomainPrefixSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterPropertiesSyncController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterPropertiesSyncController,
	}
}

func instantiateClusterPropertiesSyncController(controllerContext ControllerContext) (Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusterproperties.NewClusterPropertiesSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerClusterIdentitySyncController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterIdentitySyncController,
	}
}

func instantiateClusterIdentitySyncController(controllerContext ControllerContext) (Runnable, error) {
	return clusteridentity.NewClusterIdentitySyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterDegradedAggregatorController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterDegradedAggregatorController,
	}
}

func instantiateClusterDegradedAggregatorController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, controllerLister := controllerContext.BackendInformers.Controllers()
	return clusterstatus.NewClusterDegradedAggregatorController(
		controllerContext.ResourcesDBClient,
		clusterLister,
		controllerLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.Clock,
	), nil
}

func registerClusterRequirementsValidAggregatorController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterRequirementsValidAggregatorController,
	}
}

func instantiateClusterRequirementsValidAggregatorController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clusterstatus.NewClusterRequirementsValidAggregatorController(
		controllerContext.ResourcesDBClient,
		clusterLister,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerDesiredControlPlaneSizeController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateDesiredControlPlaneSizeController,
	}
}

func instantiateDesiredControlPlaneSizeController(controllerContext ControllerContext) (Runnable, error) {
	return clusterproperties.NewDesiredControlPlaneSizeController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerServiceProviderClusterPropertiesSyncController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateServiceProviderClusterPropertiesSyncController,
	}
}

func instantiateServiceProviderClusterPropertiesSyncController(controllerContext ControllerContext) (Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusterproperties.NewServiceProviderClusterPropertiesSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerAzureRPRegistrationValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAzureRPRegistrationValidationController,
	}
}

func instantiateAzureRPRegistrationValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewClusterValidationController(
		validationutils.NewAzureResourceProvidersRegistrationValidation(controllerContext.FPAClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerAzureClusterResourceGroupExistenceValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAzureClusterResourceGroupExistenceValidationController,
	}
}

func instantiateAzureClusterResourceGroupExistenceValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewClusterValidationController(
		validationutils.NewAzureClusterResourceGroupExistenceValidation(controllerContext.FPAClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerAzureClusterManagedIdentitiesExistenceValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAzureClusterManagedIdentitiesExistenceValidationController,
	}
}

func instantiateAzureClusterManagedIdentitiesExistenceValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewClusterValidationController(
		validationutils.NewAzureClusterManagedIdentitiesExistenceValidation(controllerContext.SMIClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerControlPlaneIdentitiesPermissionsValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateControlPlaneIdentitiesPermissionsValidationController,
	}
}

func instantiateControlPlaneIdentitiesPermissionsValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewClusterValidationController(
		validationutils.NewControlPlaneIdentitiesPermissionsClusterValidation(
			controllerContext.SMIClientBuilder,
			controllerContext.ClusterScopedIdentitiesConfig,
			controllerContext.BackendIdentityAzureCachedReaders,
			controllerContext.CheckAccessV2ClientBuilder,
			controllerContext.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder,
			controllerContext.CloudEnvironment.CheckAccessV2Scope(),
		),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerDataPlaneIdentitiesPermissionsValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateDataPlaneIdentitiesPermissionsValidationController,
	}
}

func instantiateDataPlaneIdentitiesPermissionsValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewClusterValidationController(
		validationutils.NewDataPlaneIdentitiesPermissionsValidation(
			controllerContext.SMIClientBuilder,
			controllerContext.ClusterScopedIdentitiesConfig,
			controllerContext.BackendIdentityAzureCachedReaders,
			controllerContext.CheckAccessV2ClientBuilder,
		),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerContainerRegistryPullCredentialsValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateContainerRegistryPullCredentialsValidationController,
	}
}

func instantiateContainerRegistryPullCredentialsValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewClusterValidationController(
		validationutils.NewContainerRegistryPullCredentialsPermissionValidation(controllerContext.SMIClientBuilder, controllerContext.CheckAccessV2ClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerCreateClusterScopedReadDesiresController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateCreateClusterScopedReadDesiresController,
	}
}

func instantiateCreateClusterScopedReadDesiresController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusterreaddesires.NewCreateClusterScopedReadDesiresController(
		activeOperationLister, controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients,
		serviceProviderClusterLister, unionReadDesireLister,
		controllerContext.BackendInformers, controllerContext.MaestroSourceEnvironmentIdentifier,
	), nil
}

func registerCreateServiceProviderClusterController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateCreateServiceProviderClusterController,
	}
}

func instantiateCreateServiceProviderClusterController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustercreation.NewCreateServiceProviderClusterController(
		controllerContext.ResourcesDBClient,
		clusterLister,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerCleanOrphanedClusterManagedResourceGroupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateCleanOrphanedClusterManagedResourceGroupController,
	}
}

func instantiateCleanOrphanedClusterManagedResourceGroupController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return clusterdeletion.NewCleanOrphanedClusterManagedResourceGroupController(
		controllerContext.AzureLocation,
		activeOperationLister,
		controllerContext.ResourcesDBClient,
		controllerContext.FPAClientBuilder,
		controllerContext.BackendInformers,
	), nil
}

func registerEnsureManagedResourceGroupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateEnsureManagedResourceGroupController,
	}
}

func instantiateEnsureManagedResourceGroupController(controllerContext ControllerContext) (Runnable, error) {
	_, subscriptionLister := controllerContext.BackendInformers.Subscriptions()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clusterazureresources.NewManagedResourceGroupController(
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		subscriptionLister,
		controllerContext.FPAClientBuilder,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterDeletionClusterServiceDeleteDispatchController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterDeletionClusterServiceDeleteDispatchController,
	}
}

func instantiateClusterDeletionClusterServiceDeleteDispatchController(controllerContext ControllerContext) (Runnable, error) {
	return clusterdeletion.NewClusterClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterClusterServiceIDClearerController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterClusterServiceIDClearerController,
	}
}

func instantiateClusterClusterServiceIDClearerController(controllerContext ControllerContext) (Runnable, error) {
	return clusterdeletion.NewClusterClusterServiceIDClearerController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterCredentialDeletionMarkerController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterCredentialDeletionMarkerController,
	}
}

func instantiateClusterCredentialDeletionMarkerController(controllerContext ControllerContext) (Runnable, error) {
	return clusterdeletion.NewClusterCredentialDeletionMarkerController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterChildResourcesCleanupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterChildResourcesCleanupController,
	}
}

func instantiateClusterChildResourcesCleanupController(controllerContext ControllerContext) (Runnable, error) {
	return clusterdeletion.NewClusterChildResourcesCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterDeletionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterDeletionController,
	}
}

func instantiateClusterDeletionController(controllerContext ControllerContext) (Runnable, error) {
	return clusterdeletion.NewClusterDeletionController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.BillingDBClient,
		controllerContext.BackendInformers,
		controllerContext.KubeApplierDBClients,
	), nil
}

func registerClusterClusterServiceUpdateDispatchController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterClusterServiceUpdateDispatchController,
	}
}

func instantiateClusterClusterServiceUpdateDispatchController(controllerContext ControllerContext) (Runnable, error) {
	return clusterupdate.NewClusterClusterServiceUpdateDispatchController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerPlacementSyncController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiatePlacementSyncController,
	}
}

func instantiatePlacementSyncController(controllerContext ControllerContext) (Runnable, error) {
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return clusterplacement.NewManagementClusterPlacementSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		managementClusterLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerPlacementController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiatePlacementController,
	}
}

func instantiatePlacementController(controllerContext ControllerContext) (Runnable, error) {
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	_, managementClusterSchedulingLister := controllerContext.FleetInformers.ManagementClusterSchedulings()
	return clusterplacement.NewPlacementController(
		controllerContext.ResourcesDBClient,
		controllerContext.FleetDBClient,
		managementClusterLister,
		managementClusterSchedulingLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerPendingCleanupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     5,
		instantiate: instantiatePendingCleanupController,
	}
}

func instantiatePendingCleanupController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clusterplacement.NewPendingCleanupController(
		controllerContext.FleetDBClient,
		serviceProviderClusterLister,
		clusterLister,
		controllerContext.FleetInformers,
	), nil
}

func registerBackupScheduleController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateBackupScheduleController,
	}
}

func instantiateBackupScheduleController(controllerContext ControllerContext) (Runnable, error) {
	return clusterbackups.NewBackupScheduleController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.MaestroSourceEnvironmentIdentifier,
		controllerContext.BackupConfig,
	), nil
}

func registerFetchMSIIdentitiesInfoController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateFetchMSIIdentitiesInfoController,
	}
}

func instantiateFetchMSIIdentitiesInfoController(controllerContext ControllerContext) (Runnable, error) {
	return clusteridentity.NewFetchMSIIdentitiesInfoController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.FPAMIDataplaneClientBuilder,
	), nil
}

func registerFetchDataPlaneOperatorsManagedIdentitiesInfoController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateFetchDataPlaneOperatorsManagedIdentitiesInfoController,
	}
}

func instantiateFetchDataPlaneOperatorsManagedIdentitiesInfoController(controllerContext ControllerContext) (Runnable, error) {
	return clusteridentity.NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.SMIClientBuilder,
	), nil
}

func registerIdentityRoleAssignmentsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateIdentityRoleAssignmentsController,
	}
}

func instantiateIdentityRoleAssignmentsController(controllerContext ControllerContext) (Runnable, error) {
	_, subscriptionLister := controllerContext.BackendInformers.Subscriptions()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clusterroleassignments.NewRoleAssignmentsController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		subscriptionLister,
		controllerContext.FPAClientBuilder,
		controllerContext.ClusterScopedIdentitiesConfig,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerKeyRotationBackupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateKeyRotationBackupController,
	}
}

func instantiateKeyRotationBackupController(controllerContext ControllerContext) (Runnable, error) {
	return clusterbackups.NewKeyRotationBackupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.BackupConfig,
	), nil
}
