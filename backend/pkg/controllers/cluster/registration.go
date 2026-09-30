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

package cluster

import (
	"strings"

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
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
)

func registerDispatchRequestCredentialController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateDispatchRequestCredentialController, false),
	}
}

func instantiateDispatchRequestCredentialController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return legacycredentialrequest.NewDispatchRequestCredentialController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsDispatchRequestCredentialController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsDispatchRequestCredentialController, false),
	}
}

func instantiateAdminCredentialsDispatchRequestCredentialController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	return credentialrequestoperations.NewDispatchRequestCredentialController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		clusterLister,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsDispatchRevokeCredentialsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsDispatchRevokeCredentialsController, false),
	}
}

func instantiateAdminCredentialsDispatchRevokeCredentialsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	return credentialrevocationoperations.NewDispatchRevokeCredentialsController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		clusterLister,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsOperationRequestCredentialPollController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsOperationRequestCredentialPollController, false),
	}
}

func instantiateAdminCredentialsOperationRequestCredentialPollController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return credentialrequestoperations.NewOperationRequestCredentialPollController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerAdminCredentialsOperationRevokeCredentialsPollController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsOperationRevokeCredentialsPollController, false),
	}
}

func instantiateAdminCredentialsOperationRevokeCredentialsPollController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerAdminCredentialsIssuanceObserverController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsIssuanceObserverController, true),
	}
}

func instantiateAdminCredentialsIssuanceObserverController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return credentialrequestcreation.NewIssuanceObserverController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerAdminCredentialsDesiresCreatorController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsDesiresCreatorController, true),
	}
}

func instantiateAdminCredentialsDesiresCreatorController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return credentialrequestcreation.NewDesiresCreatorController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerAdminCredentialsPostIssuanceCleanupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsPostIssuanceCleanupController, true),
	}
}

func instantiateAdminCredentialsPostIssuanceCleanupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return credentialrequestdeletion.NewPostIssuanceCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerAdminCredentialsRevokedGCController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsRevokedGCController, false),
	}
}

func instantiateAdminCredentialsRevokedGCController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return credentialrequestdeletion.NewRevokedGCController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerAdminCredentialsClusterDeletionCleanupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAdminCredentialsClusterDeletionCleanupController, true),
	}
}

func instantiateAdminCredentialsClusterDeletionCleanupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return credentialrequestdeletion.NewClusterDeletionCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerSystemAdminCredentialRevocationMarkRequestsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateSystemAdminCredentialRevocationMarkRequestsController, false),
	}
}

func instantiateSystemAdminCredentialRevocationMarkRequestsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return credentialrevocationcreation.NewRevocationMarkRequestsController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerSystemAdminCredentialRevocationDesiresController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateSystemAdminCredentialRevocationDesiresController, true),
	}
}

func instantiateSystemAdminCredentialRevocationDesiresController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerSystemAdminCredentialRevocationCompletionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateSystemAdminCredentialRevocationCompletionController, true),
	}
}

func instantiateSystemAdminCredentialRevocationCompletionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return credentialrevocationdeletion.NewRevocationCompletionController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerSystemAdminCredentialRevocationDeletionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateSystemAdminCredentialRevocationDeletionController, true),
	}
}

func instantiateSystemAdminCredentialRevocationDeletionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return credentialrevocationdeletion.NewRevocationDeletionController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterDenyAssignmentController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Enabled:     func(controllerContext controllerconfig.ControllerContext) bool { return controllerContext.HasRealFPA },
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterDenyAssignmentController, false),
	}
}

func instantiateClusterDenyAssignmentController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return denyassignments.NewClusterDenyAssignmentController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.FPAClientBuilder,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterPendingClusterServiceIDAssignController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterPendingClusterServiceIDAssignController, false),
	}
}

func instantiateClusterPendingClusterServiceIDAssignController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clustercreation.NewClusterPendingClusterServiceIDAssignController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterClusterServiceCreateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterClusterServiceCreateController, false),
	}
}

func instantiateClusterClusterServiceCreateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return clustercreation.NewClusterClusterServiceCreateController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		managementClusterLister,
		controllerContext.BackendInformers,
		controllerContext.HasRealFPA,
	), nil
}

func registerOperationClusterCreateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationClusterCreateController, true),
	}
}

func instantiateOperationClusterCreateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerOperationClusterUpdateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationClusterUpdateController, true),
	}
}

func instantiateOperationClusterUpdateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerOperationClusterDeleteController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationClusterDeleteController, true),
	}
}

func instantiateOperationClusterDeleteController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerOperationRequestCredentialController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationRequestCredentialController, false),
	}
}

func instantiateOperationRequestCredentialController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return legacycredentialrequest.NewOperationRequestCredentialController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerAlwaysSuccessClusterValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAlwaysSuccessClusterValidationController, false),
	}
}

func instantiateAlwaysSuccessClusterValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewNamedClusterValidationController(
		clustervalidation.ClusterValidationAlwaysSuccessValidationControllerName,
		validationutils.NewAlwaysSuccessValidation(),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerControlPlaneActiveVersionsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateControlPlaneActiveVersionsController, true),
	}
}

func instantiateControlPlaneActiveVersionsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerControlPlaneDesiredVersionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateControlPlaneDesiredVersionController, false),
	}
}

func instantiateControlPlaneDesiredVersionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerTriggerControlPlaneUpgradeController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateTriggerControlPlaneUpgradeController, true),
	}
}

func instantiateTriggerControlPlaneUpgradeController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerClusterBaseDomainPrefixSyncController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterBaseDomainPrefixSyncController, true),
	}
}

func instantiateClusterBaseDomainPrefixSyncController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterproperties.NewClusterBaseDomainPrefixSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterPropertiesSyncController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterPropertiesSyncController, true),
	}
}

func instantiateClusterPropertiesSyncController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusterproperties.NewClusterPropertiesSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerClusterIdentitySyncController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterIdentitySyncController, true),
	}
}

func instantiateClusterIdentitySyncController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusteridentity.NewClusterIdentitySyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterDegradedAggregatorController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterDegradedAggregatorController, true),
	}
}

func instantiateClusterDegradedAggregatorController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerClusterRequirementsValidAggregatorController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterRequirementsValidAggregatorController, false),
	}
}

func instantiateClusterRequirementsValidAggregatorController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clusterstatus.NewClusterRequirementsValidAggregatorController(
		controllerContext.ResourcesDBClient,
		clusterLister,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerDesiredControlPlaneSizeController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateDesiredControlPlaneSizeController, true),
	}
}

func instantiateDesiredControlPlaneSizeController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterproperties.NewDesiredControlPlaneSizeController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerServiceProviderClusterPropertiesSyncController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateServiceProviderClusterPropertiesSyncController, true),
	}
}

func instantiateServiceProviderClusterPropertiesSyncController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusterproperties.NewServiceProviderClusterPropertiesSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerAzureRPRegistrationValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAzureRPRegistrationValidationController, false),
	}
}

func instantiateAzureRPRegistrationValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewNamedClusterValidationController(
		clustervalidation.ClusterValidationAzureResourceProvidersRegistrationValidationControllerName,
		validationutils.NewAzureResourceProvidersRegistrationValidation(controllerContext.FPAClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerAzureClusterResourceGroupExistenceValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAzureClusterResourceGroupExistenceValidationController, false),
	}
}

func instantiateAzureClusterResourceGroupExistenceValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewNamedClusterValidationController(
		clustervalidation.ClusterValidationAzureClusterResourceGroupExistenceValidationControllerName,
		validationutils.NewAzureClusterResourceGroupExistenceValidation(controllerContext.FPAClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerAzureClusterManagedIdentitiesExistenceValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAzureClusterManagedIdentitiesExistenceValidationController, false),
	}
}

func instantiateAzureClusterManagedIdentitiesExistenceValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewNamedClusterValidationController(
		clustervalidation.ClusterValidationAzureClusterManagedIdentitiesExistenceValidationControllerName,
		validationutils.NewAzureClusterManagedIdentitiesExistenceValidation(controllerContext.SMIClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerControlPlaneIdentitiesPermissionsValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateControlPlaneIdentitiesPermissionsValidationController, false),
	}
}

func instantiateControlPlaneIdentitiesPermissionsValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewNamedClusterValidationController(
		clustervalidation.ClusterValidationControlPlaneIdentitiesPermissionsClusterValidationControllerName,
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

func registerDataPlaneIdentitiesPermissionsValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateDataPlaneIdentitiesPermissionsValidationController, false),
	}
}

func instantiateDataPlaneIdentitiesPermissionsValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewNamedClusterValidationController(
		clustervalidation.ClusterValidationDataPlaneIdentitiesPermissionsValidationControllerName,
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

func registerContainerRegistryPullCredentialsValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateContainerRegistryPullCredentialsValidationController, false),
	}
}

func instantiateContainerRegistryPullCredentialsValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustervalidation.NewNamedClusterValidationController(
		clustervalidation.ClusterValidationContainerRegistryPullCredentialsPermissionValidationControllerName,
		validationutils.NewContainerRegistryPullCredentialsPermissionValidation(controllerContext.SMIClientBuilder, controllerContext.CheckAccessV2ClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerCreateClusterScopedReadDesiresController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateCreateClusterScopedReadDesiresController, true),
	}
}

func instantiateCreateClusterScopedReadDesiresController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return clusterreaddesires.NewCreateClusterScopedReadDesiresController(
		activeOperationLister, controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients,
		serviceProviderClusterLister, unionReadDesireLister,
		controllerContext.BackendInformers, controllerContext.MaestroSourceEnvironmentIdentifier,
	), nil
}

func registerCreateServiceProviderClusterController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateCreateServiceProviderClusterController, false),
	}
}

func instantiateCreateServiceProviderClusterController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clustercreation.NewCreateServiceProviderClusterController(
		controllerContext.ResourcesDBClient,
		clusterLister,
		serviceProviderClusterLister,
		controllerContext.BackendInformers,
	), nil
}

func registerCleanOrphanedClusterManagedResourceGroupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateCleanOrphanedClusterManagedResourceGroupController, false),
	}
}

func instantiateCleanOrphanedClusterManagedResourceGroupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return clusterdeletion.NewCleanOrphanedClusterManagedResourceGroupController(
		controllerContext.AzureLocation,
		activeOperationLister,
		controllerContext.ResourcesDBClient,
		controllerContext.FPAClientBuilder,
		controllerContext.BackendInformers,
	), nil
}

func registerEnsureManagedResourceGroupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateEnsureManagedResourceGroupController, true),
	}
}

func instantiateEnsureManagedResourceGroupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerClusterDeletionClusterServiceDeleteDispatchController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterDeletionClusterServiceDeleteDispatchController, true),
	}
}

func instantiateClusterDeletionClusterServiceDeleteDispatchController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterdeletion.NewClusterClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerClusterClusterServiceIDClearerController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterClusterServiceIDClearerController, false),
	}
}

func instantiateClusterClusterServiceIDClearerController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterdeletion.NewClusterClusterServiceIDClearerController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterCredentialDeletionMarkerController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterCredentialDeletionMarkerController, false),
	}
}

func instantiateClusterCredentialDeletionMarkerController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterdeletion.NewClusterCredentialDeletionMarkerController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterChildResourcesCleanupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterChildResourcesCleanupController, false),
	}
}

func instantiateClusterChildResourcesCleanupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterdeletion.NewClusterChildResourcesCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
	), nil
}

func registerClusterDeletionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterDeletionController, false),
	}
}

func instantiateClusterDeletionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterdeletion.NewClusterDeletionController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.BillingDBClient,
		controllerContext.BackendInformers,
		controllerContext.KubeApplierDBClients,
	), nil
}

func registerClusterClusterServiceUpdateDispatchController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterClusterServiceUpdateDispatchController, false),
	}
}

func instantiateClusterClusterServiceUpdateDispatchController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterupdate.NewClusterClusterServiceUpdateDispatchController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerPlacementSyncController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiatePlacementSyncController, true),
	}
}

func instantiatePlacementSyncController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return clusterplacement.NewManagementClusterPlacementSyncController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		managementClusterLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerPlacementController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiatePlacementController, true),
	}
}

func instantiatePlacementController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerPendingCleanupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     5,
		Instantiate: controllerconfig.WithCacheSyncs(instantiatePendingCleanupController, false),
	}
}

func instantiatePendingCleanupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	return clusterplacement.NewPendingCleanupController(
		controllerContext.FleetDBClient,
		serviceProviderClusterLister,
		clusterLister,
		controllerContext.FleetInformers,
	), nil
}

func registerBackupScheduleController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateBackupScheduleController, true),
	}
}

func instantiateBackupScheduleController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterbackups.NewBackupScheduleController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.MaestroSourceEnvironmentIdentifier,
		controllerContext.BackupConfig,
	), nil
}

func registerFetchMSIIdentitiesInfoController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateFetchMSIIdentitiesInfoController, false),
	}
}

func instantiateFetchMSIIdentitiesInfoController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusteridentity.NewFetchMSIIdentitiesInfoController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.FPAMIDataplaneClientBuilder,
	), nil
}

func registerFetchDataPlaneOperatorsManagedIdentitiesInfoController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateFetchDataPlaneOperatorsManagedIdentitiesInfoController, false),
	}
}

func instantiateFetchDataPlaneOperatorsManagedIdentitiesInfoController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusteridentity.NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.SMIClientBuilder,
	), nil
}

func registerIdentityRoleAssignmentsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateIdentityRoleAssignmentsController, true),
	}
}

func instantiateIdentityRoleAssignmentsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerKeyRotationBackupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateKeyRotationBackupController, true),
	}
}

func instantiateKeyRotationBackupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return clusterbackups.NewKeyRotationBackupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.BackupConfig,
	), nil
}

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(legacycredentialrequest.DispatchRequestCredentialControllerName)] = registerDispatchRequestCredentialController()
	registry[strings.ToLower(credentialrequestoperations.SystemAdminCredentialDispatchRequestCredentialControllerName)] = registerAdminCredentialsDispatchRequestCredentialController()
	registry[strings.ToLower(credentialrevocationoperations.SystemAdminCredentialDispatchRevokeCredentialsControllerName)] = registerAdminCredentialsDispatchRevokeCredentialsController()
	registry[strings.ToLower(credentialrequestoperations.SystemAdminCredentialOperationRequestCredentialPollControllerName)] = registerAdminCredentialsOperationRequestCredentialPollController()
	registry[strings.ToLower(credentialrevocationoperations.SystemAdminCredentialOperationRevokeCredentialsPollControllerName)] = registerAdminCredentialsOperationRevokeCredentialsPollController()
	registry[strings.ToLower(credentialrequestcreation.SystemAdminCredentialIssuanceObserverControllerName)] = registerAdminCredentialsIssuanceObserverController()
	registry[strings.ToLower(credentialrequestcreation.DesiresCreatorControllerName)] = registerAdminCredentialsDesiresCreatorController()
	registry[strings.ToLower(credentialrequestdeletion.SystemAdminCredentialPostIssuanceCleanupControllerName)] = registerAdminCredentialsPostIssuanceCleanupController()
	registry[strings.ToLower(credentialrequestdeletion.SystemAdminCredentialRevokedGCControllerName)] = registerAdminCredentialsRevokedGCController()
	registry[strings.ToLower(credentialrequestdeletion.SystemAdminCredentialClusterDeletionCleanupControllerName)] = registerAdminCredentialsClusterDeletionCleanupController()
	registry[strings.ToLower(credentialrevocationcreation.SystemAdminCredentialRevocationMarkRequestsControllerName)] = registerSystemAdminCredentialRevocationMarkRequestsController()
	registry[strings.ToLower(credentialrevocationcreation.RevocationDesiresControllerName)] = registerSystemAdminCredentialRevocationDesiresController()
	registry[strings.ToLower(credentialrevocationdeletion.SystemAdminCredentialRevocationCompletionControllerName)] = registerSystemAdminCredentialRevocationCompletionController()
	registry[strings.ToLower(credentialrevocationdeletion.SystemAdminCredentialRevocationDeletionControllerName)] = registerSystemAdminCredentialRevocationDeletionController()
	registry[strings.ToLower(denyassignments.ClusterDenyAssignmentControllerName)] = registerClusterDenyAssignmentController()
	registry[strings.ToLower(clustercreation.ClusterPendingClusterServiceIDAssignControllerName)] = registerClusterPendingClusterServiceIDAssignController()
	registry[strings.ToLower(clustercreation.ClusterClusterServiceCreateControllerName)] = registerClusterClusterServiceCreateController()
	registry[strings.ToLower(clusteroperations.OperationClusterCreateControllerName)] = registerOperationClusterCreateController()
	registry[strings.ToLower(clusteroperations.OperationClusterUpdateControllerName)] = registerOperationClusterUpdateController()
	registry[strings.ToLower(clusteroperations.OperationClusterDeleteControllerName)] = registerOperationClusterDeleteController()
	registry[strings.ToLower(legacycredentialrequest.OperationRequestCredentialControllerName)] = registerOperationRequestCredentialController()
	registry[strings.ToLower(clustervalidation.ClusterValidationAlwaysSuccessValidationControllerName)] = registerAlwaysSuccessClusterValidationController()
	registry[strings.ToLower(clusterversion.ControlPlaneActiveVersionsControllerName)] = registerControlPlaneActiveVersionsController()
	registry[strings.ToLower(clusterversion.ControlPlaneDesiredVersionControllerName)] = registerControlPlaneDesiredVersionController()
	registry[strings.ToLower(clusterversion.TriggerControlPlaneUpgradeControllerName)] = registerTriggerControlPlaneUpgradeController()
	registry[strings.ToLower(clusterproperties.ClusterBaseDomainPrefixSyncControllerName)] = registerClusterBaseDomainPrefixSyncController()
	registry[strings.ToLower(clusterproperties.ClusterPropertiesSyncControllerName)] = registerClusterPropertiesSyncController()
	registry[strings.ToLower(clusteridentity.ClusterIdentitySyncControllerName)] = registerClusterIdentitySyncController()
	registry[strings.ToLower(clusterstatus.ClusterDegradedAggregatorControllerName)] = registerClusterDegradedAggregatorController()
	registry[strings.ToLower(clusterstatus.ClusterRequirementsValidAggregatorControllerName)] = registerClusterRequirementsValidAggregatorController()
	registry[strings.ToLower(clusterproperties.DesiredControlPlaneSizeControllerName)] = registerDesiredControlPlaneSizeController()
	registry[strings.ToLower(clusterproperties.ServiceProviderClusterPropertiesSyncControllerName)] = registerServiceProviderClusterPropertiesSyncController()
	registry[strings.ToLower(clustervalidation.ClusterValidationAzureResourceProvidersRegistrationValidationControllerName)] = registerAzureRPRegistrationValidationController()
	registry[strings.ToLower(clustervalidation.ClusterValidationAzureClusterResourceGroupExistenceValidationControllerName)] = registerAzureClusterResourceGroupExistenceValidationController()
	registry[strings.ToLower(clustervalidation.ClusterValidationAzureClusterManagedIdentitiesExistenceValidationControllerName)] = registerAzureClusterManagedIdentitiesExistenceValidationController()
	registry[strings.ToLower(clustervalidation.ClusterValidationControlPlaneIdentitiesPermissionsClusterValidationControllerName)] = registerControlPlaneIdentitiesPermissionsValidationController()
	registry[strings.ToLower(clustervalidation.ClusterValidationDataPlaneIdentitiesPermissionsValidationControllerName)] = registerDataPlaneIdentitiesPermissionsValidationController()
	registry[strings.ToLower(clustervalidation.ClusterValidationContainerRegistryPullCredentialsPermissionValidationControllerName)] = registerContainerRegistryPullCredentialsValidationController()
	registry[strings.ToLower(clusterreaddesires.CreateClusterScopedReadDesiresControllerName)] = registerCreateClusterScopedReadDesiresController()
	registry[strings.ToLower(clustercreation.CreateServiceProviderClusterControllerName)] = registerCreateServiceProviderClusterController()
	registry[strings.ToLower(clusterdeletion.CleanOrphanedClusterManagedResourceGroupControllerName)] = registerCleanOrphanedClusterManagedResourceGroupController()
	registry[strings.ToLower(clusterazureresources.ManagedResourceGroupControllerName)] = registerEnsureManagedResourceGroupController()
	registry[strings.ToLower(clusterdeletion.ClusterClusterServiceDeleteDispatchControllerName)] = registerClusterDeletionClusterServiceDeleteDispatchController()
	registry[strings.ToLower(clusterdeletion.ClusterDeletionClusterServiceIDClearerControllerName)] = registerClusterClusterServiceIDClearerController()
	registry[strings.ToLower(clusterdeletion.ClusterCredentialDeletionMarkerControllerControllerName)] = registerClusterCredentialDeletionMarkerController()
	registry[strings.ToLower(clusterdeletion.ClusterChildResourcesCleanupControllerControllerName)] = registerClusterChildResourcesCleanupController()
	registry[strings.ToLower(clusterdeletion.ClusterDeletionControllerControllerName)] = registerClusterDeletionController()
	registry[strings.ToLower(clusterupdate.ClusterClusterServiceUpdateDispatchControllerName)] = registerClusterClusterServiceUpdateDispatchController()
	registry[strings.ToLower(clusterplacement.ManagementClusterPlacementSyncControllerName)] = registerPlacementSyncController()
	registry[strings.ToLower(clusterplacement.PlacementControllerName)] = registerPlacementController()
	registry[strings.ToLower(clusterplacement.PendingCleanupControllerName)] = registerPendingCleanupController()
	registry[strings.ToLower(clusterbackups.BackupScheduleControllerName)] = registerBackupScheduleController()
	registry[strings.ToLower(clusteridentity.FetchMSIIdentitiesInfoControllerName)] = registerFetchMSIIdentitiesInfoController()
	registry[strings.ToLower(clusteridentity.FetchDataPlaneOperatorsManagedIdentitiesInfoControllerName)] = registerFetchDataPlaneOperatorsManagedIdentitiesInfoController()
	registry[strings.ToLower(clusterroleassignments.RoleAssignmentsControllerName)] = registerIdentityRoleAssignmentsController()
	registry[strings.ToLower(clusterbackups.KeyRotationBackupControllerName)] = registerKeyRotationBackupController()
}
