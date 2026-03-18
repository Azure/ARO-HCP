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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer is a controller that
// fetches the Client ID and Principal ID of the data plane operators managed identities
// associated to the cluster and stores them in Cosmos.
type fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	cosmosClient database.DBClient

	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

var _ controllerutils.ClusterSyncer = (*fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer)(nil)

func NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
	cosmosClient database.DBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) controllerutils.Controller {

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		cooldownChecker:  controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:     cosmosClient,
		smiClientBuilder: smiClientBuilder,
	}

	controller := controllerutils.NewClusterWatchingController(
		"FetchDataPlaneOperatorsManagedIdentitiesInfo",
		cosmosClient,
		backendInformers,
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
		currentMI, ok := existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities[dataPlaneOperatorResourceID.String()]
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

	smiResourceID := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	uaisClient, err := c.smiClientBuilder.UserAssignedIdentitiesClient(ctx, existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID, existingCluster.ID.SubscriptionID)
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

	if !equality.Semantic.DeepEqual(existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities, desiredDataPlaneOperatorsManagedIdentities) {
		if existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities == nil {
			existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities = make(map[string]*api.ServiceProviderClusterDataPlaneOperatorManagedIdentity)
		}

		for _, desired := range desiredDataPlaneOperatorsManagedIdentities {
			key := desired.ResourceID.String()
			entry := existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities[key]
			if entry == nil {
				entry = &api.ServiceProviderClusterDataPlaneOperatorManagedIdentity{}
				existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities[key] = entry
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

func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
