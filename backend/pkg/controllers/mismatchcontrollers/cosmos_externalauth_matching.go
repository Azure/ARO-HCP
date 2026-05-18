// Copyright 2025 Microsoft Corporation
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

package mismatchcontrollers

import (
	"context"
	"fmt"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cosmosExternalAuthMatching struct {
	cooldownChecker      controllerutil.CooldownChecker
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

// NewCosmosExternalAuthMatchingController periodically looks for mismatched cluster-service and cosmos externalauths
func NewCosmosExternalAuthMatchingController(resourcesDBClient database.ResourcesDBClient, clusterServiceClient ocm.ClusterServiceClientSpec, informers informers.BackendInformers) controllerutils.Controller {
	syncer := &cosmosExternalAuthMatching{
		cooldownChecker:      controllerutil.NewTimeBasedCooldownChecker(1 * time.Hour),
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	// To find cluster-service externalauths that don't have matching cosmos externalauths, you have to be a level above externalauths:
	// clusters, in order to do the "all externalauths from clusterservice".
	controller := controllerutils.NewClusterWatchingController(
		"CosmosMatchingExternalAuths",
		resourcesDBClient,
		informers,
		60*time.Minute,
		syncer,
	)

	return controller
}

func (c *cosmosExternalAuthMatching) getAllCosmosObjs(ctx context.Context, keyObj controllerutils.HCPClusterKey) (map[string]*api.HCPOpenShiftClusterExternalAuth, []*api.HCPOpenShiftClusterExternalAuth, error) {
	clusterServiceIDToExternalAuth := map[string]*api.HCPOpenShiftClusterExternalAuth{}
	ret := []*api.HCPOpenShiftClusterExternalAuth{}

	allExternalAuths, err := c.resourcesDBClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).ExternalAuth(keyObj.HCPClusterName).List(ctx, nil)
	if err != nil {
		return nil, nil, utils.TrackError(err)
	}

	for _, externalAuth := range allExternalAuths.Items(ctx) {
		// we skip cosmos externalauths that don't have a clusterServiceID because if we don't have it there's nothing we
		// can delete. It means that the externalauth hasn't been created in cluster service yet or we haven't persisted
		// the clusterServiceID in cosmos yet.
		if externalAuth.ServiceProviderProperties.ClusterServiceID == nil || len(externalAuth.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
			continue
		}
		ret = append(ret, externalAuth)
		existingCluster, exists := clusterServiceIDToExternalAuth[externalAuth.ServiceProviderProperties.ClusterServiceID.String()]
		if exists {
			return nil, nil, utils.TrackError(fmt.Errorf("duplicate obj found: %s, owned by %q and %q", externalAuth.ID.String(), existingCluster.ID.String(), externalAuth.ID.String()))
		}
		clusterServiceIDToExternalAuth[externalAuth.ServiceProviderProperties.ClusterServiceID.String()] = externalAuth
	}
	if err := allExternalAuths.GetError(); err != nil {
		return nil, nil, utils.TrackError(err)
	}

	return clusterServiceIDToExternalAuth, ret, nil
}

func (c *cosmosExternalAuthMatching) getAllClusterServiceObjs(ctx context.Context, clusterServiceClusterID api.InternalID) (map[string]*arohcpv1alpha1.ExternalAuth, []*arohcpv1alpha1.ExternalAuth, error) {
	clusterServiceIDToExternalAuth := map[string]*arohcpv1alpha1.ExternalAuth{}
	ret := []*arohcpv1alpha1.ExternalAuth{}

	externalAuthIterator := c.clusterServiceClient.ListExternalAuths(clusterServiceClusterID, "")
	for externalAuth := range externalAuthIterator.Items(ctx) {
		ret = append(ret, externalAuth)
		existingCluster, exists := clusterServiceIDToExternalAuth[externalAuth.HREF()]
		if exists {
			return nil, nil, utils.TrackError(fmt.Errorf("duplicate obj  found: %s, owned by %q and %q", externalAuth.HREF(), existingCluster.ID(), externalAuth.ID()))
		}
		clusterServiceIDToExternalAuth[externalAuth.HREF()] = externalAuth
	}
	if err := externalAuthIterator.GetError(); err != nil {
		return nil, nil, utils.TrackError(err)
	}

	return clusterServiceIDToExternalAuth, ret, nil
}

func (c *cosmosExternalAuthMatching) synchronizeAllExternalAuths(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.resourcesDBClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).Get(ctx, keyObj.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		// no work to do because clusters start without clusterServiceIDs and that means we haven't got any child resources, so they haven't got an orphan.
		return nil
	}

	clusterServiceIDToCosmosExternalAuths, allCosmosExternalAuths, err := c.getAllCosmosObjs(ctx, keyObj)
	if err != nil {
		return utils.TrackError(err)
	}

	clusterServiceIDToClusterServiceExternalAuths, allClusterServiceExternalAuths, err := c.getAllClusterServiceObjs(ctx, *cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	// now make sure that we can find a matching clusterservice cluster for all cosmos clusters
	for _, cosmosExternalAuth := range allCosmosExternalAuths {
		_, exists := clusterServiceIDToClusterServiceExternalAuths[cosmosExternalAuth.ServiceProviderProperties.ClusterServiceID.String()]
		if !exists {
			logger.Error(nil, "cosmos externalAuth doesn't have matching cluster-service externalAuth",
				"cosmosResourceID", cosmosExternalAuth.ID,
				"clusterServiceID", cosmosExternalAuth.ServiceProviderProperties.ClusterServiceID,
			)
		}
	}

	for _, clusterServiceExternalAuth := range allClusterServiceExternalAuths {
		_, exists := clusterServiceIDToCosmosExternalAuths[clusterServiceExternalAuth.HREF()]
		if !exists {
			logger.Error(nil, "cluster service externalAuth doesn't have matching cosmos externalAuth",
				"clusterServiceID", clusterServiceExternalAuth.HREF(),
			)
		}
	}

	// after reporting, do the cleanup
	for _, cosmosExternalAuth := range allCosmosExternalAuths {
		_, exists := clusterServiceIDToClusterServiceExternalAuths[cosmosExternalAuth.ServiceProviderProperties.ClusterServiceID.String()]
		if !exists {
			logger.Info("deleting cosmos externalAuth", "cosmosResourceID", cosmosExternalAuth.ID)
			if err := controllerutils.DeleteRecursively(ctx, c.resourcesDBClient, cosmosExternalAuth.ID); err != nil {
				return utils.TrackError(err)
			}
		}
	}

	return nil
}

func (c *cosmosExternalAuthMatching) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	syncErr := c.synchronizeAllExternalAuths(ctx, keyObj)
	return utils.TrackError(syncErr)
}

func (c *cosmosExternalAuthMatching) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
