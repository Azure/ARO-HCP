// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

// readAndPersistMaestroReadonlyBundlesContentSyncer is a controller that reads the Maestro readonly bundles
// references stored in the ServiceProviderCluster resource, retrieves the Maestro readonly bundles using those
// references, extracts the content of the Maestro readonly bundles and persists it in Cosmos.
// It is not responsible for creating the Maestro readonly bundles themselves. That is the responsibility of
// the createMaestroReadonlyBundlesSyncer controller.
// Right now we only support reading the content of the Maestro readonly bundle for HostedCluster associated to the cluster.
// In the future we might want to support reading the content of the Maestro readonly bundle for other resources.
type fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	smiClientBuilderFactory azureclient.ServiceManagedIdentityClientBuilderFactory
}

var _ controllerutils.ClusterSyncer = (*readAndPersistMaestroReadonlyBundlesContentSyncer)(nil)

func NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	clusterInformer cache.SharedIndexInformer,
	smiClientBuilderFactory azureclient.ServiceManagedIdentityClientBuilderFactory,
) controllerutils.Controller {

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		cooldownChecker:         controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:            cosmosClient,
		clusterServiceClient:    clusterServiceClient,
		activeOperationLister:   activeOperationLister,
		smiClientBuilderFactory: smiClientBuilderFactory,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ReadAndPersistMaestroReadonlyBundlesContent",
		cosmosClient,
		clusterInformer,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// TODO unclear if we should put the data plane operators managed identities info in the ServiceProviderCluster resource or in
	// the HCPCluster resource. For now we put it in the ServiceProviderCluster resource.
	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	type identityToSync struct {
		ResourceID   *azcorearm.ResourceID
		OperatorName string
	}

	identitiesToSync := []*identityToSync{}
	for operatorName, dataPlaneOperatorResourceID := range existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		currentMI, ok := existingServiceProviderCluster.DataPlaneOperatorsManagedIdentities[dataPlaneOperatorResourceID.String()]
		if !ok {
			identitiesToSync = append(identitiesToSync, &identityToSync{
				ResourceID:   dataPlaneOperatorResourceID,
				OperatorName: operatorName,
			})
			continue
		}

		if len(currentMI.ClientID) == 0 || len(currentMI.PrincipalID) == 0 || len(currentMI.OperatorName) == 0 {
			identitiesToSync = append(identitiesToSync, &identityToSync{
				ResourceID:   dataPlaneOperatorResourceID,
				OperatorName: operatorName,
			})
			continue
		}

	}
	if len(identitiesToSync) == 0 {
		return nil
	}

	// TODO for now we get the Managed Identities Data Plane Identity URL from the Cluster Service Cluster. In the future
	// we are going to calculate it from the RP and store it in Cosmos.
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Service Cluster: %w", err))
	}

	smiResourceID := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	smiClientBuilder := c.smiClientBuilderFactory.NewServiceManagedIdentityClientBuilder(
		csCluster.Azure().OperatorsAuthentication().ManagedIdentities().ManagedIdentitiesDataPlaneIdentityUrl(),
		smiResourceID,
	)

	uaisClient, err := smiClientBuilder.UserAssignedIdentitiesClient(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get User Assigned Identities Client: %w", err))
	}

	desiredDataPlaneOperatorsManagedIdentities := make(map[string]*api.ServiceProviderClusterDataPlaneOperatorManagedIdentity)

	var syncErrors []error
	for _, dataPlaneOperatorIdentityToSync := range identitiesToSync {
		desiredDataPlaneOperatorsManagedIdentities[dataPlaneOperatorIdentityToSync.ResourceID.String()] = &api.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
			ResourceID:   dataPlaneOperatorIdentityToSync.ResourceID,
			OperatorName: dataPlaneOperatorIdentityToSync.OperatorName,
		}
		currentMI, err := uaisClient.Get(ctx, dataPlaneOperatorIdentityToSync.ResourceID.ResourceGroupName, dataPlaneOperatorIdentityToSync.ResourceID.Name, nil)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to get Data Plane Operator Managed Identity: %w", err)))
			continue
		}

		if currentMI.Properties == nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Properties is nil", dataPlaneOperatorIdentityToSync.ResourceID.String())))
			continue
		}

		if currentMI.Properties.ClientID != nil && len(*currentMI.Properties.ClientID) > 0 {
			desiredDataPlaneOperatorsManagedIdentities[dataPlaneOperatorIdentityToSync.ResourceID.String()].ClientID = *currentMI.Properties.ClientID
		} else {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Client ID is nil or empty", dataPlaneOperatorIdentityToSync.ResourceID.String())))
		}

		if currentMI.Properties.PrincipalID != nil && len(*currentMI.Properties.PrincipalID) > 0 {
			desiredDataPlaneOperatorsManagedIdentities[dataPlaneOperatorIdentityToSync.ResourceID.String()].PrincipalID = *currentMI.Properties.PrincipalID
		} else {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Principal ID is nil or empty", dataPlaneOperatorIdentityToSync.ResourceID.String())))
		}
	}

	if !equality.Semantic.DeepEqual(existingServiceProviderCluster.DataPlaneOperatorsManagedIdentities, desiredDataPlaneOperatorsManagedIdentities) {
		if existingServiceProviderCluster.DataPlaneOperatorsManagedIdentities == nil {
			existingServiceProviderCluster.DataPlaneOperatorsManagedIdentities = make(map[string]*api.ServiceProviderClusterDataPlaneOperatorManagedIdentity)
		}

		for _, desired := range desiredDataPlaneOperatorsManagedIdentities {
			key := desired.ResourceID.String()
			entry := existingServiceProviderCluster.DataPlaneOperatorsManagedIdentities[key]
			if entry == nil {
				entry = &api.ServiceProviderClusterDataPlaneOperatorManagedIdentity{}
				existingServiceProviderCluster.DataPlaneOperatorsManagedIdentities[key] = entry
			}
			entry.ResourceID = desired.ResourceID
			entry.OperatorName = desired.OperatorName
			if len(desired.ClientID) > 0 {
				entry.ClientID = desired.ClientID
			}
			if len(desired.PrincipalID) > 0 {
				entry.PrincipalID = desired.PrincipalID
			}
		}

		_, err := c.cosmosClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, existingServiceProviderCluster, nil)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))
		}
	}

	return errors.Join(syncErrors...)
}
