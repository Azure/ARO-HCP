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
	"time"

	utilsclock "k8s.io/utils/clock"

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
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/metrics"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	nodepoolcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/creation"
	nodepooldeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/deletion"
	nodepooloperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/operations"
	nodepoolreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/readdesires"
	nodepoolstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/status"
	nodepoolupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/update"
	nodepoolvalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/validation"
	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
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
		"orphanedbillingcleanup": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return billing.NewOrphanedBillingCleanupController(controllerContext.Clock, controllerContext.BillingDBClient, controllerContext.ClusterLister, controllerContext.BillingLister), nil
			},
		},
		"createbillingdoc": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return controllerutils.NewClusterWatchingController(
					"CreateBillingDoc", controllerContext.ResourcesDBClient, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, 60*time.Second,
					billing.NewCreateBillingDocController(controllerContext.Clock, controllerContext.AzureLocation, controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, controllerContext.ClusterLister, controllerContext.BillingLister)), nil
			},
		},

		// cluster
		"dispatchrequestcredential": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return legacycredentialrequest.NewDispatchRequestCredentialController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"systemadmincredentialdispatchrequestcredential": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrequestoperations.NewDispatchRequestCredentialController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"systemadmincredentialdispatchrevokecredentials": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrevocationoperations.NewDispatchRevokeCredentialsController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"systemadmincredentialoperationrequestcredentialpoll": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrequestoperations.NewOperationRequestCredentialPollController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"systemadmincredentialoperationrevokecredentialspoll": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrevocationoperations.NewOperationRevokeCredentialsPollController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"systemadmincredentialissuanceobserver": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrequestcreation.NewIssuanceObserverController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"systemadmincredentialdesirescreator": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrequestcreation.NewDesiresCreatorController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"systemadmincredentialpostissuancecleanup": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrequestdeletion.NewPostIssuanceCleanupController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"systemadmincredentialrevokedgc": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrequestdeletion.NewRevokedGCController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"systemadmincredentialclusterdeletioncleanup": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrequestdeletion.NewClusterDeletionCleanupController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"systemadmincredentialrevocationmarkrequests": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrevocationcreation.NewRevocationMarkRequestsController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"systemadmincredentialrevocationdesires": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrevocationcreation.NewRevocationDesiresController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionApplyDesireLister,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"systemadmincredentialrevocationcompletion": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrevocationdeletion.NewRevocationCompletionController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"systemadmincredentialrevocationdeletion": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return credentialrevocationdeletion.NewRevocationDeletionController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"clusterdenyassignment": {
			Workers: 20,
			Enabled: func(controllerContext ControllerContext) bool { return controllerContext.HasRealFPA },
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return denyassignments.NewClusterDenyAssignmentController(
					utilsclock.RealClock{},
					controllerContext.ResourcesDBClient,
					controllerContext.FPAClientBuilder,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clusterpendingclusterserviceidassign": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustercreation.NewClusterPendingClusterServiceIDAssignController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clusterclusterservicecreate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustercreation.NewClusterClusterServiceCreateController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.ManagementClusterLister,
					controllerContext.BackendInformers,
					controllerContext.HasRealFPA,
				), nil
			},
		},
		"operationclustercreate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusteroperations.NewOperationClusterCreateController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
					controllerContext.BackendInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"operationclusterupdate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusteroperations.NewOperationClusterUpdateController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.UnionReadDesireLister,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"operationclusterdelete": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusteroperations.NewOperationClusterDeleteController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BillingDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.UnionReadDesireLister,
					controllerContext.ClustersServiceClient,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"operationrequestcredential": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return legacycredentialrequest.NewOperationRequestCredentialController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"clustervalidationalwayssuccessvalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustervalidation.NewClusterValidationController(
					validationutils.NewAlwaysSuccessValidation(),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"controlplaneactiveversions": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterversion.NewControlPlaneActiveVersionController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"controlplanedesiredversion": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterversion.NewControlPlaneDesiredVersionController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ClustersServiceClient,
					controllerContext.ActiveOperationLister,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.NodePoolLister,
					controllerContext.ServiceProviderNodePoolLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"triggercontrolplaneupgrade": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterversion.NewTriggerControlPlaneUpgradeController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ClustersServiceClient,
					controllerContext.ActiveOperationLister,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"clusterbasedomainprefixsync": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterproperties.NewClusterBaseDomainPrefixSyncController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"clusterpropertiessync": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterproperties.NewClusterPropertiesSyncController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"clusteridentitysync": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusteridentity.NewClusterIdentitySyncController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"clusterdegradedaggregator": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterstatus.NewClusterDegradedAggregatorController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ControllerLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.Clock,
				), nil
			},
		},
		"clusterrequirementsvalidaggregator": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterstatus.NewClusterRequirementsValidAggregatorController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"desiredcontrolplanesize": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterproperties.NewDesiredControlPlaneSizeController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"serviceproviderclusterpropertiessync": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterproperties.NewServiceProviderClusterPropertiesSyncController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"clustervalidationazureresourceprovidersregistrationvalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustervalidation.NewClusterValidationController(
					validationutils.NewAzureResourceProvidersRegistrationValidation(controllerContext.FPAClientBuilder),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clustervalidationazureclusterresourcegroupexistencevalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustervalidation.NewClusterValidationController(
					validationutils.NewAzureClusterResourceGroupExistenceValidation(controllerContext.FPAClientBuilder),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clustervalidationazureclustermanagedidentitiesexistencevalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustervalidation.NewClusterValidationController(
					validationutils.NewAzureClusterManagedIdentitiesExistenceValidation(controllerContext.SMIClientBuilder),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clustervalidationcontrolplaneidentitiespermissionsclustervalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
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
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clustervalidationdataplaneidentitiespermissionsvalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustervalidation.NewClusterValidationController(
					validationutils.NewDataPlaneIdentitiesPermissionsValidation(
						controllerContext.SMIClientBuilder,
						controllerContext.ClusterScopedIdentitiesConfig,
						controllerContext.BackendIdentityAzureCachedReaders,
						controllerContext.CheckAccessV2ClientBuilder,
					),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clustervalidationcontainerregistrypullcredentialspermissionvalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustervalidation.NewClusterValidationController(
					validationutils.NewContainerRegistryPullCredentialsPermissionValidation(controllerContext.SMIClientBuilder, controllerContext.CheckAccessV2ClientBuilder),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"createclusterscopedreaddesires": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterreaddesires.NewCreateClusterScopedReadDesiresController(
					controllerContext.ActiveOperationLister, controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients,
					controllerContext.ServiceProviderClusterLister, controllerContext.UnionReadDesireLister,
					controllerContext.BackendInformers, controllerContext.MaestroSourceEnvironmentIdentifier,
				), nil
			},
		},
		"createserviceprovidercluster": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clustercreation.NewCreateServiceProviderClusterController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClusterLister,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"cleanorphanedclustermanagedresourcegroup": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterdeletion.NewCleanOrphanedClusterManagedResourceGroupController(
					controllerContext.AzureLocation,
					controllerContext.ActiveOperationLister,
					controllerContext.ResourcesDBClient,
					controllerContext.FPAClientBuilder,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"ensuremanagedresourcegroup": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterazureresources.NewManagedResourceGroupController(
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.SubscriptionLister,
					controllerContext.FPAClientBuilder,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"clusterclusterservicedeletedispatch": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterdeletion.NewClusterClusterServiceDeleteDispatchController(
					utilsclock.RealClock{},
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"clusterdeletionclusterserviceidclearer": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterdeletion.NewClusterClusterServiceIDClearerController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clustercredentialdeletionmarkercontroller": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterdeletion.NewClusterCredentialDeletionMarkerController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clusterchildresourcescleanupcontroller": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterdeletion.NewClusterChildResourcesCleanupController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"clusterdeletioncontroller": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterdeletion.NewClusterDeletionController(
					utilsclock.RealClock{},
					controllerContext.ResourcesDBClient,
					controllerContext.BillingDBClient,
					controllerContext.BackendInformers,
					controllerContext.KubeApplierDBClients,
				), nil
			},
		},
		"clusterclusterserviceupdatedispatch": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterupdate.NewClusterClusterServiceUpdateDispatchController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"managementclusterplacementsync": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterplacement.NewManagementClusterPlacementSyncController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.ManagementClusterLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"placement": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterplacement.NewPlacementController(
					controllerContext.ResourcesDBClient,
					controllerContext.FleetDBClient,
					controllerContext.ManagementClusterLister,
					controllerContext.ManagementClusterSchedulingLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"pendingcleanup": {
			Workers: 5,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterplacement.NewPendingCleanupController(
					controllerContext.FleetDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.ClusterLister,
					controllerContext.FleetInformers,
				), nil
			},
		},
		"backupschedule": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterbackups.NewBackupScheduleController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.MaestroSourceEnvironmentIdentifier,
					controllerContext.BackupConfig,
				), nil
			},
		},
		"fetchmsiidentitiesinfo": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusteridentity.NewFetchMSIIdentitiesInfoController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.FPAMIDataplaneClientBuilder,
				), nil
			},
		},
		"fetchdataplaneoperatorsmanagedidentitiesinfo": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusteridentity.NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.SMIClientBuilder,
				), nil
			},
		},
		"identityroleassignments": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterroleassignments.NewRoleAssignmentsController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderClusterLister,
					controllerContext.SubscriptionLister,
					controllerContext.FPAClientBuilder,
					controllerContext.ClusterScopedIdentitiesConfig,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"keyrotationbackup": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterbackups.NewKeyRotationBackupController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.BackupConfig,
				), nil
			},
		},

		// clusterresources
		"clusterresources": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return clusterresources.NewClusterResourcesController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.ClustersServiceClient,
				), nil
			},
		},

		// cosmosmigration
		"cosmosmigration": {
			Workers: 5,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return cosmosmigration.NewCosmosMigrationController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					5*time.Minute,
				), nil
			},
		},

		// datadump
		"subscriptionnonclusterdatadump": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return datadump.NewSubscriptionNonClusterDataDumpController(controllerContext.ResourcesDBClient, controllerContext.BackendInformers), nil
			},
		},
		"datadump": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return datadump.NewClusterRecursiveDataDumpController(controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients, controllerContext.ManagementClusterLister, controllerContext.ActiveOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers), nil
			},
		},
		"csstatedump": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return datadump.NewCSStateDumpController(controllerContext.ResourcesDBClient, controllerContext.ActiveOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, controllerContext.ClustersServiceClient), nil
			},
		},
		"billingdump": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return datadump.NewBillingDumpController(controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, controllerContext.ActiveOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers), nil
			},
		},
		"managementclusterdatadump": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return datadump.NewManagementClusterDataDumpController(controllerContext.FleetDBClient, controllerContext.ManagementClusterLister, controllerContext.FleetInformers), nil
			},
		},

		// externalauth
		"externalauthclusterservicecreate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthcreation.NewExternalAuthClusterServiceCreateController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"operationexternalauthcreate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthoperations.NewOperationExternalAuthCreateController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"operationexternalauthupdate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthoperations.NewOperationExternalAuthUpdateController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.UnionReadDesireLister,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"operationexternalauthdelete": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthoperations.NewOperationExternalAuthDeleteController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"externalauthdegradedaggregator": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthstatus.NewExternalAuthDegradedAggregatorController(
					controllerContext.ResourcesDBClient,
					controllerContext.ExternalAuthLister,
					controllerContext.ControllerLister,
					controllerContext.BackendInformers,
					controllerContext.Clock,
				), nil
			},
		},
		"externalauthclusterservicedeletedispatch": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthdeletion.NewExternalAuthClusterServiceDeleteDispatchController(
					utilsclock.RealClock{},
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"externalauthdeletionclusterserviceidclearer": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthdeletion.NewExternalAuthClusterServiceIDClearerController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"externalauthchildresourcescleanupcontroller": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthdeletion.NewExternalAuthChildResourcesCleanupController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"externalauthdeletioncontroller": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthdeletion.NewExternalAuthDeletionController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"externalauthclusterserviceupdatedispatch": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return externalauthupdate.NewExternalAuthClusterServiceUpdateDispatchController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.ActiveOperationLister,
					controllerContext.BackendInformers,
				), nil
			},
		},

		// metrics
		"operationphasemetrics": {
			Workers: 1,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return metrics.NewController(
					"OperationPhaseMetrics", controllerContext.BackendInformers.AllOperations(), metrics.NewOperationPhaseMetricsHandler(controllerContext.MetricsRegisterer)), nil
			},
		},
		"clustermetrics": {
			Workers: 1,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return metrics.NewController(
					"ClusterMetrics", controllerContext.ClusterInformer, metrics.NewClusterMetricsHandler(controllerContext.MetricsRegisterer)), nil
			},
		},
		"clusterversionmetrics": {
			Workers: 1,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return metrics.NewController(
					"ClusterVersionMetrics", controllerContext.ServiceProviderClusterInformer, metrics.NewClusterVersionMetricsHandler(controllerContext.MetricsRegisterer, controllerContext.UnionReadDesireLister)), nil
			},
		},
		"nodepoolmetrics": {
			Workers: 1,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return metrics.NewController(
					"NodePoolMetrics", controllerContext.NodePoolInformer, metrics.NewNodePoolMetricsHandler(controllerContext.MetricsRegisterer)), nil
			},
		},
		"externalauthmetrics": {
			Workers: 1,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return metrics.NewController(
					"ExternalAuthMetrics", controllerContext.ExternalAuthInformer, metrics.NewExternalAuthMetricsHandler(controllerContext.MetricsRegisterer)), nil
			},
		},
		"clusterinfometrics": {
			Workers: 1,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return metrics.NewController(
					"ClusterInfoMetrics", controllerContext.ServiceProviderClusterInformer, metrics.NewClusterInfoMetricsHandler(controllerContext.MetricsRegisterer)), nil
			},
		},

		// mismatch
		"clusterservicematchingclusters": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return mismatch.NewClusterServiceClusterMatchingController(controllerContext.Clock, controllerContext.ResourcesDBClient, controllerContext.SubscriptionLister, controllerContext.ClustersServiceClient), nil
			},
		},
		"deleteorphanedcosmosresources": {
			Workers: 10,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return mismatch.NewDeleteOrphanedCosmosResourcesController(controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients, controllerContext.SubscriptionLister, controllerContext.ManagementClusterLister), nil
			},
		},
		"missingresourceid": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return mismatch.NewMissingResourceIDController(controllerContext.ResourcesDBClient), nil
			},
		},
		"backfillclusteruid": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return controllerutils.NewClusterWatchingController(
					"BackfillClusterUID", controllerContext.ResourcesDBClient, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, 60*time.Minute,
					mismatch.NewBackfillClusterUIDController(controllerContext.Clock, controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, controllerContext.ClusterLister)), nil
			},
		},

		// nodepool
		"nodepoolclusterservicecreate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolcreation.NewNodePoolClusterServiceCreateController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"operationnodepoolcreate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepooloperations.NewOperationNodePoolCreateController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.UnionReadDesireLister,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"operationnodepoolupdate": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepooloperations.NewOperationNodePoolUpdateController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.UnionReadDesireLister,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"operationnodepooldelete": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepooloperations.NewOperationNodePoolDeleteController(
					controllerContext.Clock,
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.HTTPClient,
					controllerContext.ActiveOperationInformer,
				), nil
			},
		},
		"nodepooldegradedaggregator": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolstatus.NewNodePoolDegradedAggregatorController(
					controllerContext.ResourcesDBClient,
					controllerContext.NodePoolLister,
					controllerContext.ControllerLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.Clock,
				), nil
			},
		},
		"nodepoolrequirementsvalidaggregator": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolstatus.NewNodePoolRequirementsValidAggregatorController(
					controllerContext.ResourcesDBClient,
					controllerContext.NodePoolLister,
					controllerContext.ServiceProviderNodePoolLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"nodepoolvalidationazurevmsizesupportsephemeralosdiskvalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolvalidation.NewNodePoolValidationController(
					validationutils.NewAzureVMSizeSupportsEphemeralOSDiskValidation(controllerContext.VirtualMachineResourceSKUsCachedReaderController),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderNodePoolLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"nodepoolvalidationazurenodepoolvmquotavalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolvalidation.NewNodePoolValidationController(
					validationutils.NewAzureNodePoolVMQuotaValidation(controllerContext.VirtualMachineResourceSKUsCachedReaderController, controllerContext.FPAClientBuilder),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderNodePoolLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"nodepoolvalidationazurenodepoolnsgbasedrequiredconnectivityvalidation": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolvalidation.NewNodePoolValidationController(
					validationutils.NewAzureNodePoolNSGBasedRequiredConnectivityValidation(controllerContext.SMIClientBuilder),
					controllerContext.ResourcesDBClient,
					controllerContext.ServiceProviderNodePoolLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"nodepoolversion": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolversion.NewNodePoolVersionController(
					controllerContext.ResourcesDBClient,
					controllerContext.SubscriptionLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"nodepoolactiveversions": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolversion.NewNodePoolActiveVersionController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.UnionReadDesireLister,
				), nil
			},
		},
		"createnodepoolscopedreaddesires": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolreaddesires.NewCreateNodePoolScopedReadDesiresController(
					controllerContext.ActiveOperationLister, controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients,
					controllerContext.ServiceProviderClusterLister, controllerContext.UnionReadDesireLister,
					controllerContext.BackendInformers, controllerContext.MaestroSourceEnvironmentIdentifier,
				), nil
			},
		},
		"createserviceprovidernodepool": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolcreation.NewCreateServiceProviderNodePoolController(
					controllerContext.ResourcesDBClient,
					controllerContext.NodePoolLister,
					controllerContext.ServiceProviderNodePoolLister,
					controllerContext.BackendInformers,
				), nil
			},
		},
		"triggernodepoolupgrade": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolversion.NewTriggerNodePoolUpgradeController(
					controllerContext.ResourcesDBClient,
					controllerContext.NodePoolLister,
					controllerContext.ClustersServiceClient,
					controllerContext.ServiceProviderNodePoolLister,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"nodepoolclusterservicedeletedispatch": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepooldeletion.NewNodePoolClusterServiceDeleteDispatchController(
					utilsclock.RealClock{},
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"nodepooldeletionclusterserviceidclearer": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepooldeletion.NewNodePoolClusterServiceIDClearerController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"nodepoolchildresourcescleanupcontroller": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepooldeletion.NewNodePoolChildResourcesCleanupController(
					controllerContext.ResourcesDBClient,
					controllerContext.KubeApplierDBClients,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
				), nil
			},
		},
		"nodepooldeletioncontroller": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepooldeletion.NewNodePoolDeletionController(
					controllerContext.ResourcesDBClient,
					controllerContext.BackendInformers,
					controllerContext.UnionKubeApplierInformers,
					controllerContext.KubeApplierDBClients,
				), nil
			},
		},
		"nodepoolclusterserviceupdatedispatch": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return nodepoolupdate.NewNodePoolClusterServiceUpdateDispatchController(
					controllerContext.ResourcesDBClient,
					controllerContext.ClustersServiceClient,
					controllerContext.BackendInformers,
				), nil
			},
		},

		// Supporting controller: internal/database/unioninformers/kubeapplier
		"union-kube-applier-informers-controller": {
			Workers: 1,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return controllerContext.UnionKubeApplierInformersController, nil
			},
		},

		// Supporting controller: backend/pkg/azure/cachedreader
		"fpavirtualmachineresourceskuscachedreader": {
			Workers: 20,
			instantiate: func(controllerContext ControllerContext) (Runnable, error) {
				return controllerContext.VirtualMachineResourceSKUsCachedReaderController, nil
			},
		},
	}
}

func controllerConstructionOrder() []string {
	return []string{
		"operationphasemetrics",
		"union-kube-applier-informers-controller",
		"clustermetrics",
		"clusterversionmetrics",
		"clusterinfometrics",
		"nodepoolmetrics",
		"externalauthmetrics",
		"subscriptionnonclusterdatadump",
		"datadump",
		"csstatedump",
		"billingdump",
		"managementclusterdatadump",
		"dispatchrequestcredential",
		"systemadmincredentialdispatchrequestcredential",
		"systemadmincredentialdispatchrevokecredentials",
		"systemadmincredentialoperationrequestcredentialpoll",
		"systemadmincredentialoperationrevokecredentialspoll",
		"systemadmincredentialissuanceobserver",
		"systemadmincredentialdesirescreator",
		"systemadmincredentialpostissuancecleanup",
		"systemadmincredentialrevokedgc",
		"systemadmincredentialclusterdeletioncleanup",
		"systemadmincredentialrevocationmarkrequests",
		"systemadmincredentialrevocationdesires",
		"systemadmincredentialrevocationcompletion",
		"systemadmincredentialrevocationdeletion",
		"operationclustercreate",
		"operationclusterupdate",
		"operationclusterdelete",
		"operationnodepoolcreate",
		"operationnodepoolupdate",
		"operationnodepooldelete",
		"operationexternalauthcreate",
		"operationexternalauthupdate",
		"operationexternalauthdelete",
		"operationrequestcredential",
		"clusterservicematchingclusters",
		"clustervalidationalwayssuccessvalidation",
		"deleteorphanedcosmosresources",
		"missingresourceid",
		"backfillclusteruid",
		"orphanedbillingcleanup",
		"createbillingdoc",
		"controlplaneactiveversions",
		"controlplanedesiredversion",
		"triggercontrolplaneupgrade",
		"clusterbasedomainprefixsync",
		"clusterpropertiessync",
		"clusteridentitysync",
		"desiredcontrolplanesize",
		"serviceproviderclusterpropertiessync",
		"backupschedule",
		"keyrotationbackup",
		"clusterdegradedaggregator",
		"clusterrequirementsvalidaggregator",
		"nodepooldegradedaggregator",
		"nodepoolrequirementsvalidaggregator",
		"externalauthdegradedaggregator",
		"createclusterscopedreaddesires",
		"createnodepoolscopedreaddesires",
		"cosmosmigration",
		"createserviceprovidercluster",
		"createserviceprovidernodepool",
		"cleanorphanedclustermanagedresourcegroup",
		"ensuremanagedresourcegroup",
		"fpavirtualmachineresourceskuscachedreader",
		"clustervalidationazureresourceprovidersregistrationvalidation",
		"clustervalidationazureclusterresourcegroupexistencevalidation",
		"clustervalidationazureclustermanagedidentitiesexistencevalidation",
		"clustervalidationcontainerregistrypullcredentialspermissionvalidation",
		"nodepoolvalidationazurevmsizesupportsephemeralosdiskvalidation",
		"nodepoolvalidationazurenodepoolvmquotavalidation",
		"clustervalidationcontrolplaneidentitiespermissionsclustervalidation",
		"clustervalidationdataplaneidentitiespermissionsvalidation",
		"nodepoolvalidationazurenodepoolnsgbasedrequiredconnectivityvalidation",
		"nodepoolversion",
		"nodepoolactiveversions",
		"triggernodepoolupgrade",
		"managementclusterplacementsync",
		"placement",
		"pendingcleanup",
		"nodepoolclusterservicecreate",
		"externalauthclusterservicecreate",
		"nodepoolclusterservicedeletedispatch",
		"nodepooldeletionclusterserviceidclearer",
		"nodepoolchildresourcescleanupcontroller",
		"nodepooldeletioncontroller",
		"externalauthclusterservicedeletedispatch",
		"externalauthdeletionclusterserviceidclearer",
		"externalauthchildresourcescleanupcontroller",
		"externalauthdeletioncontroller",
		"clusterdenyassignment",
		"clusterpendingclusterserviceidassign",
		"clusterclusterservicecreate",
		"clusterclusterservicedeletedispatch",
		"clusterdeletionclusterserviceidclearer",
		"clustercredentialdeletionmarkercontroller",
		"clusterchildresourcescleanupcontroller",
		"clusterdeletioncontroller",
		"clusterclusterserviceupdatedispatch",
		"nodepoolclusterserviceupdatedispatch",
		"externalauthclusterserviceupdatedispatch",
		"fetchmsiidentitiesinfo",
		"fetchdataplaneoperatorsmanagedidentitiesinfo",
		"identityroleassignments",
		"clusterresources",
	}
}

func controllerLaunchOrder() []string {
	return []string{
		"union-kube-applier-informers-controller",
		"subscriptionnonclusterdatadump",
		"datadump",
		"csstatedump",
		"billingdump",
		"managementclusterdatadump",
		"dispatchrequestcredential",
		"systemadmincredentialdispatchrequestcredential",
		"systemadmincredentialdispatchrevokecredentials",
		"systemadmincredentialoperationrequestcredentialpoll",
		"systemadmincredentialoperationrevokecredentialspoll",
		"systemadmincredentialissuanceobserver",
		"systemadmincredentialdesirescreator",
		"systemadmincredentialpostissuancecleanup",
		"systemadmincredentialrevokedgc",
		"systemadmincredentialclusterdeletioncleanup",
		"systemadmincredentialrevocationmarkrequests",
		"systemadmincredentialrevocationdesires",
		"systemadmincredentialrevocationcompletion",
		"systemadmincredentialrevocationdeletion",
		"clusterdenyassignment",
		"clusterpendingclusterserviceidassign",
		"clusterclusterservicecreate",
		"nodepoolclusterservicecreate",
		"externalauthclusterservicecreate",
		"operationclustercreate",
		"operationclusterupdate",
		"operationclusterdelete",
		"operationnodepoolcreate",
		"operationnodepoolupdate",
		"operationnodepooldelete",
		"operationexternalauthcreate",
		"operationexternalauthupdate",
		"operationexternalauthdelete",
		"operationrequestcredential",
		"clusterservicematchingclusters",
		"clustervalidationalwayssuccessvalidation",
		"deleteorphanedcosmosresources",
		"missingresourceid",
		"backfillclusteruid",
		"orphanedbillingcleanup",
		"createbillingdoc",
		"controlplaneactiveversions",
		"controlplanedesiredversion",
		"triggercontrolplaneupgrade",
		"clusterbasedomainprefixsync",
		"clusterpropertiessync",
		"clusteridentitysync",
		"clusterdegradedaggregator",
		"clusterrequirementsvalidaggregator",
		"nodepooldegradedaggregator",
		"nodepoolrequirementsvalidaggregator",
		"externalauthdegradedaggregator",
		"desiredcontrolplanesize",
		"serviceproviderclusterpropertiessync",
		"clustervalidationazureresourceprovidersregistrationvalidation",
		"clustervalidationazureclusterresourcegroupexistencevalidation",
		"clustervalidationazureclustermanagedidentitiesexistencevalidation",
		"nodepoolvalidationazurevmsizesupportsephemeralosdiskvalidation",
		"nodepoolvalidationazurenodepoolvmquotavalidation",
		"clustervalidationcontrolplaneidentitiespermissionsclustervalidation",
		"nodepoolvalidationazurenodepoolnsgbasedrequiredconnectivityvalidation",
		"clustervalidationdataplaneidentitiespermissionsvalidation",
		"clustervalidationcontainerregistrypullcredentialspermissionvalidation",
		"nodepoolversion",
		"nodepoolactiveversions",
		"createclusterscopedreaddesires",
		"createnodepoolscopedreaddesires",
		"createserviceprovidercluster",
		"createserviceprovidernodepool",
		"cleanorphanedclustermanagedresourcegroup",
		"ensuremanagedresourcegroup",
		"triggernodepoolupgrade",
		"nodepoolclusterservicedeletedispatch",
		"nodepooldeletionclusterserviceidclearer",
		"nodepoolchildresourcescleanupcontroller",
		"nodepooldeletioncontroller",
		"externalauthclusterservicedeletedispatch",
		"externalauthdeletionclusterserviceidclearer",
		"externalauthchildresourcescleanupcontroller",
		"externalauthdeletioncontroller",
		"clusterclusterservicedeletedispatch",
		"clusterdeletionclusterserviceidclearer",
		"clustercredentialdeletionmarkercontroller",
		"clusterchildresourcescleanupcontroller",
		"clusterdeletioncontroller",
		"clusterclusterserviceupdatedispatch",
		"nodepoolclusterserviceupdatedispatch",
		"externalauthclusterserviceupdatedispatch",
		"operationphasemetrics",
		"clustermetrics",
		"clusterversionmetrics",
		"nodepoolmetrics",
		"externalauthmetrics",
		"clusterinfometrics",
		"managementclusterplacementsync",
		"placement",
		"pendingcleanup",
		"cosmosmigration",
		"fpavirtualmachineresourceskuscachedreader",
		"backupschedule",
		"fetchmsiidentitiesinfo",
		"fetchdataplaneoperatorsmanagedidentitiesinfo",
		"identityroleassignments",
		"keyrotationbackup",
		"clusterresources",
	}
}
