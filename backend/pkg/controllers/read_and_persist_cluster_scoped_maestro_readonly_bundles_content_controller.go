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
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer is a controller that reads the Maestro readonly bundles
// references stored in the ServiceProviderCluster resource, retrieves the Maestro readonly bundles using those
// references, extracts the content of the Maestro readonly bundles and persists them in Cosmos.
// It is not responsible for creating the Maestro readonly bundles themselves. That is the responsibility of
// the createMaestroReadonlyBundlesSyncer controller.
// As of now we support reading the content of the Maestro readonly bundle of the Hypershift's HostedCluster associated
// to the Cluster.
// This controller assumes that it has full ownership of the ManagementClusterContent resource.
type readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient database.ResourcesDBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroSourceEnvironmentIdentifier string
	maestroClientBuilder               maestro.MaestroClientBuilder
}

var _ controllerutils.ClusterSyncer = (*readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer)(nil)

func NewReadAndPersistClusterScopedMaestroReadonlyBundlesContentController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
) controllerutils.Controller {

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:                    controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:                  resourcesDBClient,
		clusterServiceClient:               clusterServiceClient,
		activeOperationLister:              activeOperationLister,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
		maestroClientBuilder:               maestroClientBuilder,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ReadAndPersistClusterScopedMaestroReadonlyBundlesContent",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		// we don't have enough information to proceed.  We will retrigger once the information is present.
		// TODO remove this once we have the information all in cosmos.
		return nil
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// We return early if there are no Maestro Bundle references to process.
	if len(existingServiceProviderCluster.Status.MaestroReadonlyBundles) == 0 {
		return nil
	}

	// We get the provision shard (management cluster) the CS cluster is allocated to.
	// As of now in CS the shard allocation occurs synchronously during aro-hcp cluster creation call in CS API so
	// we are guaranteed to have a shard allocated for the cluster. If this changes in the future
	// we would need to change the logic in controllers to check that the retrieved cluster has a
	// shard allocated.
	clusterProvisionShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, *existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard from Cluster Service: %w", err))
	}

	// We create a new context with a cancel function so we can cancel the Maestro client when the sync is done.
	// This is important to avoid leaking resources when the sync is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maestroClient, err := createMaestroClientFromCSProvisionShard(ctx, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder, clusterProvisionShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	managementClusterContentsDBClient := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ManagementClusterContents(key.HCPClusterName)
	serviceProviderClustersDBClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)

	logger := utils.LoggerFromContext(ctx)

	var syncErrors []error
	spcModified := false
	for _, maestroBundleReference := range existingServiceProviderCluster.Status.MaestroReadonlyBundles {
		desiredMCC, bundle, err := readAndPersistMaestroReadonlyBundleContent(ctx, existingCluster.ID, maestroBundleReference, maestroClient, managementClusterContentsDBClient)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to read and persist HostedCluster: %w", err)))
			continue
		}

		if !isStaleMaestroReadonlyBundle(desiredMCC, bundle) {
			continue
		}

		logger.Info("self-healing stale Maestro readonly bundle (AROSLSRE-833): deleting bundle and clearing ID so the create controller can recreate it",
			"bundleName", string(maestroBundleReference.Name),
			"maestroAPIMaestroBundleName", maestroBundleReference.MaestroAPIMaestroBundleName,
			"maestroAPIMaestroBundleID", maestroBundleReference.MaestroAPIMaestroBundleID,
		)

		err = maestroClient.Delete(ctx, maestroBundleReference.MaestroAPIMaestroBundleName, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete stale Maestro readonly bundle %s: %w", maestroBundleReference.Name, err)))
			continue
		}

		maestroBundleReference.MaestroAPIMaestroBundleID = ""
		if setErr := existingServiceProviderCluster.Status.MaestroReadonlyBundles.Set(maestroBundleReference); setErr != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to update Maestro Bundle reference in ServiceProviderCluster: %w", setErr)))
			continue
		}
		spcModified = true
	}

	if spcModified {
		_, err = serviceProviderClustersDBClient.Replace(ctx, existingServiceProviderCluster, nil)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to persist ServiceProviderCluster after clearing stale Maestro bundle IDs: %w", err)))
		}
	}

	return utils.TrackError(errors.Join(syncErrors...))
}

func (c *readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
