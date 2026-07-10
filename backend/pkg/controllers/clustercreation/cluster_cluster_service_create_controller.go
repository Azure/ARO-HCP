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

package clustercreation

import (
	"context"
	"fmt"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterClusterServiceCreateSyncer struct {
	cooldownChecker       controllerutil.CooldownChecker
	resourcesDBClient     database.ResourcesDBClient
	clusterLister         listers.ClusterLister
	subscriptionLister    listers.SubscriptionLister
	clustersServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*clusterClusterServiceCreateSyncer)(nil)

func NewClusterClusterServiceCreateController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	syncer := &clusterClusterServiceCreateSyncer{
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:     resourcesDBClient,
		clusterLister:         clusterLister,
		subscriptionLister:    subscriptionLister,
		clustersServiceClient: clustersServiceClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterClusterServiceCreate",
		resourcesDBClient,
		backendInformers,
		nil,
		time.Minute,
		syncer,
	)
}

func (c *clusterClusterServiceCreateSyncer) needsWork(cluster *api.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		(cluster.ServiceProviderProperties.ClusterServiceID == nil ||
			len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0)
}

func (c *clusterClusterServiceCreateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Quick cache lookup first to see if work is needed
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(cluster) {
		return nil
	}

	// Confirm against the live document to make sure the cluster hasn't been deleted or modified since we last checked
	cluster, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(cluster) {
		return nil
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, cluster.ID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	ready, err := c.createPreconditionDesiredVersionResolved(ctx, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if !ready {
		return nil
	}

	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %s has no tenantId", key.SubscriptionID))
	}
	tenantID := *subscription.Properties.TenantId
	mrg := cluster.CustomerProperties.Platform.ManagedResourceGroup

	csCluster, err := ocm.FindClusterByAzureInfo(ctx, c.clustersServiceClient, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, tenantID, mrg)
	if err != nil {
		return utils.TrackError(err)
	}

	if csCluster == nil {
		csCluster, err = c.createClusterServiceCluster(ctx, cluster, existingServiceProviderCluster, tenantID)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create cluster in CS: %w", err))
		}
	}

	csInternalID, err := api.NewInternalID(csCluster.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Storing ClusterServiceID on cluster document", "clusterServiceID", csInternalID.String())
	cluster.ServiceProviderProperties.ClusterServiceID = &csInternalID
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, cluster, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	return nil
}

// createPreconditionDesiredVersionResolved reports whether the ControlPlaneDesiredVersion
// controller has written the Cincinnati-resolved desired version to the ServiceProviderCluster.
// Returns (false, nil) when this controller should wait and retry.
func (c *clusterClusterServiceCreateSyncer) createPreconditionDesiredVersionResolved(ctx context.Context, serviceProviderCluster *api.ServiceProviderCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	if serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion != nil {
		return true, nil
	}
	logger.Info("DesiredVersion not yet set, waiting for ControlPlaneDesiredVersion controller")
	return false, nil
}

func (c *clusterClusterServiceCreateSyncer) createClusterServiceCluster(ctx context.Context, cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster, tenantID string) (*arohcpv1alpha1.Cluster, error) {
	logger := utils.LoggerFromContext(ctx)

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(cluster.ID, tenantID, cluster, nil, nil, serviceProviderCluster)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build CS cluster: %w", err))
	}

	logger.Info("Creating cluster in Cluster Service", "version", serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion.String())
	result, err := c.clustersServiceClient.PostCluster(ctx, csClusterBuilder, csAutoscalerBuilder)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("PostCluster failed: %w", err))
	}

	return result, nil
}

func (c *clusterClusterServiceCreateSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
