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

package nodepooldeletion

import (
	"context"
	"errors"
	"fmt"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolScopedMaestroReadonlyBundlesDeleteController deletes Maestro
// readonly bundles that were created for a NodePool when the NodePool is
// being deleted. It runs after ClusterServiceID has been cleared and
// removes each bundle from the Maestro API, then clears the
// corresponding reference from the ServiceProviderNodePool in Cosmos.
type nodePoolScopedMaestroReadonlyBundlesDeleteController struct {
	cooldownChecker                    controllerutil.CooldownChecker
	nodePoolLister                     listers.NodePoolLister
	serviceProviderNodePoolLister      listers.ServiceProviderNodePoolLister
	resourcesDBClient                  database.ResourcesDBClient
	fleetDBClient                      database.FleetDBClient
	clusterServiceClient               ocm.ClusterServiceClientSpec
	maestroSourceEnvironmentIdentifier string
	maestroClientBuilder               maestro.MaestroClientBuilder
}

var _ controllerutils.NodePoolSyncer = (*nodePoolScopedMaestroReadonlyBundlesDeleteController)(nil)

func NewNodePoolScopedMaestroReadonlyBundlesDeleteController(
	resourcesDBClient database.ResourcesDBClient,
	fleetDBClient database.FleetDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, serviceProviderNodePoolLister := informers.ServiceProviderNodePools()
	syncer := &nodePoolScopedMaestroReadonlyBundlesDeleteController{
		cooldownChecker:                    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:                     nodePoolLister,
		serviceProviderNodePoolLister:      serviceProviderNodePoolLister,
		resourcesDBClient:                  resourcesDBClient,
		fleetDBClient:                      fleetDBClient,
		clusterServiceClient:               clusterServiceClient,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
		maestroClientBuilder:               maestroClientBuilder,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolScopedMaestroReadonlyBundlesDelete",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolScopedMaestroReadonlyBundlesDeleteController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// nodePoolMarkedForDeletion checks if the NodePool has been marked for deletion
// and the ClusterServiceID has been cleared. These are the preconditions for
// this controller to act.
func nodePoolMarkedForDeletion(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID == nil
}

func (c *nodePoolScopedMaestroReadonlyBundlesDeleteController) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if !nodePoolMarkedForDeletion(cachedNodePool) {
		return nil
	}

	cachedSPNP, err := c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool from cache: %w", err))
	}
	if len(cachedSPNP.Status.MaestroReadonlyBundles) == 0 {
		return nil
	}

	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	nodePool, err := nodePoolCRUD.Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool: %w", err))
	}
	if !nodePoolMarkedForDeletion(nodePool) {
		return nil
	}

	spnpCRUD := c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	spnp, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}
	if len(spnp.Status.MaestroReadonlyBundles) == 0 {
		return nil
	}

	spc, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("ServiceProviderCluster not found"))
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	// If the ServiceProviderCluster has no management cluster resource ID, we clear the
	// maestro bundle references from the ServiceProviderNodePool and return, as there's nothing else we can do
	// at that point.
	if spc.Status.ManagementClusterResourceID == nil {
		logger.Info("ServiceProviderCluster has no management cluster resource ID, deferring maestro bundle cleanup to orphaned bundles controller")
		spnp.Status.MaestroReadonlyBundles = nil
		_, err = spnpCRUD.Replace(ctx, spnp, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to persist ServiceProviderNodePool: %w", err))
		}
		return nil
	}

	managementCluster, err := c.fleetDBClient.Stamps().ManagementClusters(spc.Status.ManagementClusterResourceID.Parent.Name).Get(ctx, spc.Status.ManagementClusterResourceID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get management cluster: %w", err))
	}
	// We create a new context with a cancel function so we can cancel the Maestro client when the sync is done.
	// This is important to avoid leaking resources when the sync is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maestroClient, err := c.createMaestroClientFromManagementCluster(ctx, managementCluster, c.maestroClientBuilder, c.maestroSourceEnvironmentIdentifier)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	var syncErrors []error
	var bundlesToRemove []api.MaestroBundleInternalName
	for _, ref := range spnp.Status.MaestroReadonlyBundles {
		if len(ref.MaestroAPIMaestroBundleName) == 0 {
			logger.Info("skipping bundle reference with empty Maestro API name", "bundleInternalName", ref.Name)
			bundlesToRemove = append(bundlesToRemove, ref.Name)
			continue
		}

		logger.Info("sending Maestro readonly bundle delete", "bundleInternalName", ref.Name, "maestroAPIMaestroBundleName", ref.MaestroAPIMaestroBundleName)
		err := maestroClient.Delete(ctx, ref.MaestroAPIMaestroBundleName, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete Maestro Bundle %q: %w", ref.MaestroAPIMaestroBundleName, err)))
			continue
		}

		_, err = maestroClient.Get(ctx, ref.MaestroAPIMaestroBundleName, metav1.GetOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to verify deletion of Maestro Bundle %q: %w", ref.MaestroAPIMaestroBundleName, err)))
			continue
		}
		if err == nil {
			logger.Info("Maestro readonly bundle still exists after delete, will retry", "bundleInternalName", ref.Name, "maestroAPIMaestroBundleName", ref.MaestroAPIMaestroBundleName)
			continue
		}

		logger.Info("deleted Maestro readonly bundle", "bundleInternalName", ref.Name, "maestroAPIMaestroBundleName", ref.MaestroAPIMaestroBundleName)
		bundlesToRemove = append(bundlesToRemove, ref.Name)
	}

	if len(bundlesToRemove) > 0 {
		for _, name := range bundlesToRemove {
			if err := spnp.Status.MaestroReadonlyBundles.Remove(name); err != nil {
				syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to remove bundle reference %q: %w", name, err)))
			}
		}
		_, err = spnpCRUD.Replace(ctx, spnp, nil)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to persist ServiceProviderNodePool after deleting bundles: %w", err)))
		}
	}

	return utils.TrackError(errors.Join(syncErrors...))
}

// createMaestroClientFromManagementCluster creates a Maestro client for the given management cluster.
// the client is scoped to the Consumer Name associated to the management cluster, and to
// the source ID associated to the management cluster and the environment specified
// in maestroSourceEnvironmentIdentifier, which is a configuration parameter at
// deployment time.
func (c *nodePoolScopedMaestroReadonlyBundlesDeleteController) createMaestroClientFromManagementCluster(ctx context.Context, managementCluster *fleet.ManagementCluster, maestroClientBuilder maestro.MaestroClientBuilder, maestroSourceEnvironmentIdentifier string) (maestro.Client, error) {
	maestroRESTAPIEndpoint := managementCluster.Status.MaestroRESTAPIURL
	maestroGRPCAPIEndpoint := managementCluster.Status.MaestroGRPCTarget
	maestroConsumerName := managementCluster.Status.MaestroConsumerName
	maestroSourceID := maestro.GenerateMaestroSourceID(maestroSourceEnvironmentIdentifier, managementCluster.Status.ClusterServiceProvisionShardID.ID())

	maestroClient, err := maestroClientBuilder.NewClient(ctx, maestroRESTAPIEndpoint, maestroGRPCAPIEndpoint, maestroConsumerName, maestroSourceID)
	return maestroClient, err
}
